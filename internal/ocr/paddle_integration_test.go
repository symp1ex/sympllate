package ocr

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sympllate/translator/internal/translation"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

func TestPaddleOCRIntegration(t *testing.T) {
	if os.Getenv("SYMPLLATE_RUN_PADDLE_INTEGRATION") != "1" {
		t.Skip("set SYMPLLATE_RUN_PADDLE_INTEGRATION=1 to run the local PaddleOCR smoke test")
	}
	executableDir := os.Getenv("SYMPLLATE_PADDLE_EXECUTABLE_DIR")
	if executableDir == "" {
		executableDir = filepath.Join("..", "..", "dist", "portable")
	}
	if _, err := os.Stat(filepath.Join(executableDir, "bin", "OCR", "det.onnx")); err != nil {
		t.Skipf("local PaddleOCR models unavailable: %v", err)
	}
	engine, err := NewPaddleEngine(executableDir, DefaultTimeout, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := engine.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	}()
	for _, name := range paddleRecognizerNames {
		if _, err := engine.recognizer(name); err != nil {
			t.Fatalf("load recognizer %s: %v", name, err)
		}
	}
	fixture := image.NewNRGBA(image.Rect(0, 0, 640, 120))
	for i := range fixture.Pix {
		fixture.Pix[i] = 255
	}
	drawSyntheticText(t, fixture)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, fixture); err != nil {
		t.Fatal(err)
	}
	page, err := engine.RecognizeStructured(context.Background(), translation.ValidatedImage{Data: encoded.Bytes(), MediaType: "image/png", Width: 640, Height: 120}, "en")
	if err != nil {
		t.Fatal(err)
	}
	if page.Image.Width != 640 || page.Image.Height != 120 {
		t.Fatalf("image info=%+v", page.Image)
	}
	for _, word := range page.Words {
		if word.Box.X < 0 || word.Box.Y < 0 || word.Box.X+word.Box.Width > 640 || word.Box.Y+word.Box.Height > 120 {
			t.Fatalf("word outside image: %+v", word)
		}
	}
	if len(page.Words) == 0 {
		t.Fatal("real model inference returned no text regions")
	}
	if text := strings.ToLower(plainText(page)); !strings.Contains(text, "serial number") || !strings.HasPrefix(text, "engine") {
		t.Fatalf("real model OCR text = %q", text)
	}
}

func TestPaddleDocumentRegression(t *testing.T) {
	if os.Getenv("SYMPLLATE_RUN_PADDLE_INTEGRATION") != "1" {
		t.Skip("set SYMPLLATE_RUN_PADDLE_INTEGRATION=1 to run local document OCR benchmark")
	}
	fixturePath := os.Getenv("SYMPLLATE_PADDLE_DOCUMENT_FIXTURE")
	if fixturePath == "" {
		t.Skip("set SYMPLLATE_PADDLE_DOCUMENT_FIXTURE to a licensed local document image")
	}
	executableDir := os.Getenv("SYMPLLATE_PADDLE_EXECUTABLE_DIR")
	if executableDir == "" {
		executableDir = filepath.Join("..", "..", "dist", "portable")
	}
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	configuration, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	mediaType := map[string]string{"png": "image/png", "jpeg": "image/jpeg"}[format]
	if mediaType == "" {
		t.Fatalf("unsupported format %q", format)
	}
	engine, err := NewPaddleEngine(executableDir, 2*DefaultTimeout, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	page, err := engine.RecognizeStructured(context.Background(), translation.ValidatedImage{Data: data, MediaType: mediaType, Width: configuration.Width, Height: configuration.Height}, "en")
	if err != nil {
		t.Fatal(err)
	}
	metrics := EvaluateOCR(page, EvaluationFixture{ExpectedStrings: []string{"PREPARATION", "Engine Oil", "Engine Oil Capacity", "With mark", "Without mark", "FX801V", "FX751V", "2.3 L", "2.5 L", "2.1 L", "oil filter is removed"}})
	t.Logf("document metrics: %+v diagnostics: %+v", metrics, page.Diagnostics)
	if metrics.WordRecall < .75 || metrics.DuplicateRate > .08 {
		t.Fatalf("document regression below minimum quality: %+v", metrics)
	}
	if page.Diagnostics.Tiles == 0 {
		t.Fatalf("document profile did not use tiles: %+v", page.Diagnostics)
	}
}

func drawSyntheticText(t *testing.T, img *image.NRGBA) {
	t.Helper()
	parsed, err := opentype.Parse(goregular.TTF)
	if err != nil {
		t.Fatal(err)
	}
	face, err := opentype.NewFace(parsed, &opentype.FaceOptions{Size: 36, DPI: 96, Hinting: font.HintingFull})
	if err != nil {
		t.Fatal(err)
	}
	defer face.Close()
	drawer := font.Drawer{Dst: img, Src: image.NewUniform(color.Black), Face: face, Dot: fixed.P(30, 76)}
	drawer.DrawString("ENGINE SERIAL NUMBER")
}
