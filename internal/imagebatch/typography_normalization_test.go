package imagebatch

import (
	"testing"

	"github.com/sympllate/translator/internal/ocr"
)

func TestNormalizeLocalTypographyCorrectsIsolatedOutlier(t *testing.T) {
	paragraphs := make([]ocr.OCRParagraph, 4)
	for index := range paragraphs {
		box := ocr.OCRBox{X: 20, Y: 10 + index*20, Width: 120, Height: 12}
		paragraphs[index] = ocr.OCRParagraph{Box: box, Lines: []ocr.OCRLine{{Box: box}}}
	}
	estimates := []FontStyleEstimate{{FontSize: 11}, {FontSize: 12}, {FontSize: 27}, {FontSize: 11}}
	normalizeLocalTypography(paragraphs, estimates, []bool{true, true, true, true}, CoordinateTransform{ScaleX: 1, ScaleY: 1})
	if !estimates[2].Normalized || estimates[2].FontSize > 13 || estimates[2].InitialFontSize != 27 {
		t.Fatalf("estimate=%+v", estimates[2])
	}
}

func TestNormalizeLocalTypographyPreservesHeading(t *testing.T) {
	paragraphs := []ocr.OCRParagraph{
		{Text: "ENGINE OIL", Box: ocr.OCRBox{X: 20, Y: 10, Width: 220, Height: 28}, Lines: []ocr.OCRLine{{Text: "ENGINE OIL", Box: ocr.OCRBox{X: 20, Y: 10, Width: 220, Height: 28}}}},
		{Text: "Normal body line", Box: ocr.OCRBox{X: 20, Y: 50, Width: 180, Height: 12}, Lines: []ocr.OCRLine{{Text: "Normal body line", Box: ocr.OCRBox{X: 20, Y: 50, Width: 180, Height: 12}}}},
		{Text: "Another body line", Box: ocr.OCRBox{X: 20, Y: 70, Width: 180, Height: 12}, Lines: []ocr.OCRLine{{Text: "Another body line", Box: ocr.OCRBox{X: 20, Y: 70, Width: 180, Height: 12}}}},
	}
	estimates := []FontStyleEstimate{{FontSize: 27}, {FontSize: 11}, {FontSize: 12}}
	normalizeLocalTypography(paragraphs, estimates, []bool{true, true, true}, CoordinateTransform{ScaleX: 1, ScaleY: 1})
	if estimates[0].Normalized || estimates[0].FontSize != 27 {
		t.Fatalf("heading=%+v", estimates[0])
	}
}

func TestNormalizeLocalTypographyDoesNotGiveUIControlDocumentFont(t *testing.T) {
	paragraphs := []ocr.OCRParagraph{
		{Text: "Start", Box: ocr.OCRBox{X: 20, Y: 20, Width: 60, Height: 12}, Lines: []ocr.OCRLine{{Text: "Start", Box: ocr.OCRBox{X: 20, Y: 20, Width: 60, Height: 12}}}},
		{Text: "Document body one", Box: ocr.OCRBox{X: 20, Y: 40, Width: 180, Height: 12}, Lines: []ocr.OCRLine{{Text: "Document body one", Box: ocr.OCRBox{X: 20, Y: 40, Width: 180, Height: 12}}}},
		{Text: "Document body two", Box: ocr.OCRBox{X: 20, Y: 60, Width: 180, Height: 12}, Lines: []ocr.OCRLine{{Text: "Document body two", Box: ocr.OCRBox{X: 20, Y: 60, Width: 180, Height: 12}}}},
	}
	estimates := []FontStyleEstimate{{FontSize: 8}, {FontSize: 24}, {FontSize: 23}}
	parents := []StructuralParent{
		{ID: "control", Type: "control_row"},
		{ID: "document", Type: "document_column", SourceColumn: 1},
		{ID: "document", Type: "document_column", SourceColumn: 1},
	}
	normalizeLocalTypographyWithin(paragraphs, estimates, []bool{true, true, true}, CoordinateTransform{ScaleX: 1, ScaleY: 1}, parents)
	if estimates[0].FontSize != 8 || estimates[0].Normalized {
		t.Fatalf("UI control inherited document typography: %+v", estimates[0])
	}
}
