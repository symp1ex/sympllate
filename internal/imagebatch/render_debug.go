package imagebatch

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"path/filepath"
	"strings"

	"github.com/sympllate/translator/internal/ocr"
)

func cloneNRGBA(source *image.NRGBA) *image.NRGBA {
	target := image.NewNRGBA(image.Rect(0, 0, source.Bounds().Dx(), source.Bounds().Dy()))
	draw.Draw(target, target.Bounds(), source, source.Bounds().Min, draw.Src)
	return target
}

func (s *Service) renderDebugArtifacts(ctx context.Context, job *batchJob, report *JobFileReport, source, cleaned, rendered *image.NRGBA, document RenderDocument, stats CleanupStats) {
	if !job.request.Debug {
		return
	}
	stem := strings.TrimSuffix(report.OutputName, filepath.Ext(report.OutputName))
	operations := []struct {
		stage, path string
		run         func() error
	}{
		{"debug_original", filepath.Join(job.layout.Debug, stem+".original.png"), func() error {
			return atomicEncodeGoImage(ctx, source, filepath.Join(job.layout.Debug, stem+".original.png"), ".png", s.renderer.config.JPEGQuality)
		}},
		{"debug_candidate_mask", filepath.Join(job.layout.Debug, stem+".candidate-mask.png"), func() error {
			return atomicEncodeGoImage(ctx, cleanupMaskOrBlank(stats.Diagnostics.CandidateMask, source.Bounds()), filepath.Join(job.layout.Debug, stem+".candidate-mask.png"), ".png", s.renderer.config.JPEGQuality)
		}},
		{"debug_protected_mask", filepath.Join(job.layout.Debug, stem+".protected-graphics-mask.png"), func() error {
			return atomicEncodeGoImage(ctx, cleanupMaskOrBlank(stats.Diagnostics.ProtectedMask, source.Bounds()), filepath.Join(job.layout.Debug, stem+".protected-graphics-mask.png"), ".png", s.renderer.config.JPEGQuality)
		}},
		{"debug_final_mask", filepath.Join(job.layout.Debug, stem+".final-cleanup-mask.png"), func() error {
			return atomicEncodeGoImage(ctx, cleanupMaskOrBlank(stats.Diagnostics.FinalCleanupMask, source.Bounds()), filepath.Join(job.layout.Debug, stem+".final-cleanup-mask.png"), ".png", s.renderer.config.JPEGQuality)
		}},
		{"debug_cleanup_overlay", filepath.Join(job.layout.Debug, stem+".cleanup-overlay.png"), func() error {
			return writeCleanupDebug(ctx, source, document, stats.Diagnostics, filepath.Join(job.layout.Debug, stem+".cleanup-overlay.png"), s.renderer.config.JPEGQuality)
		}},
		{"debug_cleaned", filepath.Join(job.layout.Debug, stem+".cleaned.png"), func() error {
			return atomicEncodeGoImage(ctx, cleaned, filepath.Join(job.layout.Debug, stem+".cleaned.png"), ".png", s.renderer.config.JPEGQuality)
		}},
		{"debug_final", filepath.Join(job.layout.Debug, stem+".final.png"), func() error {
			return atomicEncodeGoImage(ctx, rendered, filepath.Join(job.layout.Debug, stem+".final.png"), ".png", s.renderer.config.JPEGQuality)
		}},
		{"debug_layout", filepath.Join(job.layout.Debug, stem+".layout.png"), func() error {
			return writeLayoutDebug(ctx, rendered, document, filepath.Join(job.layout.Debug, stem+".layout.png"), s.renderer.config.JPEGQuality)
		}},
		{"debug_render_json", filepath.Join(job.layout.Debug, stem+".render.json"), func() error { return atomicWriteJSON(filepath.Join(job.layout.Debug, stem+".render.json"), document) }},
	}
	for _, operation := range operations {
		if err := operation.run(); err != nil && !errors.Is(err, context.Canceled) {
			s.addFileError(job, report.SourceFile, operation.stage, err, true)
			s.logf("image batch debug output warning: id=%s name=%s stage=%s error=%v", job.status.ID, report.SourceFile, operation.stage, err)
		}
	}
}

func cleanupMaskOrBlank(mask *image.Gray, bounds image.Rectangle) *image.Gray {
	if mask != nil {
		return mask
	}
	return image.NewGray(bounds)
}

