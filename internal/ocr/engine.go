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

	"github.com/sympllate/translator/internal/translation"
)

const (
	DefaultTimeout = 30 * time.Second
	maxOutputBytes = 2 << 20
	maxStderrBytes = 64 << 10
)

type commandRunner func(ctx context.Context, executable string, args []string, stdout, stderr io.Writer) error

type Engine struct {
	executablePath string
	tessdataDir    string
	timeout        time.Duration
	run            commandRunner
}

func New(executableDir string, timeout time.Duration) *Engine {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	tesseractDir := filepath.Join(executableDir, "bin", "tesseract")
	return &Engine{
		executablePath: filepath.Join(tesseractDir, "tesseract.exe"),
		tessdataDir:    filepath.Join(tesseractDir, "tessdata"),
		timeout:        timeout,
		run:            runCommand,
	}
}

func (e *Engine) Capability() translation.ImageCapability {
	if err := requireRegularFile(e.executablePath); err != nil {
		return translation.ImageCapability{
			Supported: false,
			Reason:    fmt.Sprintf("local image translation requires Tesseract OCR at %q: %v", e.executablePath, err),
		}
	}
	languages, err := e.availableLanguages()
	if err != nil || len(languages) == 0 {
		if err == nil {
			err = errors.New("no .traineddata files found")
		}
		return translation.ImageCapability{
			Supported: false,
			Reason:    fmt.Sprintf("local image translation requires Tesseract language data in %q: %v", e.tessdataDir, err),
		}
	}
	return translation.ImageCapability{Supported: true}
}

func (e *Engine) Recognize(ctx context.Context, image translation.ValidatedImage, source string) (string, error) {
	capability := e.Capability()
	if !capability.Supported {
		return "", errors.New(capability.Reason)
	}
	languages, err := e.languagesForSource(source)
	if err != nil {
		return "", err
	}
	temporaryDir, err := os.MkdirTemp("", "sympllate-ocr-")
	if err != nil {
		return "", fmt.Errorf("create OCR temporary directory: %w", err)
	}
	defer os.RemoveAll(temporaryDir)
	extension := ".png"
	if image.MediaType == "image/jpeg" {
		extension = ".jpg"
	}
	inputPath := filepath.Join(temporaryDir, "input"+extension)
	if err := os.WriteFile(inputPath, image.Data, 0o600); err != nil {
		return "", fmt.Errorf("write OCR temporary image: %w", err)
	}

	ocrContext, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	stdout := &cappedBuffer{limit: maxOutputBytes}
	stderr := &cappedBuffer{limit: maxStderrBytes}
	args := []string{inputPath, "stdout", "-l", strings.Join(languages, "+"), "--tessdata-dir", e.tessdataDir}
	err = e.run(ocrContext, e.executablePath, args, stdout, stderr)
	if err != nil {
		if errors.Is(ocrContext.Err(), context.Canceled) && errors.Is(ctx.Err(), context.Canceled) {
			return "", context.Canceled
		}
		if errors.Is(ocrContext.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("Tesseract OCR timed out after %s", e.timeout)
		}
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return "", fmt.Errorf("Tesseract OCR failed: %w: %s", err, message)
		}
		return "", fmt.Errorf("start Tesseract OCR (verify tesseract.exe and its DLLs): %w", err)
	}
	if stdout.exceeded {
		return "", fmt.Errorf("Tesseract OCR output is too large: maximum %d bytes", maxOutputBytes)
	}
	return strings.TrimSpace(stdout.String()), nil
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
