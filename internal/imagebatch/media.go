package imagebatch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sympllate/translator/internal/ffmpeg"
	"github.com/sympllate/translator/internal/translation"
)

const (
	defaultFFmpegTimeout = ffmpeg.DefaultTimeout
)

type processRunner = ffmpeg.Runner
type execRunner = ffmpeg.ExecRunner

type ffmpegAdapter struct {
	executable string
	timeout    time.Duration
	runner     processRunner
}

func newFFmpegAdapter(executableDir string) *ffmpegAdapter {
	return &ffmpegAdapter{executable: filepath.Join(executableDir, "bin", "ffmpeg", "ffmpeg.exe"), timeout: defaultFFmpegTimeout, runner: execRunner{}}
}

type preparedSource struct {
	Original  []byte
	Validated translation.ValidatedImage
	Image     *image.NRGBA
	Extension string
}

func prepareSource(ctx context.Context, path string, adapter *ffmpegAdapter) (preparedSource, error) {
	if err := ctx.Err(); err != nil {
		return preparedSource{}, err
	}
	data, err := readLimitedFile(path, translation.MaxImageBytes)
	if err != nil {
		return preparedSource{}, err
	}
	extension := strings.ToLower(filepath.Ext(path))
	mediaType := ""
	if extension == ".png" {
		mediaType = "image/png"
	}
	if extension == ".jpg" || extension == ".jpeg" {
		mediaType = "image/jpeg"
	}
	normalized := data
	if mediaType == "" {
		normalized, err = adapter.Normalize(ctx, path)
		if err != nil {
			return preparedSource{}, err
		}
		mediaType = "image/png"
	}
	validated, err := translation.ValidateImageData(normalized, mediaType)
	if err != nil {
		return preparedSource{}, err
	}
	decoded, _, err := image.Decode(bytes.NewReader(normalized))
	if err != nil {
		return preparedSource{}, fmt.Errorf("decode image: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return preparedSource{}, err
	}
	canvas := image.NewNRGBA(image.Rect(0, 0, decoded.Bounds().Dx(), decoded.Bounds().Dy()))
	draw.Draw(canvas, canvas.Bounds(), decoded, decoded.Bounds().Min, draw.Src)
	return preparedSource{Original: data, Validated: validated, Image: canvas, Extension: extension}, nil
}

func readLimitedFile(path string, limit int) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open image: %w", safePathError(err))
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, fmt.Errorf("read image: %w", safePathError(err))
	}
	if len(data) > limit {
		return nil, fmt.Errorf("image is too large: maximum %d bytes", limit)
	}
	return data, nil
}

func (a *ffmpegAdapter) Normalize(ctx context.Context, input string) ([]byte, error) {
	if err := requireTool(a.executable, "FFmpeg"); err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp("", "sympllate-render-")
	if err != nil {
		return nil, fmt.Errorf("create image temporary directory: %w", safePathError(err))
	}
	defer os.RemoveAll(directory)
	output := filepath.Join(directory, "normalized.png")
	if err := a.run(ctx, []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-noautorotate", "-i", input, "-frames:v", "1", output}); err != nil {
		return nil, fmt.Errorf("normalize image with FFmpeg: %w", err)
	}
	if err := ffmpeg.VerifyPNG(output); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(output)
	if err != nil {
		return nil, errors.New("FFmpeg did not create a normalized image")
	}
	if _, err := translation.ValidateImageData(data, "image/png"); err != nil {
		return nil, fmt.Errorf("verify FFmpeg normalized image: %w", err)
	}
	return data, nil
}

func (a *ffmpegAdapter) Encode(ctx context.Context, sourcePNG, destination string, width, height int) error {
	if err := requireTool(a.executable, "FFmpeg"); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".sympllate-*"+filepath.Ext(destination))
	if err != nil {
		return fmt.Errorf("create output temporary file: %w", safePathError(err))
	}
	temporaryPath := temporary.Name()
	_ = temporary.Close()
	_ = os.Remove(temporaryPath)
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := a.run(ctx, []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-i", sourcePNG, "-frames:v", "1", temporaryPath}); err != nil {
		return fmt.Errorf("Не удалось сохранить изображение в исходном формате: %w", err)
	}
	if err := verifyRegularOutput(temporaryPath); err != nil {
		return err
	}
	verified, err := a.Normalize(ctx, temporaryPath)
	if err != nil {
		return fmt.Errorf("verify encoded image: %w", err)
	}
	configuration, decodeErr := png.DecodeConfig(bytes.NewReader(verified))
	if decodeErr != nil || configuration.Width != width || configuration.Height != height {
		return errors.New("encoded image dimensions changed")
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return fmt.Errorf("commit image: %w", safePathError(err))
	}
	keep = true
	return nil
}

func (a *ffmpegAdapter) run(ctx context.Context, args []string) error {
	return ffmpeg.Run(ctx, a.executable, args, a.timeout, a.runner)
}

func requireTool(path, name string) error {
	return ffmpeg.RequireExecutable(path, name)
}

func encodeRendered(ctx context.Context, source *image.NRGBA, destination, extension string, jpegQuality int, adapter *ffmpegAdapter) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch extension {
	case ".png", ".jpg", ".jpeg":
		return atomicEncodeGoImage(ctx, source, destination, extension, jpegQuality)
	default:
		directory, err := os.MkdirTemp("", "sympllate-encode-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(directory)
		pngPath := filepath.Join(directory, "rendered.png")
		if err := atomicEncodeGoImage(ctx, source, pngPath, ".png", jpegQuality); err != nil {
			return err
		}
		return adapter.Encode(ctx, pngPath, destination, source.Bounds().Dx(), source.Bounds().Dy())
	}
}

func atomicEncodeGoImage(ctx context.Context, source image.Image, destination, extension string, jpegQuality int) error {
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".sympllate-*"+extension)
	if err != nil {
		return fmt.Errorf("create output temporary file: %w", safePathError(err))
	}
	temporaryPath := temporary.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := ctx.Err(); err != nil {
		_ = temporary.Close()
		return err
	}
	writer := &contextWriter{ctx: ctx, writer: temporary}
	if extension == ".png" {
		err = png.Encode(writer, source)
	} else {
		err = jpeg.Encode(writer, source, &jpeg.Options{Quality: jpegQuality})
	}
	if err != nil {
		_ = temporary.Close()
		return fmt.Errorf("encode output image: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := verifyGoImage(temporaryPath, source.Bounds().Dx(), source.Bounds().Dy()); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return fmt.Errorf("commit image: %w", safePathError(err))
	}
	keep = true
	return nil
}

type contextWriter struct {
	ctx    context.Context
	writer io.Writer
}

func (w *contextWriter) Write(buffer []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.writer.Write(buffer)
}

func verifyGoImage(path string, width, height int) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	configuration, _, err := image.DecodeConfig(file)
	if err != nil {
		return fmt.Errorf("verify output image: %w", err)
	}
	if configuration.Width != width || configuration.Height != height {
		return errors.New("encoded image dimensions changed")
	}
	return nil
}

func verifyRegularOutput(path string) error {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("FFmpeg output is missing or empty")
	}
	return nil
}
