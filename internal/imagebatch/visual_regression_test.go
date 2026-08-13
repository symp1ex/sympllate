package imagebatch

import (
	"context"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"github.com/sympllate/translator/internal/ocr"
)

func TestWriteGeometryVisualRegressionArtifacts(t *testing.T) {
	artifacts := os.Getenv("SYMPLLATE_WRITE_GEOMETRY_FIXTURES")
	if artifacts == "" {
		t.Skip("set SYMPLLATE_WRITE_GEOMETRY_FIXTURES to write review artifacts")
	}
	paragraphs := []ocr.OCRParagraph{
		geometryParagraph("paddle-group", 95, []ocr.OCRBox{{X: 18, Y: 16, Width: 230, Height: 20}, {X: 18, Y: 86, Width: 230, Height: 20}}),
		geometryParagraph("ui-label", 94, []ocr.OCRBox{{X: 100, Y: 51, Width: 85, Height: 20}}),
	}
	directory := t.TempDir()
	writeTestFont(t, directory)
	renderer, err := NewRenderer(directory, DefaultRenderConfig(), &fakeInpaintEngine{})
	if err != nil {
		t.Fatal(err)
	}
	defer renderer.Close()
	source := solidNRGBA(320, 130, color.NRGBA{R: 248, G: 248, B: 248, A: 255})
	for _, paragraph := range paragraphs {
		for _, line := range paragraph.Lines {
			drawSyntheticWord(source, line.Box)
		}
	}
	document, err := renderer.Prepare(context.Background(), source, ocr.OCRPage{Image: ocr.OCRImageInfo{Width: 320, Height: 130}, Paragraphs: paragraphs, Diagnostics: ocr.OCRDiagnostics{Backend: "paddleocr"}}, TranslationDocument{Blocks: []TranslatedBlock{
		{ID: "paddle-group", TranslatedText: "Translated group", Status: "translated"},
		{ID: "ui-label", TranslatedText: "Control", Status: "translated"},
	}})
	if err != nil || len(document.Blocks) != 2 {
		t.Fatalf("document=%+v err=%v", document, err)
	}
	cleaned, document, _, err := renderer.Clean(context.Background(), source, document)
	if err != nil {
		t.Fatal(err)
	}
	if err := renderer.Draw(context.Background(), cleaned, document); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(artifacts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeRenderCandidatesDebug(context.Background(), source, document, filepath.Join(artifacts, "geometry-candidates.png"), 92); err != nil {
		t.Fatal(err)
	}
	if err := atomicEncodeGoImage(context.Background(), cleaned, filepath.Join(artifacts, "geometry-final.png"), ".png", 92); err != nil {
		t.Fatal(err)
	}
}
