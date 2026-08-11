package imagebatch

import (
	"context"
	"image"
	"image/color"
	"testing"

	"github.com/sympllate/translator/internal/ocr"
)

func TestBackgroundSamplingSolidColorsAndEdges(t *testing.T) {
	colors := []color.NRGBA{{R: 255, G: 255, B: 255, A: 255}, {R: 242, G: 242, B: 242, A: 255}, {R: 160, G: 160, B: 160, A: 255}, {R: 20, G: 120, B: 190, A: 255}, {R: 15, G: 15, B: 15, A: 255}}
	boxes := []ocr.OCRBox{{X: 8, Y: 8, Width: 8, Height: 8}, {X: 0, Y: 8, Width: 8, Height: 8}, {X: 16, Y: 8, Width: 8, Height: 8}, {X: 8, Y: 0, Width: 8, Height: 8}, {X: 0, Y: 0, Width: 8, Height: 8}}
	for _, background := range colors {
		for _, box := range boxes {
			source := solidNRGBA(24, 24, background)
			sample, err := SampleBackground(context.Background(), source, boxFromOCR(box), 3, 4, 1024)
			if err != nil {
				t.Fatal(err)
			}
			if sample.Color != background || sample.Variance != 0 || sample.Count == 0 {
				t.Fatalf("background=%+v box=%+v sample=%+v", background, box, sample)
			}
		}
	}
}

func TestBackgroundSamplingMedianVarianceTransparencyAndFallback(t *testing.T) {
	source := solidNRGBA(10, 10, color.NRGBA{})
	for x := 0; x < 10; x++ {
		source.SetNRGBA(x, 0, color.NRGBA{R: uint8(x * 10), G: 100, B: 200, A: 255})
	}
	sample, err := SampleBackground(context.Background(), source, boxFromOCR(ocr.OCRBox{X: 2, Y: 2, Width: 6, Height: 6}), 2, 100, 64)
	if err != nil {
		t.Fatal(err)
	}
	if !sample.Fallback || sample.Count == 0 || sample.Color.A != 255 || sample.Variance == 0 {
		t.Fatalf("sample=%+v", sample)
	}

	varied := solidNRGBA(20, 20, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	for x := 0; x < 20; x += 2 {
		varied.SetNRGBA(x, 4, color.NRGBA{A: 255})
	}
	high, err := SampleBackground(context.Background(), varied, boxFromOCR(ocr.OCRBox{X: 5, Y: 5, Width: 10, Height: 10}), 2, 4, 1024)
	if err != nil || high.Variance == 0 {
		t.Fatalf("sample=%+v err=%v", high, err)
	}
}

func TestBackgroundAndCleanupCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	source := solidNRGBA(100, 100, color.NRGBA{R: 200, G: 200, B: 200, A: 255})
	if _, err := SampleBackground(ctx, source, boxFromOCR(ocr.OCRBox{X: 10, Y: 10, Width: 50, Height: 50}), 4, 4, 1024); err == nil {
		t.Fatal("expected cancellation")
	}
	renderer, _ := NewRenderer(t.TempDir(), DefaultRenderConfig(), &fakeInpaintEngine{})
	if _, _, err := renderer.Clean(ctx, source, RenderDocument{}); err == nil {
		t.Fatal("expected cancellation")
	}
}

func TestForegroundContrastFallbacks(t *testing.T) {
	light := solidNRGBA(10, 10, color.NRGBA{R: 245, G: 245, B: 245, A: 255})
	foreground, err := SampleForeground(context.Background(), light, ocr.OCRBox{Width: 10, Height: 10}, color.NRGBA{R: 245, G: 245, B: 245, A: 255})
	if err != nil || Luminance(foreground) > 0.04 {
		t.Fatalf("foreground=%+v err=%v", foreground, err)
	}
	dark := solidNRGBA(10, 10, color.NRGBA{R: 10, G: 10, B: 10, A: 255})
	foreground, err = SampleForeground(context.Background(), dark, ocr.OCRBox{Width: 10, Height: 10}, color.NRGBA{R: 10, G: 10, B: 10, A: 255})
	if err != nil || !IsNearlyWhite(foreground) || ContrastRatio(foreground, color.NRGBA{R: 10, G: 10, B: 10, A: 255}) < 4.5 {
		t.Fatalf("foreground=%+v err=%v", foreground, err)
	}
}

func solidNRGBA(width, height int, value color.NRGBA) *image.NRGBA {
	result := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			result.SetNRGBA(x, y, value)
		}
	}
	return result
}
