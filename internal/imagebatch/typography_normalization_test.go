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
		{Box: ocr.OCRBox{X: 20, Y: 10, Width: 220, Height: 28}, Lines: []ocr.OCRLine{{Box: ocr.OCRBox{X: 20, Y: 10, Width: 220, Height: 28}}}},
		{Box: ocr.OCRBox{X: 20, Y: 50, Width: 180, Height: 12}, Lines: []ocr.OCRLine{{Box: ocr.OCRBox{X: 20, Y: 50, Width: 180, Height: 12}}}},
		{Box: ocr.OCRBox{X: 20, Y: 70, Width: 180, Height: 12}, Lines: []ocr.OCRLine{{Box: ocr.OCRBox{X: 20, Y: 70, Width: 180, Height: 12}}}},
	}
	estimates := []FontStyleEstimate{{FontSize: 27}, {FontSize: 11}, {FontSize: 12}}
	normalizeLocalTypography(paragraphs, estimates, []bool{true, true, true}, CoordinateTransform{ScaleX: 1, ScaleY: 1})
	if estimates[0].Normalized || estimates[0].FontSize != 27 {
		t.Fatalf("heading=%+v", estimates[0])
	}
}
