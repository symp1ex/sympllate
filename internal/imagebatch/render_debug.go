package imagebatch

import (
	"context"
	"errors"
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

func (s *Service) renderDebugArtifacts(ctx context.Context, job *batchJob, report *JobFileReport, cleaned, rendered *image.NRGBA, document RenderDocument) {
	if !job.request.Debug {
		return
	}
	stem := strings.TrimSuffix(report.OutputName, filepath.Ext(report.OutputName))
	operations := []struct {
		stage, path string
		run         func() error
	}{
		{"debug_cleaned", filepath.Join(job.layout.Debug, stem+".cleaned.png"), func() error {
			return atomicEncodeGoImage(ctx, cleaned, filepath.Join(job.layout.Debug, stem+".cleaned.png"), ".png", s.renderer.config.JPEGQuality)
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

func writeLayoutDebug(ctx context.Context, rendered *image.NRGBA, document RenderDocument, path string, jpegQuality int) error {
	canvas := cloneNRGBA(rendered)
	for _, block := range document.Blocks {
		if err := ctx.Err(); err != nil {
			return err
		}
		drawNRGBABox(canvas, block.SourceBox, color.NRGBA{R: 255, G: 48, B: 48, A: 255}, 1)
		drawNRGBABox(canvas, block.CleanupBox, color.NRGBA{R: 255, G: 190, B: 32, A: 255}, 2)
		drawNRGBABox(canvas, block.TextBox, color.NRGBA{R: 32, G: 128, B: 255, A: 255}, 1)
		drawNRGBALabel(canvas, block.TextBox.X+2, block.TextBox.Y+2, block.ID, color.NRGBA{R: 32, G: 128, B: 255, A: 255})
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
