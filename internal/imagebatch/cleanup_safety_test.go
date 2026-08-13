package imagebatch

import (
	"context"
	"image"
	"image/color"
	"testing"

	"github.com/sympllate/translator/internal/inpaint"
	"github.com/sympllate/translator/internal/ocr"
)

type wholeCropInpaintEngine struct {
	calls int
	fill  color.NRGBA
}

func (e *wholeCropInpaintEngine) Inpaint(_ context.Context, source *image.NRGBA, _ *image.Gray) (inpaint.Result, error) {
	e.calls++
	result := solidNRGBA(source.Bounds().Dx(), source.Bounds().Dy(), e.fill)
	return inpaint.Result{Image: result}, nil
}

func (e *wholeCropInpaintEngine) Close() error { return nil }

func TestSafeCleanupPlainTextAndUniformBackground(t *testing.T) {
	source := solidNRGBA(120, 60, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	box := ocr.OCRBox{X: 20, Y: 15, Width: 60, Height: 25}
	drawSyntheticWord(source, box)
	original := cloneNRGBA(source)
	cleaned, _, stats := runSafetyCleanup(t, source, safetyBlock("plain", box, CleanupSolid))
	if got := cleaned.NRGBAAt(23, 19); got != (color.NRGBA{R: 255, G: 255, B: 255, A: 255}) {
		t.Fatalf("glyph was not restored: %+v", got)
	}
	assertPixelsOutsideMaskEqual(t, original, cleaned, stats.Diagnostics.FinalCleanupMask)
}

func TestSafeCleanupPreservesTableBordersAndNearbyRules(t *testing.T) {
	tests := []struct {
		name  string
		draw  func(*image.NRGBA)
		box   ocr.OCRBox
		check []image.Point
	}{
		{
			name:  "text inside table cell",
			box:   ocr.OCRBox{X: 30, Y: 20, Width: 55, Height: 20},
			draw:  func(value *image.NRGBA) { drawRectangle(value, image.Rect(10, 10, 105, 50)) },
			check: []image.Point{{10, 10}, {104, 10}, {10, 49}, {104, 49}},
		},
		{
			name: "text touching horizontal line",
			box:  ocr.OCRBox{X: 25, Y: 15, Width: 60, Height: 25},
			draw: func(value *image.NRGBA) {
				for x := 5; x < 115; x++ {
					value.SetNRGBA(x, 27, color.NRGBA{A: 255})
				}
			},
			check: []image.Point{{5, 27}, {40, 27}, {114, 27}},
		},
		{
			name: "text near vertical border",
			box:  ocr.OCRBox{X: 24, Y: 15, Width: 60, Height: 25},
			draw: func(value *image.NRGBA) {
				for y := 3; y < 57; y++ {
					value.SetNRGBA(24, y, color.NRGBA{A: 255})
				}
			},
			check: []image.Point{{24, 3}, {24, 25}, {24, 56}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := solidNRGBA(120, 60, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
			test.draw(source)
			drawSyntheticWord(source, test.box)
			original := cloneNRGBA(source)
			cleaned, _, stats := runSafetyCleanup(t, source, safetyBlock(test.name, test.box, CleanupSolid))
			for _, point := range test.check {
				if got, want := cleaned.NRGBAAt(point.X, point.Y), original.NRGBAAt(point.X, point.Y); got != want {
					t.Fatalf("protected pixel %v changed: got=%+v want=%+v", point, got, want)
				}
			}
			assertPixelsOutsideMaskEqual(t, original, cleaned, stats.Diagnostics.FinalCleanupMask)
		})
	}
}

func TestSafeCleanupPreservesTechnicalDiagramInsideLargeParagraph(t *testing.T) {
	source := solidNRGBA(140, 80, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	drawRectangle(source, image.Rect(8, 8, 72, 62))
	for x := 15; x < 65; x++ {
		y := 15 + (x-15)/2
		source.SetNRGBA(x, y, color.NRGBA{A: 255})
	}
	word := ocr.OCRBox{X: 88, Y: 25, Width: 35, Height: 18}
	drawSyntheticWord(source, word)
	original := cloneNRGBA(source)
	block := safetyBlock("diagram", ocr.OCRBox{X: 5, Y: 5, Width: 125, Height: 62}, CleanupSolid)
	block.CleanupRegions = []CleanupRegion{{Level: "paragraph", Box: block.SourceBox, TextHeight: 18}}
	cleaned, _, stats := runSafetyCleanup(t, source, block)
	for _, point := range []image.Point{{8, 8}, {71, 8}, {8, 61}, {71, 61}, {35, 25}} {
		if got, want := cleaned.NRGBAAt(point.X, point.Y), original.NRGBAAt(point.X, point.Y); got != want {
			t.Fatalf("diagram pixel %v changed: got=%+v want=%+v", point, got, want)
		}
	}
	if stats.FinalCleanupPixels >= block.SourceBox.Width*block.SourceBox.Height/3 {
		t.Fatalf("large paragraph was cleaned too aggressively: stats=%+v", stats)
	}
	assertPixelsOutsideMaskEqual(t, original, cleaned, stats.Diagnostics.FinalCleanupMask)
}

func TestLargeAndLinearComponentsAreProtected(t *testing.T) {
	tests := []struct {
		name   string
		draw   func(*image.NRGBA)
		reason string
	}{
		{
			name: "large connected component",
			draw: func(value *image.NRGBA) {
				for y := 18; y < 38; y++ {
					for x := 15; x < 35; x++ {
						value.SetNRGBA(x, y, color.NRGBA{A: 255})
					}
				}
			},
			reason: "component_too_large",
		},
		{
			name: "long horizontal component",
			draw: func(value *image.NRGBA) {
				for x := 8; x < 92; x++ {
					value.SetNRGBA(x, 28, color.NRGBA{A: 255})
				}
			},
			reason: "horizontal_graphics",
		},
		{
			name: "long vertical component",
			draw: func(value *image.NRGBA) {
				for y := 4; y < 56; y++ {
					value.SetNRGBA(20, y, color.NRGBA{A: 255})
				}
			},
			reason: "vertical_graphics",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := solidNRGBA(100, 60, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
			test.draw(source)
			word := ocr.OCRBox{X: 60, Y: 20, Width: 25, Height: 16}
			drawSyntheticWord(source, word)
			block := safetyBlock(test.name, ocr.OCRBox{X: 5, Y: 5, Width: 90, Height: 50}, CleanupSolid)
			block.CleanupRegions = []CleanupRegion{{Level: "paragraph", Box: block.SourceBox, TextHeight: 12}}
			_, _, stats := runSafetyCleanup(t, source, block)
			if !hasRejectionReason(stats, test.reason) {
				t.Fatalf("missing rejection %q: %+v", test.reason, stats.Blocks)
			}
		})
	}
}

func TestTinyTextDilationDoesNotCrossProtectedLine(t *testing.T) {
	source := solidNRGBA(40, 25, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	for y := 3; y < 22; y++ {
		source.SetNRGBA(14, y, color.NRGBA{A: 255})
	}
	for y := 10; y <= 12; y++ {
		source.SetNRGBA(12, y, color.NRGBA{A: 255})
	}
	original := cloneNRGBA(source)
	box := ocr.OCRBox{X: 11, Y: 9, Width: 5, Height: 5}
	cleaned, _, stats := runSafetyCleanup(t, source, safetyBlock("tiny", box, CleanupSolid))
	for y := 3; y < 22; y++ {
		if cleaned.NRGBAAt(14, y) != original.NRGBAAt(14, y) {
			t.Fatalf("vertical line changed at y=%d", y)
		}
	}
	assertPixelsOutsideMaskEqual(t, original, cleaned, stats.Diagnostics.FinalCleanupMask)
}

func TestNeuralCleanupCompositesOnlyFinalMask(t *testing.T) {
	source := gradientFixture(120, 60)
	box := ocr.OCRBox{X: 25, Y: 15, Width: 55, Height: 25}
	drawSyntheticWord(source, box)
	original := cloneNRGBA(source)
	engine := &wholeCropInpaintEngine{fill: color.NRGBA{R: 220, G: 30, B: 160, A: 255}}
	renderer, err := NewRenderer(t.TempDir(), DefaultRenderConfig(), engine)
	if err != nil {
		t.Fatal(err)
	}
	block := safetyBlock("neural", box, CleanupNeural)
	block.Background = newRenderColor(color.NRGBA{R: 125, G: 125, B: 125, A: 255})
	cleaned, document, stats, err := renderer.Clean(context.Background(), source, RenderDocument{Blocks: []RenderBlock{block}})
	if err != nil {
		t.Fatal(err)
	}
	if engine.calls != 1 {
		t.Fatalf("inpaint calls=%d document=%+v stats=%+v", engine.calls, document, stats)
	}
	assertPixelsOutsideMaskEqual(t, original, cleaned, stats.Diagnostics.FinalCleanupMask)
}

func TestOverlappingCleanupRegionsAreAppliedOnce(t *testing.T) {
	source := solidNRGBA(90, 45, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	box := ocr.OCRBox{X: 15, Y: 10, Width: 55, Height: 22}
	drawSyntheticWord(source, box)
	original := cloneNRGBA(source)
	one := safetyBlock("one", box, CleanupSolid)
	two := safetyBlock("two", box, CleanupSolid)
	renderer, _ := NewRenderer(t.TempDir(), DefaultRenderConfig(), &fakeInpaintEngine{})
	cleaned, filtered, stats, err := renderer.Clean(context.Background(), source, RenderDocument{Blocks: []RenderBlock{one, two}})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Blocks) != 2 || len(filtered.SkippedBlocks) != 0 || filtered.CleanupDiagnostics.FailedRenderedBlocks != 1 {
		t.Fatalf("overlap was not normalized: %+v", filtered)
	}
	assertPixelsOutsideMaskEqual(t, original, cleaned, stats.Diagnostics.FinalCleanupMask)
}

func safetyBlock(id string, box ocr.OCRBox, mode CleanupMode) RenderBlock {
	return RenderBlock{
		ID: id, SourceBox: box, CleanupBox: box,
		CleanupRegions: []CleanupRegion{{Level: "word", Box: box, TextHeight: box.Height}},
		Background:     newRenderColor(color.NRGBA{R: 255, G: 255, B: 255, A: 255}),
		Foreground:     newRenderColor(color.NRGBA{A: 255}), CleanupMode: mode,
	}
}

func runSafetyCleanup(t *testing.T, source *image.NRGBA, block RenderBlock) (*image.NRGBA, RenderDocument, CleanupStats) {
	t.Helper()
	renderer, err := NewRenderer(t.TempDir(), DefaultRenderConfig(), &fakeInpaintEngine{})
	if err != nil {
		t.Fatal(err)
	}
	cleaned, document, stats, err := renderer.Clean(context.Background(), source, RenderDocument{Blocks: []RenderBlock{block}})
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Blocks) != 1 {
		t.Fatalf("cleanup rejected test block: %+v", document)
	}
	return cleaned, document, stats
}

func assertPixelsOutsideMaskEqual(t *testing.T, original, cleaned *image.NRGBA, mask *image.Gray) {
	t.Helper()
	for y := original.Bounds().Min.Y; y < original.Bounds().Max.Y; y++ {
		for x := original.Bounds().Min.X; x < original.Bounds().Max.X; x++ {
			if mask != nil && mask.GrayAt(x, y).Y != 0 {
				continue
			}
			if original.NRGBAAt(x, y) != cleaned.NRGBAAt(x, y) {
				t.Fatalf("pixel outside FinalCleanupMask changed at (%d,%d)", x, y)
			}
		}
	}
}

func drawRectangle(target *image.NRGBA, rectangle image.Rectangle) {
	for x := rectangle.Min.X; x < rectangle.Max.X; x++ {
		target.SetNRGBA(x, rectangle.Min.Y, color.NRGBA{A: 255})
		target.SetNRGBA(x, rectangle.Max.Y-1, color.NRGBA{A: 255})
	}
	for y := rectangle.Min.Y; y < rectangle.Max.Y; y++ {
		target.SetNRGBA(rectangle.Min.X, y, color.NRGBA{A: 255})
		target.SetNRGBA(rectangle.Max.X-1, y, color.NRGBA{A: 255})
	}
}

func hasRejectionReason(stats CleanupStats, reason string) bool {
	for _, block := range stats.Blocks {
		for _, rejection := range block.Rejections {
			if rejection.Reason == reason {
				return true
			}
		}
	}
	return false
}
