package imagebatch

import (
	"context"
	"errors"
	"image"
	"image/color"
	"math"
	"sort"

	"github.com/sympllate/translator/internal/ocr"
)

func Luminance(value color.NRGBA) float64 {
	linear := func(channel uint8) float64 {
		v := float64(channel) / 255
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(value.R) + 0.7152*linear(value.G) + 0.0722*linear(value.B)
}

func IsNearlyWhite(value color.NRGBA) bool { return value.A > 0 && Luminance(value) >= 0.88 }
func IsNearlyBlack(value color.NRGBA) bool { return value.A > 0 && Luminance(value) <= 0.04 }

func ContrastRatio(foreground, background color.NRGBA) float64 {
	a, b := Luminance(foreground), Luminance(background)
	if a < b {
		a, b = b, a
	}
	return (a + 0.05) / (b + 0.05)
}

func SampleBackground(ctx context.Context, source *image.NRGBA, box ocrBox, ringWidth, minimumSamples, maximumSamples int) (BackgroundSample, error) {
	if source == nil || ringWidth <= 0 || maximumSamples <= 0 {
		return BackgroundSample{}, errors.New("invalid background sample request")
	}
	box = boxFromOCR(ClampBox(box.toOCR(), source.Bounds().Dx(), source.Bounds().Dy()))
	if box.Width <= 0 || box.Height <= 0 {
		return BackgroundSample{}, errors.New("cleanup box is outside the image")
	}
	outer := boxFromOCR(ExpandBox(box.toOCR(), CleanupPadding{Horizontal: ringWidth, Vertical: ringWidth}, source.Bounds().Dx(), source.Bounds().Dy()))
	pixels, err := collectRingPixels(ctx, source, box, outer, maximumSamples)
	fallback := false
	if err != nil {
		return BackgroundSample{}, err
	}
	if len(pixels) < minimumSamples {
		pixels, err = collectGlobalPixels(ctx, source, maximumSamples)
		fallback = true
	}
	if err != nil {
		return BackgroundSample{}, err
	}
	if len(pixels) == 0 {
		return BackgroundSample{Color: color.NRGBA{R: 245, G: 245, B: 245, A: 255}, Fallback: true}, nil
	}
	median := medianColor(pixels)
	variance := colorVariance(pixels, median)
	light := 0
	for _, pixel := range pixels {
		if Luminance(pixel) >= 0.82 {
			light++
		}
	}
	if IsNearlyWhite(median) && float64(light)/float64(len(pixels)) >= 0.8 && variance < 225 {
		median = color.NRGBA{R: max(median.R, 240), G: max(median.G, 240), B: max(median.B, 240), A: median.A}
	}
	return BackgroundSample{Color: median, Variance: variance, Count: len(pixels), Fallback: fallback}, nil
}

// ocrBox keeps the pixel loops independent of JSON-facing OCR structures.
type ocrBox struct{ X, Y, Width, Height int }

func boxFromOCR(value ocr.OCRBox) ocrBox { return ocrBox{value.X, value.Y, value.Width, value.Height} }
func (b ocrBox) toOCR() ocr.OCRBox {
	return ocr.OCRBox{X: b.X, Y: b.Y, Width: b.Width, Height: b.Height}
}

func collectRingPixels(ctx context.Context, source *image.NRGBA, inner, outer ocrBox, maximum int) ([]color.NRGBA, error) {
	values := make([]color.NRGBA, 0, min(maximum, max(0, outer.Width*outer.Height-inner.Width*inner.Height)))
	seen := 0
	for y := outer.Y; y < outer.Y+outer.Height; y++ {
		if y&31 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		for x := outer.X; x < outer.X+outer.Width; x++ {
			if x >= inner.X && x < inner.X+inner.Width && y >= inner.Y && y < inner.Y+inner.Height {
				continue
			}
			pixel := source.NRGBAAt(x+source.Bounds().Min.X, y+source.Bounds().Min.Y)
			if pixel.A == 0 {
				continue
			}
			seen++
			appendReservoir(&values, pixel, seen, maximum)
		}
	}
	return values, nil
}

func collectGlobalPixels(ctx context.Context, source *image.NRGBA, maximum int) ([]color.NRGBA, error) {
	values := make([]color.NRGBA, 0, maximum)
	seen := 0
	bounds := source.Bounds()
	step := max(1, int(math.Sqrt(float64(bounds.Dx()*bounds.Dy()/max(1, maximum)))))
	for y := bounds.Min.Y; y < bounds.Max.Y; y += step {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for x := bounds.Min.X; x < bounds.Max.X; x += step {
			pixel := source.NRGBAAt(x, y)
			if pixel.A == 0 {
				continue
			}
			seen++
			appendReservoir(&values, pixel, seen, maximum)
		}
	}
	return values, nil
}

func appendReservoir(values *[]color.NRGBA, value color.NRGBA, seen, maximum int) {
	if len(*values) < maximum {
		*values = append(*values, value)
		return
	}
	// Deterministic evenly-spaced replacement avoids random render differences.
	index := int((uint64(seen)*11400714819323198485)>>32) % seen
	if index < maximum {
		(*values)[index] = value
	}
}

func medianColor(values []color.NRGBA) color.NRGBA {
	r, g, b, a := make([]int, len(values)), make([]int, len(values)), make([]int, len(values)), make([]int, len(values))
	for index, value := range values {
		r[index], g[index], b[index], a[index] = int(value.R), int(value.G), int(value.B), int(value.A)
	}
	for _, channel := range [][]int{r, g, b, a} {
		sort.Ints(channel)
	}
	middle := len(values) / 2
	return color.NRGBA{R: uint8(r[middle]), G: uint8(g[middle]), B: uint8(b[middle]), A: uint8(a[middle])}
}

func colorVariance(values []color.NRGBA, median color.NRGBA) float64 {
	var total float64
	for _, value := range values {
		dr, dg, db := float64(int(value.R)-int(median.R)), float64(int(value.G)-int(median.G)), float64(int(value.B)-int(median.B))
		total += (dr*dr + dg*dg + db*db) / 3
	}
	return total / float64(len(values))
}

func SampleForeground(ctx context.Context, source *image.NRGBA, box ocr.OCRBox, background color.NRGBA) (color.NRGBA, error) {
	box = ClampBox(box, source.Bounds().Dx(), source.Bounds().Dy())
	values := make([]color.NRGBA, 0, min(4096, box.Width*box.Height))
	seen := 0
	for y := box.Y; y < box.Y+box.Height; y++ {
		if y&31 == 0 {
			if err := ctx.Err(); err != nil {
				return color.NRGBA{}, err
			}
		}
		for x := box.X; x < box.X+box.Width; x++ {
			pixel := source.NRGBAAt(x+source.Bounds().Min.X, y+source.Bounds().Min.Y)
			if pixel.A > 0 && math.Abs(Luminance(pixel)-Luminance(background)) >= 0.2 {
				seen++
				appendReservoir(&values, pixel, seen, 4096)
			}
		}
	}
	fallback := color.NRGBA{A: 255}
	if Luminance(background) < 0.35 {
		fallback = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	}
	if len(values) == 0 {
		return fallback, nil
	}
	result := medianColor(values)
	if ContrastRatio(result, background) < 4.5 {
		return fallback, nil
	}
	return result, nil
}
