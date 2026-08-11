package ocr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sympllate/translator/internal/ffmpeg"
	"github.com/sympllate/translator/internal/translation"
)

const (
	DefaultTimeout          = 30 * time.Second
	maxStderrBytes          = 64 << 10
	maxTileOutputBytes      = 2 << 20
	maxTotalTesseractPasses = 1 + maxOCRTiles
)

type commandRunner func(ctx context.Context, executable string, args []string, stdout, stderr io.Writer) error

func (runner commandRunner) Run(ctx context.Context, executable string, args []string, stdout, stderr io.Writer) error {
	return runner(ctx, executable, args, stdout, stderr)
}

type Engine struct {
	executablePath string
	ffmpegPath     string
	tessdataDir    string
	timeout        time.Duration
	run            commandRunner
}

func New(executableDir string, timeout time.Duration) *Engine {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	tesseractDir := filepath.Join(executableDir, "bin", "tesseract")
	// Keep the phase-one nested layout, while also accepting the documented
	// portable layout where tesseract.exe and tessdata live directly in bin.
	if err := requireRegularFile(filepath.Join(tesseractDir, "tesseract.exe")); err != nil {
		directDirectory := filepath.Join(executableDir, "bin")
		if directErr := requireRegularFile(filepath.Join(directDirectory, "tesseract.exe")); directErr == nil {
			tesseractDir = directDirectory
		}
	}
	execRunner := ffmpeg.ExecRunner{}
	return &Engine{
		executablePath: filepath.Join(tesseractDir, "tesseract.exe"),
		ffmpegPath:     filepath.Join(executableDir, "bin", "ffmpeg", "ffmpeg.exe"),
		tessdataDir:    filepath.Join(tesseractDir, "tessdata"),
		timeout:        timeout,
		run:            execRunner.Run,
	}
}

func (e *Engine) Capability() translation.ImageCapability {
	if err := requireRegularFile(e.executablePath); err != nil {
		return translation.ImageCapability{Supported: false, Reason: fmt.Sprintf("local image translation requires Tesseract OCR at %q: %v", e.executablePath, err)}
	}
	if err := ffmpeg.RequireExecutable(e.ffmpegPath, "FFmpeg"); err != nil {
		return translation.ImageCapability{Supported: false, Reason: fmt.Sprintf("local image translation requires FFmpeg preprocessing: %v", err)}
	}
	languages, err := e.availableLanguages()
	if err != nil || len(languages) == 0 {
		if err == nil {
			err = errors.New("no .traineddata files found")
		}
		return translation.ImageCapability{Supported: false, Reason: fmt.Sprintf("local image translation requires Tesseract language data in %q: %v", e.tessdataDir, err)}
	}
	return translation.ImageCapability{Supported: true}
}

func (e *Engine) ValidateSource(source string) error {
	capability := e.Capability()
	if !capability.Supported {
		return errors.New(capability.Reason)
	}
	_, err := e.languagesForSource(source)
	return err
}

func (e *Engine) Recognize(ctx context.Context, image translation.ValidatedImage, source string) (string, error) {
	page, err := e.RecognizeStructured(ctx, image, source)
	if err != nil {
		return "", err
	}
	return plainText(page), nil
}

func (e *Engine) RecognizeStructured(ctx context.Context, image translation.ValidatedImage, source string) (OCRPage, error) {
	if image.Width <= 0 || image.Height <= 0 {
		return OCRPage{}, errors.New("OCR image dimensions must be positive")
	}
	capability := e.Capability()
	if !capability.Supported {
		return OCRPage{}, errors.New(capability.Reason)
	}
	languages, err := e.languagesForSource(source)
	if err != nil {
		return OCRPage{}, err
	}
	ocrContext, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	if err := ocrContext.Err(); err != nil {
		return OCRPage{}, e.contextError(ctx, ocrContext, err)
	}
	temporaryDir, err := os.MkdirTemp("", "sympllate-ocr-")
	if err != nil {
		return OCRPage{}, fmt.Errorf("create OCR temporary directory: %w", err)
	}
	defer os.RemoveAll(temporaryDir)

	inputPath := filepath.Join(temporaryDir, "input"+imageExtension(image.MediaType))
	if err := os.WriteFile(inputPath, image.Data, 0o600); err != nil {
		return OCRPage{}, fmt.Errorf("write OCR temporary image: %w", err)
	}
	if err := ocrContext.Err(); err != nil {
		return OCRPage{}, e.contextError(ctx, ocrContext, err)
	}
	plan := calculateOCRPlan(image.Width, image.Height)
	fullPage, err := e.runPass(ocrContext, inputPath, temporaryDir, plan.Full, languages, maxStructuredOutputBytes)
	if err != nil {
		return OCRPage{}, e.contextError(ctx, ocrContext, err)
	}
	words := projectWords(fullPage.Words, plan.Full, image.Width, image.Height)
	if shouldUseTiles(plan.Full, words) {
		for index, pass := range plan.Tiles {
			if err := ocrContext.Err(); err != nil {
				return OCRPage{}, e.contextError(ctx, ocrContext, err)
			}
			if index+2 > maxTotalTesseractPasses {
				break
			}
			tilePage, passErr := e.runPass(ocrContext, inputPath, temporaryDir, pass, languages, maxTileOutputBytes)
			if passErr != nil {
				return OCRPage{}, e.contextError(ctx, ocrContext, passErr)
			}
			words = mergeOCRWords(words, projectAcceptedWords(tilePage.Words, pass, image.Width, image.Height))
		}
	}
	page := rebuildOCRPage(words, OCRImageInfo{Width: image.Width, Height: image.Height, MediaType: image.MediaType})
	return page, nil
}