func writeCleanupDebug(ctx context.Context, source *image.NRGBA, document RenderDocument, diagnostics CleanupDiagnostics, path string, jpegQuality int) error {
	canvas := cloneNRGBA(source)
	overlayMask(canvas, diagnostics.CandidateMask, color.NRGBA{R: 255, G: 210, B: 32, A: 96})
	overlayMask(canvas, diagnostics.RejectedMask, color.NRGBA{R: 255, G: 96, B: 32, A: 144})
	overlayMask(canvas, diagnostics.ProtectedMask, color.NRGBA{R: 32, G: 220, B: 255, A: 176})
	overlayMask(canvas, diagnostics.FinalCleanupMask, color.NRGBA{R: 64, G: 220, B: 96, A: 192})
	for _, block := range document.Blocks {
		if err := ctx.Err(); err != nil {
			return err
		}
		for _, region := range block.CleanupRegions {
			drawNRGBABox(canvas, region.Box, color.NRGBA{R: 32, G: 200, B: 96, A: 255}, 1)
		}
	}
	return atomicEncodeGoImage(ctx, canvas, path, ".png", jpegQuality)
}

func overlayMask(target *image.NRGBA, mask *image.Gray, overlay color.NRGBA) {
	if mask == nil {
		return
	}
	bounds := target.Bounds().Intersect(mask.Bounds())
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if mask.GrayAt(x, y).Y == 0 {
				continue
			}
			base := target.NRGBAAt(x, y)
			alpha := uint32(overlay.A)
			inverse := uint32(255 - overlay.A)
			target.SetNRGBA(x, y, color.NRGBA{
				R: uint8((uint32(overlay.R)*alpha + uint32(base.R)*inverse) / 255),
				G: uint8((uint32(overlay.G)*alpha + uint32(base.G)*inverse) / 255),
				B: uint8((uint32(overlay.B)*alpha + uint32(base.B)*inverse) / 255),
				A: base.A,
			})
		}
	}
}

func writeLayoutDebug(ctx context.Context, rendered *image.NRGBA, document RenderDocument, path string, jpegQuality int) error {
	canvas := cloneNRGBA(rendered)
	for _, block := range document.Blocks {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Green=source words, magenta=source lines, red=source paragraph,
		// blue=final translated text container.
		for _, word := range block.SourceWords {
			drawNRGBABox(canvas, word.Box, color.NRGBA{R: 32, G: 210, B: 96, A: 255}, 1)
		}
		for _, line := range block.SourceLines {
			drawNRGBABox(canvas, line.Box, color.NRGBA{R: 220, G: 64, B: 220, A: 255}, 1)
		}
		drawNRGBABox(canvas, block.SourceBox, color.NRGBA{R: 255, G: 48, B: 48, A: 255}, 2)
		drawNRGBABox(canvas, block.CleanupBox, color.NRGBA{R: 255, G: 190, B: 32, A: 255}, 2)
		drawNRGBABox(canvas, block.TextBox, color.NRGBA{R: 32, G: 128, B: 255, A: 255}, 2)
		flags := ""
		if block.BoxExpanded {
			flags += "-E"
		}
		if block.FontReduced {
			flags += "-R"
		}
		label := fmt.Sprintf("%s-P%.0f-F%.0f-L%d-S%.0f%s", block.ID, block.PreferredFontSize, block.FontSize, len(block.Lines), block.LayoutScore, flags)
		drawNRGBALabel(canvas, block.TextBox.X+2, block.TextBox.Y+2, label, color.NRGBA{R: 32, G: 128, B: 255, A: 255})
	}
	return atomicEncodeGoImage(ctx, canvas, path, ".png", jpegQuality)
}

func drawNRGBABox(target *image.NRGBA, box ocr.OCRBox, value color.NRGBA, thickness int) {
	for offset := 0; offset < thickness; offset++ {
		left, top := box.X+offset, box.Y+offset
		right, bottom := box.X+box.Width-1-offset, box.Y+box.Height-1-offset
		for x := left; x <= right; x++ {
			setNRGBA(target, x, top, value)
			setNRGBA(target, x, bottom, value)
		}
		for y := top; y <= bottom; y++ {
			setNRGBA(target, left, y, value)
			setNRGBA(target, right, y, value)
		}
	}
}

func drawNRGBALabel(target *image.NRGBA, x, y int, value string, foreground color.NRGBA) {
	value = strings.ToUpper(value)
	for py := y; py < y+7; py++ {
		for px := x; px < x+len(value)*4+2; px++ {
			setNRGBA(target, px, py, color.NRGBA{A: 220})
		}
	}
	for index, character := range value {
		glyph, ok := debugGlyphs[character]
		if !ok {
			glyph = debugGlyphs['?']
		}
		for row, bits := range glyph {
			for column := 0; column < 3; column++ {
				if bits&(1<<(2-column)) != 0 {
					setNRGBA(target, x+1+index*4+column, y+1+row, foreground)
				}
			}
		}
	}
}

func setNRGBA(target *image.NRGBA, x, y int, value color.NRGBA) {
	if image.Pt(x, y).In(target.Bounds()) {
		target.SetNRGBA(x, y, value)
	}
}
