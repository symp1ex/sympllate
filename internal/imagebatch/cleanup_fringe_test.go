package imagebatch

import (
	"context"
	"image"
	"image/color"
	"testing"

	"github.com/sympllate/translator/internal/ocr"
)

func TestAntiAliasedGlyphFringeRecall(t *testing.T) {
	background := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	foreground := color.NRGBA{A: 255}
	source := solidNRGBA(64, 44, background)
	groundTruth := drawAntialiasedH(source, image.Rect(22, 12, 34, 28), foreground, background, 2)
	box := ocrBoxFromRectangle(nonZeroBounds(groundTruth))

	built := buildFixtureMask(t, source, box, "word", foreground, background)
	assertMaskRecallAtLeast(t, built.mask, groundTruth, 0.97)
	if countMask(built.strictTextCoreMask) == 0 || countMask(built.fringeRecoveryMask) == 0 {
		t.Fatal("strict core and recovered fringe diagnostics must both contain pixels")
	}
	if countMask(built.mask) <= countMask(built.strictTextCoreMask) {
		t.Fatal("final mask did not expand beyond the strict text core")
	}
	if built.candidateMask.GrayAt(20, 12).Y != 0 {
		t.Fatal("light anti-alias fringe unexpectedly passed the strict candidate classifier")
	}
	if built.mask.GrayAt(17, 12).Y != 0 {
		t.Fatal("background beyond the local glyph halo was selected")
	}
	assertFalsePositivesOutsideHaloAtMost(t, built.mask, groundTruth, 2, 2)
}

func TestAdaptiveSearchFindsGlyphOutsideTightOCRBox(t *testing.T) {
	background := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	foreground := color.NRGBA{A: 255}
	source := solidNRGBA(64, 44, background)
	groundTruth := drawAntialiasedH(source, image.Rect(22, 12, 34, 28), foreground, background, 2)
	tight := nonZeroBounds(groundTruth)
	box := ocrBoxFromRectangle(image.Rect(tight.Min.X, tight.Min.Y, tight.Max.X-2, tight.Max.Y))

	built := buildFixtureMask(t, source, box, "word", foreground, background)
	assertMaskRecallAtLeast(t, built.mask, groundTruth, 0.97)
	for y := tight.Min.Y; y < tight.Max.Y; y++ {
		if groundTruth.GrayAt(tight.Max.X-1, y).Y != 0 && built.mask.GrayAt(tight.Max.X-1, y).Y == 0 {
			t.Fatalf("glyph pixel outside the OCR bbox was missed at (%d,%d)", tight.Max.X-1, y)
		}
	}
}

func TestWordFringeRecoveryStopsAtNearbyTableBorder(t *testing.T) {
	background := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	foreground := color.NRGBA{A: 255}
	source := solidNRGBA(72, 44, background)
	groundTruth := drawAntialiasedH(source, image.Rect(24, 12, 36, 28), foreground, background, 2)
	borderX := nonZeroBounds(groundTruth).Max.X + 1
	for y := 2; y < 42; y++ {
		source.SetNRGBA(borderX, y, foreground)
	}
	box := ocrBoxFromRectangle(nonZeroBounds(groundTruth))

	built := buildFixtureMask(t, source, box, "word", foreground, background)
	assertMaskRecallAtLeast(t, built.mask, groundTruth, 0.97)
	for y := 2; y < 42; y++ {
		if built.mask.GrayAt(borderX, y).Y != 0 {
			t.Fatalf("table border entered cleanup mask at (%d,%d)", borderX, y)
		}
	}
	if built.protectedMask.GrayAt(borderX, box.Y+box.Height/2).Y == 0 {
		t.Fatal("nearby table border was not protected")
	}
}

