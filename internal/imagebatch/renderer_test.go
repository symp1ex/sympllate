package imagebatch

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/sympllate/translator/internal/ocr"
)

func TestCleanupChangesOnlyConfirmedBoxesAndPreservesAlpha(t *testing.T) {
	source := solidNRGBA(20, 20, color.NRGBA{R: 10, G: 20, B: 30, A: 111})
	renderer, err := NewRenderer(t.TempDir(), DefaultRenderConfig(), &fakeInpaintEngine{})
	if err != nil {
		t.Fatal(err)
	}
	document := RenderDocument{Blocks: []RenderBlock{{CleanupBox: ocr.OCRBox{X: 5, Y: 6, Width: 4, Height: 3}, Background: newRenderColor(color.NRGBA{R: 200, G: 210, B: 220, A: 123}), CleanupMode: CleanupSolid}}}
	cleaned, _, err := renderer.Clean(context.Background(), source, document)
	if err != nil {
		t.Fatal(err)
	}
	if got := cleaned.NRGBAAt(6, 7); got != (color.NRGBA{R: 200, G: 210, B: 220, A: 123}) {
		t.Fatalf("inside=%+v", got)
	}
	if got := cleaned.NRGBAAt(4, 7); got != source.NRGBAAt(4, 7) {
		t.Fatalf("outside changed: %+v", got)
	}
	if got := source.NRGBAAt(6, 7); got != (color.NRGBA{R: 10, G: 20, B: 30, A: 111}) {
		t.Fatalf("source mutated: %+v", got)
	}
}

func TestPrepareSkipsUntranslatedBlockAndDrawsCyrillic(t *testing.T) {
	directory := t.TempDir()
	writeTestFont(t, directory)
	renderer, err := NewRenderer(directory, DefaultRenderConfig(), &fakeInpaintEngine{})
	if err != nil {
		t.Fatal(err)
	}
	defer renderer.Close()
	source := solidNRGBA(300, 120, color.NRGBA{R: 245, G: 245, B: 245, A: 255})
	page := ocr.OCRPage{SchemaVersion: 1, Image: ocr.OCRImageInfo{Width: 300, Height: 120}, Paragraphs: []ocr.OCRParagraph{
		{ID: "one", Text: "One", Box: ocr.OCRBox{X: 10, Y: 10, Width: 120, Height: 40}, Lines: []ocr.OCRLine{{Text: "One"}}},
		{ID: "two", Text: "Two", Box: ocr.OCRBox{X: 160, Y: 10, Width: 120, Height: 40}, Lines: []ocr.OCRLine{{Text: "Two"}}},
	}}
	translation := TranslationDocument{SchemaVersion: 1, SourceFile: "page.png", Blocks: []TranslatedBlock{{ID: "one", SourceText: "One", TranslatedText: "Перевод", Status: "translated"}, {ID: "two", SourceText: "Two", Status: "failed"}}}
	document, err := renderer.Prepare(context.Background(), source, page, translation)
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Blocks) != 1 || len(document.SkippedBlocks) != 1 || document.SkippedBlocks[0].ID != "two" {
		t.Fatalf("document=%+v", document)
	}
	cleaned, _, err := renderer.Clean(context.Background(), source, document)
	if err != nil {
		t.Fatal(err)
	}
	before := append([]byte(nil), cleaned.Pix...)
	if err := renderer.Draw(context.Background(), cleaned, document); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(before, cleaned.Pix) {
		t.Fatal("renderer did not draw text")
	}
	if got := cleaned.NRGBAAt(170, 20); got != source.NRGBAAt(170, 20) {
		t.Fatalf("skipped block changed: got=%+v want=%+v", got, source.NRGBAAt(170, 20))
	}
}

func TestRendererRejectsUnsupportedScriptsBeforeCleanup(t *testing.T) {
	directory := t.TempDir()
	writeTestFont(t, directory)
	renderer, err := NewRenderer(directory, DefaultRenderConfig(), &fakeInpaintEngine{})
	if err != nil {
		t.Fatal(err)
	}
	supported, err := renderer.supportsText("Latin Кириллица 123")
	if err != nil || !supported {
		t.Fatalf("supported=%v err=%v", supported, err)
	}
	supported, err = renderer.supportsText("中文")
	if err != nil || supported {
		t.Fatalf("supported=%v err=%v", supported, err)
	}
}

func TestAtomicPNGAndJPEGEncodingPreservesDimensionsAndExistingFile(t *testing.T) {
	directory := t.TempDir()
	source := solidNRGBA(32, 24, color.NRGBA{R: 120, G: 130, B: 140, A: 200})
	for _, extension := range []string{".png", ".jpg", ".jpeg"} {
		path := filepath.Join(directory, "output"+extension)
		if err := atomicEncodeGoImage(context.Background(), source, path, extension, 92); err != nil {
			t.Fatal(err)
		}
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		configuration, _, err := image.DecodeConfig(file)
		_ = file.Close()
		if err != nil || configuration.Width != 32 || configuration.Height != 24 {
			t.Fatalf("extension=%s config=%+v err=%v", extension, configuration, err)
		}
	}
	existing := filepath.Join(directory, "existing.png")
	if err := os.WriteFile(existing, []byte("good"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := atomicEncodeGoImage(ctx, source, existing, ".png", 92); err == nil {
		t.Fatal("expected cancellation")
	}
	data, _ := os.ReadFile(existing)
	if string(data) != "good" {
		t.Fatalf("existing file changed: %q", data)
	}
}

func TestPNGCleanupKeepsPixelsOutsideBoxByteEquivalentAfterDecode(t *testing.T) {
	source := solidNRGBA(10, 10, color.NRGBA{R: 30, G: 40, B: 50, A: 60})
	renderer, _ := NewRenderer(t.TempDir(), DefaultRenderConfig(), &fakeInpaintEngine{})
	cleaned, _, err := renderer.Clean(context.Background(), source, RenderDocument{Blocks: []RenderBlock{{CleanupBox: ocr.OCRBox{X: 2, Y: 2, Width: 2, Height: 2}, Background: newRenderColor(color.NRGBA{R: 200, A: 255}), CleanupMode: CleanupSolid}}})
	if err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, cleaned); err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(bytes.NewReader(buffer.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if got := color.NRGBAModel.Convert(decoded.At(0, 0)).(color.NRGBA); got != source.NRGBAAt(0, 0) {
		t.Fatalf("outside=%+v", got)
	}
}
