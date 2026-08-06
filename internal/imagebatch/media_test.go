package imagebatch

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeProcessRunner struct {
	run  func(context.Context, string, []string, io.Writer, io.Writer) error
	args [][]string
}

func (f *fakeProcessRunner) Run(ctx context.Context, executable string, args []string, stdout, stderr io.Writer) error {
	f.args = append(f.args, append([]string(nil), args...))
	if f.run != nil {
		return f.run(ctx, executable, args, stdout, stderr)
	}
	return nil
}

func TestFFmpegNormalizeAndEncodeUseArgumentListsAndVerifyOutput(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "ffmpeg.exe")
	if err := os.WriteFile(executable, []byte("fake"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeProcessRunner{run: func(_ context.Context, _ string, args []string, _, _ io.Writer) error {
		output := args[len(args)-1]
		if strings.EqualFold(filepath.Ext(output), ".png") {
			var buffer bytes.Buffer
			if err := png.Encode(&buffer, solidNRGBA(8, 6, color.NRGBA{R: 240, G: 240, B: 240, A: 255})); err != nil {
				return err
			}
			return os.WriteFile(output, buffer.Bytes(), 0o600)
		}
		return os.WriteFile(output, []byte("encoded"), 0o600)
	}}
	adapter := &ffmpegAdapter{executable: executable, timeout: time.Second, runner: runner}
	input := filepath.Join(directory, "input.bmp")
	if err := os.WriteFile(input, []byte("bitmap"), 0o600); err != nil {
		t.Fatal(err)
	}
	normalized, err := adapter.Normalize(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	configuration, err := png.DecodeConfig(bytes.NewReader(normalized))
	if err != nil || configuration.Width != 8 || configuration.Height != 6 {
		t.Fatalf("config=%+v err=%v", configuration, err)
	}
	workingPNG := filepath.Join(directory, "working.png")
	if err := os.WriteFile(workingPNG, normalized, 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(directory, "result.webp")
	if err := adapter.Encode(context.Background(), workingPNG, destination, 8, 6); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(destination); err != nil || info.Size() == 0 {
		t.Fatalf("info=%v err=%v", info, err)
	}
	for _, args := range runner.args {
		for _, argument := range args {
			if strings.EqualFold(argument, "cmd.exe") || strings.EqualFold(argument, "powershell.exe") {
				t.Fatalf("shell argument: %v", args)
			}
		}
	}
}

func TestFFmpegErrorsTimeoutCancellationAndMissingOutput(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "ffmpeg.exe")
	if err := os.WriteFile(executable, []byte("fake"), 0o600); err != nil {
		t.Fatal(err)
	}
	adapter := &ffmpegAdapter{executable: executable, timeout: 5 * time.Millisecond, runner: &fakeProcessRunner{run: func(ctx context.Context, _ string, _ []string, _, _ io.Writer) error { <-ctx.Done(); return ctx.Err() }}}
	if _, err := adapter.Normalize(context.Background(), "input.bmp"); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := adapter.Normalize(ctx, "input.bmp"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	adapter.timeout = time.Second
	adapter.runner = &fakeProcessRunner{run: func(_ context.Context, _ string, _ []string, _, stderr io.Writer) error {
		_, _ = stderr.Write(bytes.Repeat([]byte("x"), 100_000))
		return errors.New("exit 1")
	}}
	if _, err := adapter.Normalize(context.Background(), "input.bmp"); err == nil || len(err.Error()) > 700 {
		t.Fatalf("err length=%d err=%v", len(err.Error()), err)
	}
	adapter.runner = &fakeProcessRunner{}
	if _, err := adapter.Normalize(context.Background(), "input.bmp"); err == nil || !strings.Contains(err.Error(), "did not create") {
		t.Fatalf("err=%v", err)
	}
	adapter.executable = filepath.Join(directory, "missing.exe")
	if _, err := adapter.Normalize(context.Background(), "input.bmp"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err=%v", err)
	}
}

func TestPrepareSourceUsesFFmpegForNonGoFormats(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "ffmpeg.exe")
	_ = os.WriteFile(executable, []byte("fake"), 0o600)
	runner := &fakeProcessRunner{run: func(_ context.Context, _ string, args []string, _, _ io.Writer) error {
		file, err := os.Create(args[len(args)-1])
		if err != nil {
			return err
		}
		defer file.Close()
		return png.Encode(file, image.NewNRGBA(image.Rect(0, 0, 12, 9)))
	}}
	adapter := &ffmpegAdapter{executable: executable, timeout: time.Second, runner: runner}
	input := filepath.Join(directory, "page.tiff")
	original := []byte("fake-tiff")
	if err := os.WriteFile(input, original, 0o600); err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareSource(context.Background(), input, adapter)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(prepared.Original, original) || prepared.Extension != ".tiff" || prepared.Validated.Width != 12 || prepared.Image.Bounds().Dy() != 9 {
		t.Fatalf("prepared=%+v", prepared)
	}
}
