package imagebatch

import (
	"context"
	"fmt"
	"image"
	"image/draw"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sympllate/translator/internal/ocr"
	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

type Renderer struct {
	config RenderConfig
	fonts  *fontCache
}

func NewRenderer(executableDir string, config RenderConfig) (*Renderer, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	return &Renderer{config: config, fonts: newFontCache(executableDir)}, nil
}

func (r *Renderer) Close() { r.fonts.close() }

func (r *Renderer) Prepare(ctx context.Context, source *image.NRGBA, page ocr.OCRPage, translation TranslationDocument) (RenderDocument, error) {
	width, height := source.Bounds().Dx(), source.Bounds().Dy()
	ocrWidth, ocrHeight := page.Image.Width, page.Image.Height
	if ocrWidth == 0 && ocrHeight == 0 {
		ocrWidth, ocrHeight = width, height
	}
	transform, err := NewCoordinateTransform(width, height, ocrWidth, ocrHeight)
	if err != nil {
		return RenderDocument{}, err
	}
	document := RenderDocument{SchemaVersion: SchemaVersion, SourceFile: translation.SourceFile, ImageWidth: width, ImageHeight: height, Transform: transform, Blocks: []RenderBlock{}, SkippedBlocks: []SkippedRenderBlock{}, Warnings: []RenderWarning{}}
	translated := make(map[string]TranslatedBlock, len(translation.Blocks))
	paragraphIDs := make(map[string]struct{}, len(page.Paragraphs))
	for _, paragraph := range page.Paragraphs {
		paragraphIDs[paragraph.ID] = struct{}{}
	}
	for _, block := range translation.Blocks {
		if _, ok := paragraphIDs[block.ID]; !ok {
			document.SkippedBlocks = append(document.SkippedBlocks, SkippedRenderBlock{ID: block.ID, Reason: "unknown_block_id"})
			continue
		}
		translated[block.ID] = block
	}
	sourceBoxes := make([]ocr.OCRBox, len(page.Paragraphs))
	for index, paragraph := range page.Paragraphs {
		sourceBoxes[index] = ClampBox(TransformBox(paragraph.Box, transform), width, height)
	}
	activeTextBoxes := make([]ocr.OCRBox, 0, len(page.Paragraphs))
	for index, paragraph := range page.Paragraphs {
		if err := ctx.Err(); err != nil {
			return RenderDocument{}, err
		}
		block, ok := translated[paragraph.ID]
		reason := ""
		switch {
		case strings.TrimSpace(paragraph.ID) == "":
			reason = "missing_block_id"
		case strings.TrimSpace(paragraph.Text) == "":
			reason = "empty_source_text"
		case !ok:
			reason = "missing_translation"
		case block.Status != "" && block.Status != "translated":
			reason = "translation_not_successful"
		case strings.TrimSpace(block.TranslatedText) == "":
			reason = "empty_translation"
		case sourceBoxes[index].Width <= 0 || sourceBoxes[index].Height <= 0:
			reason = "invalid_or_outside_box"
		case intersectsOther(sourceBoxes[index], sourceBoxes, index):
			reason = "overlapping_ocr_box"
		}
		if reason == "" {
			supported, supportErr := r.supportsText(block.TranslatedText)
			if supportErr != nil {
				return RenderDocument{}, supportErr
			}
			if !supported {
				reason = "unsupported_script_or_glyph"
			}
		}
		if reason != "" {
			document.SkippedBlocks = append(document.SkippedBlocks, SkippedRenderBlock{ID: paragraph.ID, Reason: reason})
			continue
		}
		cleanup := ExpandBox(sourceBoxes[index], CleanupPadding{Horizontal: r.config.CleanupPaddingX, Vertical: r.config.CleanupPaddingY}, width, height)
		if intersectsOther(cleanup, sourceBoxes, index) {
			cleanup = sourceBoxes[index]
		}
		background, err := SampleBackground(ctx, source, boxFromOCR(cleanup), r.config.BackgroundSampleWidth, r.config.MinimumSamples, r.config.MaximumSamples)
		if err != nil {
			return RenderDocument{}, fmt.Errorf("sample background for block %s: %w", paragraph.ID, err)
		}
		foreground, err := SampleForeground(ctx, source, sourceBoxes[index], background.Color)
		if err != nil {
			return RenderDocument{}, err
		}
		alignment, verticalAlignment := chooseAlignment(paragraph, block.TranslatedText)
		textBox, fit, err := r.fitBlock(ctx, block.TranslatedText, cleanup, sourceBoxes, index, activeTextBoxes, width, height)
		if err != nil {
			return RenderDocument{}, fmt.Errorf("layout block %s: %w", paragraph.ID, err)
		}
		if !fit.Fits {
			document.SkippedBlocks = append(document.SkippedBlocks, SkippedRenderBlock{ID: paragraph.ID, Reason: "text_does_not_fit_safely"})
			continue
		}
		warnings := make([]string, 0, 2)
		if background.Fallback {
			warnings = append(warnings, "insufficient_background_sample")
			document.Warnings = append(document.Warnings, RenderWarning{Code: "insufficient_background_sample", BlockID: paragraph.ID})
		}
		if background.Variance > r.config.NonUniformThreshold {
			warnings = append(warnings, "non_uniform_background")
			document.Warnings = append(document.Warnings, RenderWarning{Code: "non_uniform_background", BlockID: paragraph.ID})
		}
		if fit.FontSize <= r.config.MinimumFontSize {
			warnings = append(warnings, "minimum_font_size")
			document.Warnings = append(document.Warnings, RenderWarning{Code: "minimum_font_size", BlockID: paragraph.ID})
		}
		document.Blocks = append(document.Blocks, RenderBlock{
			ID: paragraph.ID, SourceText: paragraph.Text, TranslatedText: block.TranslatedText,
			SourceBox: sourceBoxes[index], CleanupBox: cleanup, TextBox: textBox,
			Background: newRenderColor(background.Color), Foreground: newRenderColor(foreground),
			FontSize: fit.FontSize, LineSpacing: r.config.LineSpacing, Lines: fit.Lines,
			Alignment: alignment, VerticalAlign: verticalAlignment, Status: "renderable", Warning: strings.Join(warnings, ","),
		})
		activeTextBoxes = append(activeTextBoxes, textBox)
	}
	return document, nil
}

func (r *Renderer) supportsText(value string) (bool, error) {
	face, err := r.fonts.face(r.config.MinimumFontSize)
	if err != nil {
		return false, err
	}
	for _, character := range value {
		if unicode.IsSpace(character) {
			continue
		}
		if unicode.IsLetter(character) && !unicode.In(character, unicode.Latin, unicode.Cyrillic) {
			return false, nil
		}
		if _, ok := face.GlyphAdvance(character); !ok {
			return false, nil
		}
	}
	return true, nil
}

func (r *Renderer) fitBlock(ctx context.Context, text string, base ocr.OCRBox, occupied []ocr.OCRBox, own int, active []ocr.OCRBox, width, height int) (ocr.OCRBox, TextFitResult, error) {
	if intersectsAny(base, active) {
		return base, TextFitResult{}, nil
	}
	candidates := []ocr.OCRBox{base}
	down := ClampBox(ocr.OCRBox{X: base.X, Y: base.Y, Width: base.Width, Height: min(height-base.Y, base.Height+max(24, base.Height))}, width, height)
	wideBy := min(max(16, base.Width/3), width/8)
	wide := ClampBox(ocr.OCRBox{X: base.X - wideBy, Y: base.Y, Width: base.Width + wideBy*2, Height: down.Height}, width, height)
	for _, candidate := range []ocr.OCRBox{down, wide} {
		if candidate != base && !intersectsOther(candidate, occupied, own) && !intersectsAny(candidate, active) {
			candidates = append(candidates, candidate)
		}
	}
	maxSize := min(r.config.MaximumFontSize, math.Max(r.config.MinimumFontSize, float64(base.Height)*1.2))
	var last TextFitResult
	for _, candidate := range candidates {
		fit, err := FitText(ctx, r.fonts, TextFitRequest{Text: text, Width: candidate.Width, Height: candidate.Height, MinFontSize: r.config.MinimumFontSize, MaxFontSize: maxSize, LineSpacing: r.config.LineSpacing, HorizontalPad: r.config.HorizontalTextPadding, VerticalPad: r.config.VerticalTextPadding})
		if err != nil {
			return ocr.OCRBox{}, TextFitResult{}, err
		}
		last = fit
		if fit.Fits {
			return candidate, fit, nil
		}
	}
	return base, last, nil
}

func chooseAlignment(paragraph ocr.OCRParagraph, translatedText string) (string, string) {
	if len(paragraph.Lines) == 1 && utf8.RuneCountInString(strings.TrimSpace(translatedText)) <= 40 {
		return "center", "middle"
	}
	return "left", "top"
}

func intersectsOther(candidate ocr.OCRBox, boxes []ocr.OCRBox, own int) bool {
	for index, other := range boxes {
		if index != own && BoxesIntersect(candidate, other) {
			return true
		}
	}
	return false
}

func intersectsAny(candidate ocr.OCRBox, boxes []ocr.OCRBox) bool {
	for _, box := range boxes {
		if BoxesIntersect(candidate, box) {
			return true
		}
	}
	return false
}

func (r *Renderer) Clean(ctx context.Context, source *image.NRGBA, document RenderDocument) (*image.NRGBA, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	target := image.NewNRGBA(image.Rect(0, 0, source.Bounds().Dx(), source.Bounds().Dy()))
	draw.Draw(target, target.Bounds(), source, source.Bounds().Min, draw.Src)
	for _, block := range document.Blocks {
		value := block.Background.NRGBA()
		for y := block.CleanupBox.Y; y < block.CleanupBox.Y+block.CleanupBox.Height; y++ {
			if y&31 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			for x := block.CleanupBox.X; x < block.CleanupBox.X+block.CleanupBox.Width; x++ {
				target.SetNRGBA(x, y, value)
			}
		}
	}
	return target, nil
}

func (r *Renderer) Draw(ctx context.Context, target *image.NRGBA, document RenderDocument) error {
	for _, block := range document.Blocks {
		if err := ctx.Err(); err != nil {
			return err
		}
		face, err := r.fonts.face(block.FontSize)
		if err != nil {
			return err
		}
		metrics := face.Metrics()
		lineHeight := max(1, metrics.Height.Ceil())
		lineStep := int(math.Ceil(float64(lineHeight) * block.LineSpacing))
		totalHeight := lineHeight + lineStep*max(0, len(block.Lines)-1)
		y := block.TextBox.Y + r.config.VerticalTextPadding + metrics.Ascent.Ceil()
		if block.VerticalAlign == "middle" {
			y = block.TextBox.Y + (block.TextBox.Height-totalHeight)/2 + metrics.Ascent.Ceil()
		}
		drawer := font.Drawer{Dst: target, Src: image.NewUniform(block.Foreground.NRGBA()), Face: face}
		for _, line := range block.Lines {
			if err := ctx.Err(); err != nil {
				return err
			}
			x := block.TextBox.X + r.config.HorizontalTextPadding
			if block.Alignment == "center" {
				x = block.TextBox.X + (block.TextBox.Width-measure(face, line))/2
			}
			drawer.Dot = fixed.P(x, y)
			drawer.DrawString(line)
			y += lineStep
		}
	}
	return nil
}
