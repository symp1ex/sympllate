package imagebatch

import (
	"errors"
	"math"

	"github.com/sympllate/translator/internal/ocr"
)

func NewCoordinateTransform(sourceWidth, sourceHeight, ocrWidth, ocrHeight int) (CoordinateTransform, error) {
	if sourceWidth <= 0 || sourceHeight <= 0 || ocrWidth <= 0 || ocrHeight <= 0 {
		return CoordinateTransform{}, errors.New("Координаты OCR не соответствуют размеру изображения")
	}
	return CoordinateTransform{
		SourceWidth: sourceWidth, SourceHeight: sourceHeight, OCRWidth: ocrWidth, OCRHeight: ocrHeight,
		ScaleX: float64(sourceWidth) / float64(ocrWidth), ScaleY: float64(sourceHeight) / float64(ocrHeight),
	}, nil
}

func TransformBox(box ocr.OCRBox, transform CoordinateTransform) ocr.OCRBox {
	return ocr.OCRBox{
		X:      int(math.Round(float64(box.X) * transform.ScaleX)),
		Y:      int(math.Round(float64(box.Y) * transform.ScaleY)),
		Width:  int(math.Round(float64(box.Width) * transform.ScaleX)),
		Height: int(math.Round(float64(box.Height) * transform.ScaleY)),
	}
}

func ClampBox(box ocr.OCRBox, imageWidth, imageHeight int) ocr.OCRBox {
	if box.Width <= 0 || box.Height <= 0 || imageWidth <= 0 || imageHeight <= 0 {
		return ocr.OCRBox{}
	}
	left := max(0, box.X)
	top := max(0, box.Y)
	right := min(imageWidth, box.X+box.Width)
	bottom := min(imageHeight, box.Y+box.Height)
	if right <= left || bottom <= top {
		return ocr.OCRBox{}
	}
	return ocr.OCRBox{X: left, Y: top, Width: right - left, Height: bottom - top}
}

func ExpandBox(box ocr.OCRBox, padding CleanupPadding, imageWidth, imageHeight int) ocr.OCRBox {
	return ClampBox(ocr.OCRBox{
		X: box.X - padding.Horizontal, Y: box.Y - padding.Vertical,
		Width: box.Width + padding.Horizontal*2, Height: box.Height + padding.Vertical*2,
	}, imageWidth, imageHeight)
}

func BoxesIntersect(a, b ocr.OCRBox) bool { return IntersectionArea(a, b) > 0 }

func IntersectionArea(a, b ocr.OCRBox) int {
	left, top := max(a.X, b.X), max(a.Y, b.Y)
	right, bottom := min(a.X+a.Width, b.X+b.Width), min(a.Y+a.Height, b.Y+b.Height)
	if right <= left || bottom <= top {
		return 0
	}
	return (right - left) * (bottom - top)
}
