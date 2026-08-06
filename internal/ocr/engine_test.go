package ocr

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sympllate/translator/internal/translation"
)

func TestEngineRecognizeUsesLanguageAndRemovesTemporaryDirectory(t *testing.T) {
	t.Parallel()
	engine := newTestEngine(t, "eng")
	var temporaryDir string
	engine.run = func(_ context.Context, executable string, args []string, stdout, _ io.Writer) error {
		if executable != engine.executablePath {
			t.Errorf("executable = %q", executable)
		}
		if len(args) != 6 || args[1] != "stdout" || args[2] != "-l" || args[3] != "eng" || args[4] != "--tessdata-dir" || args[5] != engine.tessdataDir {
			t.Errorf("args = %q", args)
		}
		temporaryDir = filepath.Dir(args[0])
		data, err := os.ReadFile(args[0])
		if err != nil || string(data) != "image-bytes" {
			t.Errorf("temporary image = %q, %v", data, err)
		}
		_, _ = io.WriteString(stdout, "  recognized text\r\n")
		return nil
	}
	text, err := engine.Recognize(context.Background(), testImage(), "en")
	if err != nil || text != "recognized text" {
		t.Fatalf("Recognize() = %q, %v", text, err)
	}
	if _, err := os.Stat(temporaryDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary directory still exists: %q, %v", temporaryDir, err)
	}
}

func TestEngineAutoUsesInstalledLanguages(t *testing.T) {
	t.Parallel()
	engine := newTestEngine(t, "rus", "eng", "osd")
	engine.run = func(_ context.Context, _ string, args []string, stdout, _ io.Writer) error {
		if args[3] != "eng+rus" {
			t.Errorf("OCR languages = %q", args[3])
		}
		_, _ = io.WriteString(stdout, "text")
		return nil
	}
	if _, err := engine.Recognize(context.Background(), testImage(), "auto"); err != nil {
		t.Fatal(err)
	}
}

func TestEngineRecognizeStructuredUsesTSVAndPSM(t *testing.T) {
	t.Parallel()
	engine := newTestEngine(t, "eng")
	engine.run = func(_ context.Context, _ string, args []string, stdout, _ io.Writer) error {
		wantSuffix := []string{"--psm", "3", "tsv"}
		if len(args) != 9 {
			t.Fatalf("args=%q", args)
		}
		for index, value := range wantSuffix {
			if args[len(args)-3+index] != value {
				t.Fatalf("args=%q", args)
			}
		}
		_, _ = io.WriteString(stdout, tsvHeader+tsvWord(1, 1, 1, 1, 1, "90", "text"))
		return nil
	}
	image := testImage()
	image.Width = 100
	image.Height = 100
	page, err := engine.RecognizeStructured(context.Background(), image, "en")
	if err != nil || len(page.Paragraphs) != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
}

func TestEngineReportsMissingBinaryAndLanguageData(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	engine := New(base, time.Second)
	capability := engine.Capability()
	if capability.Supported || !strings.Contains(capability.Reason, filepath.Join("bin", "tesseract", "tesseract.exe")) {
		t.Fatalf("Capability() = %+v", capability)
	}
	if _, err := engine.Recognize(context.Background(), testImage(), "en"); err == nil || !strings.Contains(err.Error(), "requires Tesseract OCR") {
		t.Fatalf("Recognize() error = %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(engine.executablePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(engine.executablePath, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	capability = engine.Capability()
	if capability.Supported || !strings.Contains(capability.Reason, "language data") {
		t.Fatalf("Capability() after executable = %+v", capability)
	}
}

func TestEngineReportsMissingRequestedLanguage(t *testing.T) {
	t.Parallel()
	engine := newTestEngine(t, "eng")
	if _, err := engine.Recognize(context.Background(), testImage(), "ru"); err == nil || !strings.Contains(err.Error(), "rus.traineddata") {
		t.Fatalf("Recognize() error = %v", err)
	}
}

func TestEngineSupportsTesseractDirectlyInBin(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(filepath.Join(bin, "tessdata"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "tesseract.exe"), []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "tessdata", "eng.traineddata"), []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	engine := New(base, time.Second)
	if engine.executablePath != filepath.Join(bin, "tesseract.exe") || !engine.Capability().Supported {
		t.Fatalf("engine=%+v capability=%+v", engine, engine.Capability())
	}
}

func TestEngineTimeoutAndCancellation(t *testing.T) {
	t.Parallel()
	engine := newTestEngine(t, "eng")
	engine.timeout = 5 * time.Millisecond
	engine.run = func(ctx context.Context, _ string, _ []string, _, _ io.Writer) error {
		<-ctx.Done()
		return ctx.Err()
	}
	if _, err := engine.Recognize(context.Background(), testImage(), "en"); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("Recognize(timeout) error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := engine.Recognize(ctx, testImage(), "en"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Recognize(canceled) error = %v", err)
	}
}

func TestEngineRemovesTemporaryDirectoryAfterFailure(t *testing.T) {
	t.Parallel()
	engine := newTestEngine(t, "eng")
	var temporaryDir string
	engine.run = func(_ context.Context, _ string, args []string, _, stderr io.Writer) error {
		temporaryDir = filepath.Dir(args[0])
		_, _ = io.WriteString(stderr, "decoder failed")
		return errors.New("exit status 1")
	}
	if _, err := engine.Recognize(context.Background(), testImage(), "en"); err == nil || !strings.Contains(err.Error(), "decoder failed") {
		t.Fatalf("Recognize() error = %v", err)
	}
	if _, err := os.Stat(temporaryDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary directory still exists: %q, %v", temporaryDir, err)
	}
}

func newTestEngine(t *testing.T, languages ...string) *Engine {
	t.Helper()
	base := t.TempDir()
	engine := New(base, time.Second)
	if err := os.MkdirAll(engine.tessdataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(engine.executablePath, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, language := range languages {
		if err := os.WriteFile(filepath.Join(engine.tessdataDir, language+".traineddata"), []byte("test"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return engine
}

func testImage() translation.ValidatedImage {
	return translation.ValidatedImage{Data: []byte("image-bytes"), MediaType: "image/png"}
}