func TestTextInsideUIButtonRecoversFringeWithoutDamagingButton(t *testing.T) {
	page := color.NRGBA{R: 238, G: 240, B: 244, A: 255}
	fill := color.NRGBA{R: 48, G: 104, B: 190, A: 255}
	border := color.NRGBA{R: 22, G: 55, B: 112, A: 255}
	foreground := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	source := solidNRGBA(120, 64, page)
	button := image.Rect(12, 10, 108, 54)
	fillNRGBARectangle(source, button, fill)
	drawColoredRectangle(source, button, border)
	groundTruth := drawAntialiasedH(source, image.Rect(48, 22, 64, 42), foreground, fill, 2)
	box := ocrBoxFromRectangle(nonZeroBounds(groundTruth))

	built := buildFixtureMask(t, source, box, "word", foreground, fill)
	assertMaskRecallAtLeast(t, built.mask, groundTruth, 0.97)
	for _, point := range []image.Point{button.Min, {X: button.Max.X - 1, Y: button.Min.Y}, {X: button.Min.X, Y: button.Max.Y - 1}, {X: button.Max.X - 1, Y: button.Max.Y - 1}, {X: 30, Y: 32}} {
		if built.mask.GrayAt(point.X, point.Y).Y != 0 {
			t.Fatalf("button pixel %v entered local text mask", point)
		}
	}
	assertFalsePositivesOutsideHaloAtMost(t, built.mask, groundTruth, 2, 2)
}

func TestSmallUILabelCleanupStaysLocal(t *testing.T) {
	background := color.NRGBA{R: 245, G: 245, B: 245, A: 255}
	foreground := color.NRGBA{R: 35, G: 35, B: 35, A: 255}
	source := solidNRGBA(48, 28, background)
	groundTruth := drawAntialiasedH(source, image.Rect(18, 10, 25, 18), foreground, background, 1)
	box := ocrBoxFromRectangle(nonZeroBounds(groundTruth))

	built := buildFixtureMask(t, source, box, "word", foreground, background)
	assertMaskRecallAtLeast(t, built.mask, groundTruth, 0.97)
	assertFalsePositivesOutsideHaloAtMost(t, built.mask, groundTruth, 1, 2)
	if built.mask.GrayAt(nonZeroBounds(groundTruth).Min.X-2, 14).Y != 0 {
		t.Fatal("tiny label dilation extended two pixels beyond the glyph")
	}
}

func TestLargeHeadingRecoversWideAntiAliasFringe(t *testing.T) {
	background := color.NRGBA{R: 252, G: 252, B: 252, A: 255}
	foreground := color.NRGBA{R: 20, G: 25, B: 35, A: 255}
	source := solidNRGBA(110, 72, background)
	groundTruth := drawAntialiasedH(source, image.Rect(36, 18, 58, 50), foreground, background, 3)
	box := ocrBoxFromRectangle(nonZeroBounds(groundTruth))
	region := CleanupRegion{Level: "word", Box: box, TextHeight: box.Height}
	if radius := cleanupDilationRadius(region, defaultCleanupMaskConfig); radius <= 1 {
		t.Fatalf("large heading dilation radius=%d, want > 1", radius)
	}

	built := buildFixtureMask(t, source, box, "word", foreground, background)
	assertMaskRecallAtLeast(t, built.mask, groundTruth, 0.97)
	assertFalsePositivesOutsideHaloAtMost(t, built.mask, groundTruth, 2, 4)
}

func TestColoredSubpixelFringeRecovery(t *testing.T) {
	background := color.NRGBA{R: 250, G: 250, B: 250, A: 255}
	foreground := color.NRGBA{R: 20, G: 20, B: 20, A: 255}
	source := solidNRGBA(70, 44, background)
	core := drawCoreHMask(source.Bounds(), image.Rect(24, 12, 38, 29))
	groundTruth := dilateMask(core, 2)
	paintMask(source, groundTruth, color.NRGBA{R: 238, G: 244, B: 248, A: 255})
	paintMask(source, dilateMask(core, 1), color.NRGBA{R: 188, G: 215, B: 239, A: 255})
	paintMask(source, core, foreground)

	built := buildFixtureMask(t, source, ocrBoxFromRectangle(nonZeroBounds(groundTruth)), "word", foreground, background)
	assertMaskRecallAtLeast(t, built.mask, groundTruth, 0.97)
	assertFalsePositivesOutsideHaloAtMost(t, built.mask, groundTruth, 2, 2)
}

