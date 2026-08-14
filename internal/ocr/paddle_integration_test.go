package ocr

import (
	"bytes"
	"context"
	"fmt"
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

func TestPaddleSevenImageSemanticRegression(t *testing.T) {
	repository := strings.TrimSpace(os.Getenv("SYMPLLATE_PADDLE_REGRESSION_ROOT"))
	if repository == "" {
		t.Skip("set SYMPLLATE_PADDLE_REGRESSION_ROOT to run the seven-image semantic regression")
	}
	executableDir := filepath.Join(repository, "dist", "portable")
	engine, err := NewPaddleEngine(executableDir, 2*DefaultTimeout, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	fixtures := []string{
		filepath.Join(repository, "_resources", "images", "99920-2296-05-o5fx801v-us-en-tws.pdf", "99920-2296-05-o5fx801v-us-en-tws-14.png"),
		filepath.Join(repository, "_resources", "images", "99920-2296-05-o5fx801v-us-en-tws.pdf", "99920-2296-05-o5fx801v-us-en-tws-18.png"),
	}
	for page := 1; page <= 5; page++ {
		fixtures = append(fixtures, filepath.Join(repository, "_resources", "images", "TAX and Service.pdf", fmt.Sprintf("TAX and Service-%d.png", page)))
	}
	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(filepath.Base(fixture), func(t *testing.T) {
			data, readErr := os.ReadFile(fixture)
			if readErr != nil {
				t.Fatal(readErr)
			}
			configuration, format, decodeErr := image.DecodeConfig(bytes.NewReader(data))
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			mediaType := map[string]string{"png": "image/png", "jpeg": "image/jpeg"}[format]
			page, recognizeErr := engine.RecognizeStructured(context.Background(), translation.ValidatedImage{Data: data, MediaType: mediaType, Width: configuration.Width, Height: configuration.Height}, "en")
			if recognizeErr != nil {
				t.Fatal(recognizeErr)
			}
			maximumLines := 0
			for _, paragraph := range page.Paragraphs {
				if len(paragraph.Lines) > maximumLines {
					maximumLines = len(paragraph.Lines)
				}
				if strings.HasPrefix(filepath.Base(fixture), "TAX and Service-") && paragraph.Box.Width < page.Image.Width/2 && len(paragraph.Lines) > 5 {
					t.Fatalf("UI paragraph is too large: id=%s lines=%d text=%q", paragraph.ID, len(paragraph.Lines), paragraph.Text)
				}
			}
			text := strings.ToLower(plainText(page))
			for _, fragment := range []string{"eans of sof the by an equally", "the oil thout el. the marks."} {
				if strings.Contains(strings.Join(strings.Fields(text), " "), fragment) {
					t.Fatalf("contained OCR fragment survived: %q", fragment)
				}
			}
			t.Logf("semantic OCR words=%d paragraphs=%d max_lines=%d noise=%d duplicates=%d", len(page.Words), len(page.Paragraphs), maximumLines, page.Diagnostics.NonSemanticOCRNoise, page.Diagnostics.MergeDuplicates)
		})
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
