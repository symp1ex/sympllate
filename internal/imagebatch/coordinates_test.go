package imagebatch

import (
	"testing"

	"github.com/sympllate/translator/internal/ocr"
)

func TestCoordinateTransformScalingAndRounding(t *testing.T) {
	tests := []struct {
		name                                           string
		sourceWidth, sourceHeight, ocrWidth, ocrHeight int
		box, want                                      ocr.OCRBox
	}{
		{"identity", 100, 80, 100, 80, ocr.OCRBox{X: 10, Y: 8, Width: 20, Height: 16}, ocr.OCRBox{X: 10, Y: 8, Width: 20, Height: 16}},
		{"upscaled OCR", 100, 80, 200, 160, ocr.OCRBox{X: 20, Y: 16, Width: 40, Height: 32}, ocr.OCRBox{X: 10, Y: 8, Width: 20, Height: 16}},
		{"downscaled OCR", 200, 160, 100, 80, ocr.OCRBox{X: 10, Y: 8, Width: 20, Height: 16}, ocr.OCRBox{X: 20, Y: 16, Width: 40, Height: 32}},
		{"independent axes", 300, 100, 100, 200, ocr.OCRBox{X: 5, Y: 8, Width: 7, Height: 9}, ocr.OCRBox{X: 15, Y: 4, Width: 21, Height: 5}},
		{"rounding", 101, 101, 200, 200, ocr.OCRBox{X: 3, Y: 5, Width: 7, Height: 9}, ocr.OCRBox{X: 2, Y: 3, Width: 4, Height: 5}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transform, err := NewCoordinateTransform(test.sourceWidth, test.sourceHeight, test.ocrWidth, test.ocrHeight)
			if err != nil {
				t.Fatal(err)
			}
			if got := TransformBox(test.box, transform); got != test.want {
				t.Fatalf("got=%+v want=%+v", got, test.want)
			}
		})
	}
}

func TestClampBoxBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		box, want ocr.OCRBox
	}{
		{"negative", ocr.OCRBox{X: -3, Y: -2, Width: 8, Height: 7}, ocr.OCRBox{Width: 5, Height: 5}},
		{"right", ocr.OCRBox{X: 8, Y: 1, Width: 8, Height: 3}, ocr.OCRBox{X: 8, Y: 1, Width: 2, Height: 3}},
		{"bottom", ocr.OCRBox{X: 1, Y: 8, Width: 3, Height: 8}, ocr.OCRBox{X: 1, Y: 8, Width: 3, Height: 2}},
		{"outside", ocr.OCRBox{X: 12, Y: 1, Width: 2, Height: 2}, ocr.OCRBox{}},
		{"zero", ocr.OCRBox{X: 1, Y: 1}, ocr.OCRBox{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ClampBox(test.box, 10, 10); got != test.want {
				t.Fatalf("got=%+v want=%+v", got, test.want)
			}
		})
	}
}

func TestCoordinateTransformRejectsDimensionMismatch(t *testing.T) {
	for _, dimensions := range [][4]int{{0, 10, 10, 10}, {10, 0, 10, 10}, {10, 10, 0, 10}, {10, 10, 10, 0}} {
		if _, err := NewCoordinateTransform(dimensions[0], dimensions[1], dimensions[2], dimensions[3]); err == nil {
			t.Fatalf("dimensions=%v", dimensions)
		}
	}
}

func TestBoxIntersection(t *testing.T) {
	a := ocr.OCRBox{X: 1, Y: 1, Width: 5, Height: 5}
	if got := IntersectionArea(a, ocr.OCRBox{X: 4, Y: 4, Width: 4, Height: 4}); got != 4 {
		t.Fatalf("area=%d", got)
	}
	if BoxesIntersect(a, ocr.OCRBox{X: 6, Y: 1, Width: 2, Height: 2}) {
		t.Fatal("touching boxes must not intersect")
	}
}