func TestFadedResizeArtifactFringeRecovery(t *testing.T) {
	background := color.NRGBA{R: 238, G: 236, B: 232, A: 255}
	foreground := color.NRGBA{R: 92, G: 90, B: 88, A: 255}
	source := solidNRGBA(72, 46, background)
	core := drawCoreHMask(source.Bounds(), image.Rect(25, 13, 40, 31))
	groundTruth := dilateMask(core, 2)
	paintMask(source, groundTruth, color.NRGBA{R: 228, G: 226, B: 221, A: 255})
	paintMask(source, dilateMask(core, 1), color.NRGBA{R: 194, G: 196, B: 190, A: 255})
	paintMask(source, core, color.NRGBA{R: 105, G: 101, B: 98, A: 255})

	built := buildFixtureMask(t, source, ocrBoxFromRectangle(nonZeroBounds(groundTruth)), "word", foreground, background)
	assertMaskRecallAtLeast(t, built.mask, groundTruth, 0.97)
	assertFalsePositivesOutsideHaloAtMost(t, built.mask, groundTruth, 2, 2)
}

func TestParagraphFallbackDoesNotUseAggressiveWordCleanup(t *testing.T) {
	background := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	foreground := color.NRGBA{A: 255}
	source := solidNRGBA(150, 84, background)
	diagram := image.Rect(8, 8, 72, 64)
	drawRectangle(source, diagram)
	for x := 18; x < 62; x++ {
		source.SetNRGBA(x, 20+(x-18)/2, foreground)
	}
	groundTruth := drawAntialiasedH(source, image.Rect(102, 28, 114, 44), foreground, background, 2)
	paragraph := ocr.OCRBox{X: 5, Y: 5, Width: 135, Height: 68}

	built := buildFixtureMaskWithTextHeight(t, source, paragraph, "paragraph", 18, foreground, background)
	for _, point := range []image.Point{{8, 8}, {71, 8}, {8, 63}, {71, 63}, {40, 31}} {
		if built.mask.GrayAt(point.X, point.Y).Y != 0 {
			t.Fatalf("paragraph fallback selected diagram pixel %v", point)
		}
	}
	if recall := maskRecall(built.mask, groundTruth); recall < 0.60 {
		t.Fatalf("paragraph fallback lost all text evidence: recall=%.3f", recall)
	}
}

func TestCleanupCompletenessMetric(t *testing.T) {
	background := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	foreground := color.NRGBA{A: 255}
	source := solidNRGBA(80, 48, background)
	groundTruth := drawAntialiasedH(source, image.Rect(28, 14, 43, 32), foreground, background, 2)
	built := buildFixtureMask(t, source, ocrBoxFromRectangle(nonZeroBounds(groundTruth)), "word", foreground, background)

	if recall := maskRecall(built.mask, groundTruth); recall < 0.97 {
		t.Fatalf("final cleanup recall=%.3f, want >= 0.970", recall)
	}
	assertFalsePositivesOutsideHaloAtMost(t, built.mask, groundTruth, 2, 2)
}