func (e *Engine) runPass(ctx context.Context, inputPath, temporaryDir string, pass ocrPass, languages []string, outputLimit int) (OCRPage, error) {
	outputPath := filepath.Join(temporaryDir, pass.name+".png")
	filter := fmt.Sprintf("crop=%d:%d:%d:%d,scale=%d:%d:flags=lanczos,format=gray,eq=contrast=1.08,unsharp=5:5:0.45:3:3:0.0",
		pass.crop.Width, pass.crop.Height, pass.crop.X, pass.crop.Y, pass.width, pass.height)
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-noautorotate", "-i", inputPath, "-vf", filter, "-frames:v", "1", outputPath}
	if err := ffmpeg.Run(ctx, e.ffmpegPath, args, e.timeout, e.run); err != nil {
		return OCRPage{}, fmt.Errorf("preprocess OCR image with FFmpeg: %w", err)
	}
	width, height, err := ffmpeg.PNGDimensions(outputPath)
	if err != nil {
		return OCRPage{}, fmt.Errorf("verify OCR preprocessing output: %w", err)
	}
	if width != pass.width || height != pass.height {
		return OCRPage{}, fmt.Errorf("verify OCR preprocessing output: FFmpeg created %dx%d PNG; expected %dx%d", width, height, pass.width, pass.height)
	}

	stdout := &cappedBuffer{limit: outputLimit}
	stderr := &cappedBuffer{limit: maxStderrBytes}
	tesseractArgs := []string{outputPath, "stdout", "-l", strings.Join(languages, "+"), "--tessdata-dir", e.tessdataDir, "--psm", fmt.Sprint(pass.psm), "tsv"}
	err = e.run(ctx, e.executablePath, tesseractArgs, stdout, stderr)
	if err != nil {
		message := redactOCRPaths(strings.TrimSpace(stderr.String()), outputPath, e.tessdataDir)
		if message != "" {
			return OCRPage{}, fmt.Errorf("Tesseract OCR failed: %w: %s", err, message)
		}
		return OCRPage{}, fmt.Errorf("start Tesseract OCR (verify tesseract.exe and its DLLs): %w", err)
	}
	if stdout.exceeded {
		return OCRPage{}, fmt.Errorf("Tesseract OCR output is too large: maximum %d bytes", outputLimit)
	}
	page, err := ParseTSV(strings.NewReader(stdout.String()), pass.width, pass.height, DefaultFilterConfig())
	if err != nil {
		return OCRPage{}, fmt.Errorf("parse Tesseract TSV: %w", err)
	}
	return page, nil
}

func redactOCRPaths(message string, paths ...string) string {
	for _, path := range paths {
		if path == "" {
			continue
		}
		message = strings.ReplaceAll(message, path, "<path>")
		message = strings.ReplaceAll(message, strings.ReplaceAll(path, `\`, `/`), "<path>")
	}
	return message
}

func (e *Engine) contextError(parent, pipeline context.Context, err error) error {
	if errors.Is(parent.Err(), context.Canceled) {
		return context.Canceled
	}
	if errors.Is(pipeline.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("OCR timed out after %s", e.timeout)
	}
	return err
}

func imageExtension(mediaType string) string {
	if mediaType == "image/jpeg" {
		return ".jpg"
	}
	return ".png"
}

func (e *Engine) languagesForSource(source string) ([]string, error) {
	if source == "auto" {
		languages, err := e.availableLanguages()
		if err != nil {
			return nil, fmt.Errorf("read Tesseract language data: %w", err)
		}
		if len(languages) == 0 {
			return nil, fmt.Errorf("no Tesseract .traineddata files found in %q", e.tessdataDir)
		}
		return languages, nil
	}
	code := source
	if separator := strings.IndexAny(code, "-_"); separator >= 0 {
		code = code[:separator]
	}
	language, ok := tesseractLanguageCodes[strings.ToLower(code)]
	if !ok {
		return nil, fmt.Errorf("Tesseract OCR does not have a language mapping for source %q", source)
	}
	path := filepath.Join(e.tessdataDir, language+".traineddata")
	if err := requireRegularFile(path); err != nil {
		return nil, fmt.Errorf("Tesseract language data for %q is missing at %q", source, path)
	}
	return []string{language}, nil
}

func (e *Engine) availableLanguages() ([]string, error) {
	entries, err := os.ReadDir(e.tessdataDir)
	if err != nil {
		return nil, err
	}
	languages := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".traineddata") {
			continue
		}
		language := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		if language == "osd" || language == "equ" {
			continue
		}
		languages = append(languages, language)
	}
	sort.Strings(languages)
	return languages, nil
}

func requireRegularFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("not a regular file")
	}
	return nil
}

type cappedBuffer struct {
	data     []byte
	limit    int
	exceeded bool
}

func (b *cappedBuffer) Write(value []byte) (int, error) {
	remaining := b.limit - len(b.data)
	if remaining > 0 {
		count := len(value)
		if count > remaining {
			count = remaining
		}
		b.data = append(b.data, value[:count]...)
	}
	if len(value) > remaining {
		b.exceeded = true
	}
	return len(value), nil
}

func (b *cappedBuffer) String() string { return string(b.data) }

var tesseractLanguageCodes = map[string]string{
	"ar": "ara",
	"de": "deu",
	"en": "eng",
	"es": "spa",
	"fr": "fra",
	"it": "ita",
	"ja": "jpn",
	"ko": "kor",
	"pl": "pol",
	"pt": "por",
	"ru": "rus",
	"tr": "tur",
	"uk": "ukr",
	"zh": "chi_sim",
}