func TestWordCleanupFillsEnclosedGlyphInteriorWithoutTouchingNeighbor(t *testing.T) {
	background := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	foreground := color.NRGBA{A: 255}
	source := solidNRGBA(84, 54, background)
	coreBounds := image.Rect(28, 13, 48, 41)
	core := image.NewGray(source.Bounds())
	for y := coreBounds.Min.Y; y < coreBounds.Max.Y; y++ {
		for x := coreBounds.Min.X; x < coreBounds.Max.X; x++ {
			if x < coreBounds.Min.X+2 || x >= coreBounds.Max.X-2 || y < coreBounds.Min.Y+2 || y >= coreBounds.Max.Y-2 {
				core.SetGray(x, y, color.Gray{Y: 255})
			}
		}
	}
	groundTruth := dilateMask(core, 2)
	paintMask(source, groundTruth, color.NRGBA{R: 226, G: 226, B: 226, A: 255})
	paintMask(source, dilateMask(core, 1), color.NRGBA{R: 165, G: 165, B: 165, A: 255})
	paintMask(source, core, foreground)
	borderX := nonZeroBounds(groundTruth).Max.X + 1
	for y := 2; y < 52; y++ {
		source.SetNRGBA(borderX, y, foreground)
	}

	built := buildFixtureMask(t, source, ocrBoxFromRectangle(nonZeroBounds(groundTruth)), "word", foreground, background)
	center := image.Pt((coreBounds.Min.X+coreBounds.Max.X)/2, (coreBounds.Min.Y+coreBounds.Max.Y)/2)
	if built.mask.GrayAt(center.X, center.Y).Y == 0 {
		t.Fatalf("enclosed area under glyph was not selected at %v", center)
	}
	cleaned := cloneNRGBA(source)
	if err := solidCleanup(context.Background(), cleaned, built.mask, background); err != nil {
		t.Fatal(err)
	}
	for y := coreBounds.Min.Y; y < coreBounds.Max.Y; y++ {
		for x := coreBounds.Min.X; x < coreBounds.Max.X; x++ {
			if got := cleaned.NRGBAAt(x, y); got != background {
				t.Fatalf("pixel under glyph remained at (%d,%d): %+v", x, y, got)
			}
		}
	}
	for y := 2; y < 52; y++ {
		if built.mask.GrayAt(borderX, y).Y != 0 {
			t.Fatalf("neighboring border entered cleanup mask at (%d,%d)", borderX, y)
		}
		if got := cleaned.NRGBAAt(borderX, y); got != foreground {
			t.Fatalf("neighboring border changed at (%d,%d): %+v", borderX, y, got)
		}
	}
	if built.mask.GrayAt(coreBounds.Min.X-5, center.Y).Y != 0 {
		t.Fatal("cleanup expanded beyond the local glyph boundary")
	}
}

func TestWordBBoxTestModeFillsExactRectangle(t *testing.T) {
	background := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	foreground := color.NRGBA{A: 255}
	source := solidNRGBA(80, 44, background)
	box := ocr.OCRBox{X: 20, Y: 12, Width: 36, Height: 20}
	for _, startX := range []int{24, 45} {
		for y := 16; y < 28; y++ {
			for x := startX; x < startX+3; x++ {
				source.SetNRGBA(x, y, foreground)
			}
		}
	}

	safe := buildFixtureMask(t, source, box, "word", foreground, background)
	gap := image.Pt(box.X+box.Width/2, box.Y+2)
	if safe.mask.GrayAt(gap.X, gap.Y).Y != 0 {
		t.Fatalf("safe mode unexpectedly filled word bbox gap at %v", gap)
	}
	config := defaultCleanupMaskConfig
	config.FillWordBoxes = true
	built := buildFixtureMaskWithConfig(t, source, box, "word", box.Height, foreground, background, config)
	for y := box.Y; y < box.Y+box.Height; y++ {
		for x := box.X; x < box.X+box.Width; x++ {
			if built.mask.GrayAt(x, y).Y == 0 {
				t.Fatalf("word bbox pixel was not selected at (%d,%d)", x, y)
			}
		}
	}
	if built.mask.GrayAt(box.X-3, box.Y+box.Height/2).Y != 0 {
		t.Fatal("word bbox test mode expanded into a distant neighboring pixel")
	}
	if built.mask.GrayAt(gap.X, box.Y-1).Y != 0 {
		t.Fatal("rectangular word fill was dilated beyond the exact bbox")
	}
}

func TestRendererCleanSelectsWordBBoxModeFromDocument(t *testing.T) {
	background := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	foreground := color.NRGBA{A: 255}
	source := solidNRGBA(80, 44, background)
	box := ocr.OCRBox{X: 20, Y: 12, Width: 36, Height: 20}
	for _, startX := range []int{24, 45} {
		for y := 16; y < 28; y++ {
			for x := startX; x < startX+3; x++ {
				source.SetNRGBA(x, y, foreground)
			}
		}
	}
	block := safetyBlock("word-mode", box, CleanupSolid)
	renderer, err := NewRenderer(t.TempDir(), DefaultRenderConfig(), &fakeInpaintEngine{})
	if err != nil {
		t.Fatal(err)
	}
	gap := image.Pt(box.X+box.Width/2, box.Y+2)
	_, _, safeStats, err := renderer.Clean(context.Background(), source, RenderDocument{Blocks: []RenderBlock{block}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, fullStats, err := renderer.Clean(context.Background(), source, RenderDocument{FillWordBoxes: true, Blocks: []RenderBlock{block}})
	if err != nil {
		t.Fatal(err)
	}
	if safeStats.Diagnostics.FinalCleanupMask.GrayAt(gap.X, gap.Y).Y != 0 {
		t.Fatalf("safe document mode filled word bbox gap at %v", gap)
	}
	if fullStats.Diagnostics.FinalCleanupMask.GrayAt(gap.X, gap.Y).Y == 0 {
		t.Fatalf("full document mode did not fill word bbox gap at %v", gap)
	}
}

func TestFullWordBBoxModePreservesProtectedLineInsideRectangle(t *testing.T) {
	background := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	foreground := color.NRGBA{A: 255}
	source := solidNRGBA(80, 44, background)
	box := ocr.OCRBox{X: 20, Y: 12, Width: 36, Height: 20}
	for y := 16; y < 28; y++ {
		for x := 24; x < 27; x++ {
			source.SetNRGBA(x, y, foreground)
		}
	}
	protectedX := box.X + box.Width - 3
	for y := 0; y < source.Bounds().Dy(); y++ {
		source.SetNRGBA(protectedX, y, foreground)
	}
	config := defaultCleanupMaskConfig
	config.FillWordBoxes = true
	built := buildFixtureMaskWithConfig(t, source, box, "word", box.Height, foreground, background, config)
	for y := box.Y; y < box.Y+box.Height; y++ {
		if built.mask.GrayAt(protectedX, y).Y != 0 {
			t.Fatalf("protected line entered filled word bbox at (%d,%d)", protectedX, y)
		}
	}
	if built.mask.GrayAt(box.X+box.Width/2, box.Y+2).Y == 0 {
		t.Fatal("unprotected word bbox interior was not filled")
	}
}

func buildFixtureMask(t *testing.T, source *image.NRGBA, box ocr.OCRBox, level string, foreground, background color.NRGBA) maskBuildResult {
	t.Helper()
	return buildFixtureMaskWithTextHeight(t, source, box, level, box.Height, foreground, background)
}

func buildFixtureMaskWithTextHeight(t *testing.T, source *image.NRGBA, box ocr.OCRBox, level string, textHeight int, foreground, background color.NRGBA) maskBuildResult {
	t.Helper()
	return buildFixtureMaskWithConfig(t, source, box, level, textHeight, foreground, background, defaultCleanupMaskConfig)
}

func buildFixtureMaskWithConfig(t *testing.T, source *image.NRGBA, box ocr.OCRBox, level string, textHeight int, foreground, background color.NRGBA, config cleanupMaskConfig) maskBuildResult {
	t.Helper()
	built, err := buildSafeTextMask(context.Background(), source, RenderBlock{
		ID: "fixture", SourceBox: box, CleanupBox: box,
		CleanupRegions: []CleanupRegion{{Level: level, Box: box, TextHeight: textHeight}},
		Foreground:     newRenderColor(foreground), Background: newRenderColor(background), CleanupMode: CleanupSolid,
	}, config)
	if err != nil {
		t.Fatal(err)
	}
	return built
}

func drawAntialiasedH(target *image.NRGBA, coreBounds image.Rectangle, foreground, background color.NRGBA, fringeRadius int) *image.Gray {
	core := drawCoreHMask(target.Bounds(), coreBounds)
	groundTruth := dilateMask(core, fringeRadius)
	for radius := fringeRadius; radius >= 1; radius-- {
		layer := dilateMask(core, radius)
		amount := 0.10 + 0.16*float64(fringeRadius-radius)
		paintMask(target, layer, blendNRGBA(background, foreground, amount))
	}
	paintMask(target, core, foreground)
	return groundTruth
}

func drawCoreHMask(bounds, coreBounds image.Rectangle) *image.Gray {
	core := image.NewGray(bounds)
	stroke := max(1, coreBounds.Dx()/6)
	for y := coreBounds.Min.Y; y < coreBounds.Max.Y; y++ {
		for x := coreBounds.Min.X; x < coreBounds.Min.X+stroke; x++ {
			core.SetGray(x, y, color.Gray{Y: 255})
		}
		for x := coreBounds.Max.X - stroke; x < coreBounds.Max.X; x++ {
			core.SetGray(x, y, color.Gray{Y: 255})
		}
	}
	middle := coreBounds.Min.Y + coreBounds.Dy()/2
	for y := middle - stroke/2; y < middle-stroke/2+stroke; y++ {
		for x := coreBounds.Min.X; x < coreBounds.Max.X; x++ {
			core.SetGray(x, y, color.Gray{Y: 255})
		}
	}
	return core
}

func paintMask(target *image.NRGBA, mask *image.Gray, value color.NRGBA) {
	for y := mask.Bounds().Min.Y; y < mask.Bounds().Max.Y; y++ {
		for x := mask.Bounds().Min.X; x < mask.Bounds().Max.X; x++ {
			if mask.GrayAt(x, y).Y != 0 {
				target.SetNRGBA(x, y, value)
			}
		}
	}
}

func blendNRGBA(background, foreground color.NRGBA, amount float64) color.NRGBA {
	blend := func(bg, fg uint8) uint8 { return uint8(float64(bg)*(1-amount) + float64(fg)*amount + 0.5) }
	return color.NRGBA{R: blend(background.R, foreground.R), G: blend(background.G, foreground.G), B: blend(background.B, foreground.B), A: 255}
}

func ocrBoxFromRectangle(rectangle image.Rectangle) ocr.OCRBox {
	return ocr.OCRBox{X: rectangle.Min.X, Y: rectangle.Min.Y, Width: rectangle.Dx(), Height: rectangle.Dy()}
}

func maskRecall(mask, groundTruth *image.Gray) float64 {
	groundTruthPixels := countMask(groundTruth)
	if groundTruthPixels == 0 {
		return 1
	}
	covered := 0
	for y := groundTruth.Bounds().Min.Y; y < groundTruth.Bounds().Max.Y; y++ {
		for x := groundTruth.Bounds().Min.X; x < groundTruth.Bounds().Max.X; x++ {
			if groundTruth.GrayAt(x, y).Y != 0 && mask.GrayAt(x, y).Y != 0 {
				covered++
			}
		}
	}
	return float64(covered) / float64(groundTruthPixels)
}

func assertMaskRecallAtLeast(t *testing.T, mask, groundTruth *image.Gray, minimum float64) {
	t.Helper()
	if recall := maskRecall(mask, groundTruth); recall < minimum {
		t.Fatalf("final cleanup recall=%.3f, want >= %.3f", recall, minimum)
	}
}

func assertFalsePositivesOutsideHaloAtMost(t *testing.T, mask, groundTruth *image.Gray, haloRadius, maximum int) {
	t.Helper()
	halo := dilateMask(groundTruth, haloRadius)
	outside := 0
	for y := mask.Bounds().Min.Y; y < mask.Bounds().Max.Y; y++ {
		for x := mask.Bounds().Min.X; x < mask.Bounds().Max.X; x++ {
			if mask.GrayAt(x, y).Y != 0 && halo.GrayAt(x, y).Y == 0 {
				outside++
			}
		}
	}
	if outside > maximum {
		t.Fatalf("false-positive pixels outside local halo=%d, want <= %d", outside, maximum)
	}
}

func fillNRGBARectangle(target *image.NRGBA, rectangle image.Rectangle, value color.NRGBA) {
	for y := rectangle.Min.Y; y < rectangle.Max.Y; y++ {
		for x := rectangle.Min.X; x < rectangle.Max.X; x++ {
			target.SetNRGBA(x, y, value)
		}
	}
}

func drawColoredRectangle(target *image.NRGBA, rectangle image.Rectangle, value color.NRGBA) {
	for x := rectangle.Min.X; x < rectangle.Max.X; x++ {
		target.SetNRGBA(x, rectangle.Min.Y, value)
		target.SetNRGBA(x, rectangle.Max.Y-1, value)
	}
	for y := rectangle.Min.Y; y < rectangle.Max.Y; y++ {
		target.SetNRGBA(rectangle.Min.X, y, value)
		target.SetNRGBA(rectangle.Max.X-1, y, value)
	}
}
