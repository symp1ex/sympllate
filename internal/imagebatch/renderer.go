package imagebatch

import (
	"context"
	"fmt"
	"image"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sympllate/translator/internal/inpaint"
	"github.com/sympllate/translator/internal/ocr"
	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

type Renderer struct {
	config    RenderConfig
	fonts     *fontCache
	inpainter inpaint.Engine
}

func NewRenderer(executableDir string, config RenderConfig, inpainter inpaint.Engine) (*Renderer, error) {
	defaults := DefaultRenderConfig().Layout
	if config.Layout.MaximumUpscaleRatio == 0 {
		config.Layout.MaximumUpscaleRatio = defaults.MaximumUpscaleRatio
	}
	if config.Layout.PreferredShrinkRatio == 0 {
		config.Layout.PreferredShrinkRatio = defaults.PreferredShrinkRatio
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	if inpainter == nil {
		return nil, fmt.Errorf("invalid image renderer configuration: inpaint engine is required")
	}
	return &Renderer{config: config, fonts: newFontCache(executableDir), inpainter: inpainter}, nil
}

func (r *Renderer) Close() error {
	r.fonts.close()
	return r.inpainter.Close()
}

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
		cleanup := ExpandBox(sourceBoxes[index], CleanupPadding{Horizontal: cleanupPaddingHorizontal, Vertical: cleanupPaddingVertical}, width, height)
		if intersectsOther(cleanup, sourceBoxes, index) {
			cleanup = sourceBoxes[index]
		}
		background, err := SampleBackground(ctx, source, boxFromOCR(cleanup), backgroundSampleWidth, minimumBackgroundSamples, maximumBackgroundSamples)
		if err != nil {
			return RenderDocument{}, fmt.Errorf("sample background for block %s: %w", paragraph.ID, err)
		}
		foreground, err := SampleForeground(ctx, source, sourceBoxes[index], background.Color)
		if err != nil {
			return RenderDocument{}, err
		}
		alignment, verticalAlignment := chooseAlignment(paragraph, block.TranslatedText)
		preferredFontSize, err := EstimateSourceFontSize(ctx, r.fonts, paragraph, transform, r.config.MinimumFontSize, r.config.MaximumFontSize)
		if err != nil {
			return RenderDocument{}, fmt.Errorf("estimate source font size for block %s: %w", paragraph.ID, err)
		}
		textBox, fit, err := r.fitBlock(ctx, block.TranslatedText, preferredFontSize, sourceLineCount(paragraph), sourceBoxes[index], sourceBoxes, index, activeTextBoxes, width, height)
		if err != nil {
			return RenderDocument{}, fmt.Errorf("layout block %s: %w", paragraph.ID, err)
		}
		if !fit.Fits {
			document.SkippedBlocks = append(document.SkippedBlocks, SkippedRenderBlock{ID: paragraph.ID, Reason: "text_does_not_fit_safely"})
			continue
		}
		warnings := make([]string, 0, 1)
		if background.Fallback {
			warnings = append(warnings, "insufficient_background_sample")
			document.Warnings = append(document.Warnings, RenderWarning{Code: "insufficient_background_sample", BlockID: paragraph.ID})
		}
		if fit.FontSize <= r.config.MinimumFontSize {
			warnings = append(warnings, "minimum_font_size")
			document.Warnings = append(document.Warnings, RenderWarning{Code: "minimum_font_size", BlockID: paragraph.ID})
		}
		if fit.EmergencyShrink {
			warnings = append(warnings, "font_size_below_preferred_range")
			document.Warnings = append(document.Warnings, RenderWarning{Code: "font_size_below_preferred_range", BlockID: paragraph.ID})
		}
		lineLayouts := positionTextLines(fit, textBox, alignment, verticalAlignment, r.config.HorizontalTextPadding, r.config.VerticalTextPadding)
		cleanupRegions := cleanupRegionsFor(paragraph, transform, width, height)
		document.Blocks = append(document.Blocks, RenderBlock{
			ID: paragraph.ID, SourceText: paragraph.Text, TranslatedText: block.TranslatedText,
			SourceBox: sourceBoxes[index], CleanupBox: cleanup, CleanupRegions: cleanupRegions, TextBox: textBox,
			Background: newRenderColor(background.Color), Foreground: newRenderColor(foreground),
			CleanupMode: cleanupModeFor(background),
			FontSize:    fit.FontSize, PreferredFontSize: preferredFontSize,
			MinimumFontSize: math.Max(r.config.MinimumFontSize, preferredFontSize*r.config.Layout.PreferredShrinkRatio),
			MaximumFontSize: math.Min(r.config.MaximumFontSize, preferredFontSize*r.config.Layout.MaximumUpscaleRatio),
			LineSpacing:     r.config.LineSpacing, Lines: fit.Lines, LineLayouts: lineLayouts,
			SourceLineCount: sourceLineCount(paragraph), LineHeight: fit.LineHeight, LineStep: fit.LineStep, Ascent: fit.Ascent,
			TextWidth: fit.TextWidth, TextHeight: fit.TextHeight,
			Alignment: alignment, VerticalAlign: verticalAlignment,
			BoxExpanded: fit.BoxExpanded, FontReduced: fit.FontReduced, EmergencyShrink: fit.EmergencyShrink,
			LayoutScore: fit.Score, FallbackReason: fit.FallbackReason,
			Status: "renderable", Warning: strings.Join(warnings, ","),
		})
		activeTextBoxes = append(activeTextBoxes, textBox)
	}
	return document, nil
}

func cleanupRegionsFor(paragraph ocr.OCRParagraph, transform CoordinateTransform, width, height int) []CleanupRegion {
	regions := make([]CleanupRegion, 0)
	for _, line := range paragraph.Lines {
		for _, word := range line.Words {
			if !word.Accepted {
				continue
			}
			regions = appendCleanupRegion(regions, "word", word.Box, transform, width, height)
		}
	}
	if len(regions) > 0 {
		return regions
	}
	for _, line := range paragraph.Lines {
		regions = appendCleanupRegion(regions, "line", line.Box, transform, width, height)
	}
	if len(regions) > 0 {
		return regions
	}
	regions = appendCleanupRegion(regions, "paragraph", paragraph.Box, transform, width, height)
	if len(regions) == 1 {
		regions[0].TextHeight = max(1, regions[0].Box.Height/sourceLineCount(paragraph))
	}
	return regions
}

func appendCleanupRegion(regions []CleanupRegion, level string, box ocr.OCRBox, transform CoordinateTransform, width, height int) []CleanupRegion {
	box = ClampBox(TransformBox(box, transform), width, height)
	if box.Width <= 0 || box.Height <= 0 {
		return regions
	}
	for _, existing := range regions {
		if existing.Box == box {
			return regions
		}
	}
	return append(regions, CleanupRegion{Level: level, Box: box, TextHeight: box.Height})
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

type layoutCandidate struct {
	box      ocr.OCRBox
	expanded bool
}

func (r *Renderer) fitBlock(ctx context.Context, text string, preferredFontSize float64, sourceLines int, base ocr.OCRBox, occupied []ocr.OCRBox, own int, active []ocr.OCRBox, width, height int) (ocr.OCRBox, TextFitResult, error) {
	if intersectsAny(base, active) {
		return base, TextFitResult{}, nil
	}
	candidates := []layoutCandidate{{box: base}}
	down := ClampBox(ocr.OCRBox{X: base.X, Y: base.Y, Width: base.Width, Height: min(height-base.Y, base.Height+max(24, base.Height))}, width, height)
	wideBy := min(max(16, base.Width/3), width/8)
	wide := ClampBox(ocr.OCRBox{X: base.X - wideBy, Y: base.Y, Width: base.Width + wideBy*2, Height: down.Height}, width, height)
	for _, candidate := range []ocr.OCRBox{down, wide} {
		if candidate != base && !intersectsOther(candidate, occupied, own) && !intersectsAny(candidate, active) {
			candidates = append(candidates, layoutCandidate{box: candidate, expanded: true})
		}
	}
	preferredFontSize = math.Max(r.config.MinimumFontSize, math.Min(r.config.MaximumFontSize, preferredFontSize))
	maximumFontSize := math.Min(r.config.MaximumFontSize, preferredFontSize*r.config.Layout.MaximumUpscaleRatio)
	preferredRequest := r.textFitRequest(text, preferredFontSize, maximumFontSize, preferredFontSize)
	for _, candidate := range candidates {
		preferredRequest.Width, preferredRequest.Height = candidate.box.Width, candidate.box.Height
		fit, err := FitText(ctx, r.fonts, preferredRequest)
		if err != nil {
			return ocr.OCRBox{}, TextFitResult{}, err
		}
		if fit.Fits {
			decorateLayoutResult(&fit, preferredFontSize, sourceLines, base, candidate, false)
			return candidate.box, fit, nil
		}
	}
	normalMinimum := math.Max(r.config.MinimumFontSize, preferredFontSize*r.config.Layout.PreferredShrinkRatio)
	box, fit, err := r.bestCandidateFit(ctx, text, preferredFontSize, maximumFontSize, normalMinimum, sourceLines, base, candidates, false)
	if err != nil || fit.Fits {
		return box, fit, err
	}
	if normalMinimum > r.config.MinimumFontSize {
		box, fit, err = r.bestCandidateFit(ctx, text, preferredFontSize, maximumFontSize, r.config.MinimumFontSize, sourceLines, base, candidates, true)
		if err != nil || fit.Fits {
			return box, fit, err
		}
	}
	return base, fit, nil
}

func (r *Renderer) textFitRequest(text string, minimum, maximum, preferred float64) TextFitRequest {
	return TextFitRequest{
		Text: text, MinFontSize: minimum, MaxFontSize: maximum, PreferredFontSize: preferred,
		LineSpacing: r.config.LineSpacing, HorizontalPad: r.config.HorizontalTextPadding, VerticalPad: r.config.VerticalTextPadding,
	}
}

func (r *Renderer) bestCandidateFit(ctx context.Context, text string, preferred, maximum, minimum float64, sourceLines int, base ocr.OCRBox, candidates []layoutCandidate, emergency bool) (ocr.OCRBox, TextFitResult, error) {
	request := r.textFitRequest(text, minimum, maximum, preferred)
	bestBox := base
	var best TextFitResult
	for _, candidate := range candidates {
		request.Width, request.Height = candidate.box.Width, candidate.box.Height
		fit, err := FitText(ctx, r.fonts, request)
		if err != nil {
			return ocr.OCRBox{}, TextFitResult{}, err
		}
		decorateLayoutResult(&fit, preferred, sourceLines, base, candidate, emergency)
		if !fit.Fits {
			if best.FontSize == 0 {
				best = fit
			}
			continue
		}
		if !best.Fits || fit.Score < best.Score {
			bestBox, best = candidate.box, fit
		}
	}
	return bestBox, best, nil
}

func decorateLayoutResult(result *TextFitResult, preferred float64, sourceLines int, base ocr.OCRBox, candidate layoutCandidate, emergency bool) {
	result.PreferredFontSize = preferred
	result.BoxExpanded = candidate.expanded
	result.FontReduced = result.FontSize+fontSizeStep/2 < preferred
	result.EmergencyShrink = emergency && result.FontReduced
	result.Score = scoreLayout(*result, preferred, sourceLines, base, candidate.box, emergency)
	switch {
	case result.EmergencyShrink && result.BoxExpanded:
		result.FallbackReason = "bbox_expanded_and_emergency_font_reduction"
	case result.EmergencyShrink:
		result.FallbackReason = "emergency_font_reduction"
	case result.FontReduced && result.BoxExpanded:
		result.FallbackReason = "bbox_expanded_and_font_reduced"
	case result.FontReduced:
		result.FallbackReason = "font_reduced"
	case result.BoxExpanded:
		result.FallbackReason = "bbox_expanded"
	default:
		result.FallbackReason = ""
	}
}

func scoreLayout(result TextFitResult, preferred float64, sourceLines int, base, candidate ocr.OCRBox, emergency bool) float64 {
	shrink := math.Max(0, preferred-result.FontSize) / math.Max(preferred, 1)
	expansion := 0.0
	baseArea, candidateArea := base.Width*base.Height, candidate.Width*candidate.Height
	if baseArea > 0 && candidateArea > baseArea {
		expansion = float64(candidateArea-baseArea) / float64(baseArea)
	}
	lineDifference := math.Abs(float64(len(result.Lines) - max(1, sourceLines)))
	score := shrink*100 + expansion*3 + lineDifference*0.5
	if emergency {
		score += 25
	}
	return math.Round(score*1000) / 1000
}

func sourceLineCount(paragraph ocr.OCRParagraph) int {
	if len(paragraph.Lines) > 0 {
		return len(paragraph.Lines)
	}
	return max(1, len(nonEmptyTextLines(paragraph.Text)))
}

func positionTextLines(fit TextFitResult, box ocr.OCRBox, alignment, verticalAlignment string, horizontalPadding, verticalPadding int) []RenderLineLayout {
	y := box.Y + verticalPadding + fit.Ascent
	if verticalAlignment == "middle" {
		y = box.Y + (box.Height-fit.TextHeight)/2 + fit.Ascent
	}
	result := make([]RenderLineLayout, 0, len(fit.Lines))
	for index, line := range fit.Lines {
		lineWidth := 0
		if index < len(fit.LineWidths) {
			lineWidth = fit.LineWidths[index]
		}
		x := box.X + horizontalPadding
		if alignment == "center" {
			x = box.X + (box.Width-lineWidth)/2
		}
		result = append(result, RenderLineLayout{Text: line, X: x, BaselineY: y, Width: lineWidth})
		y += fit.LineStep
	}
	return result
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

func (r *Renderer) Draw(ctx context.Context, target *image.NRGBA, document RenderDocument) error {
	for _, block := range document.Blocks {
		if err := ctx.Err(); err != nil {
			return err
		}
		face, err := r.fonts.face(block.FontSize)
		if err != nil {
			return err
		}
		lineLayouts := block.LineLayouts
		if len(lineLayouts) == 0 && len(block.Lines) > 0 {
			metrics := face.Metrics()
			lineHeight := max(1, (metrics.Ascent + metrics.Descent).Ceil())
			lineStep := max(lineHeight, int(math.Ceil(float64(metrics.Height.Ceil())*block.LineSpacing)))
			lineWidths := make([]int, len(block.Lines))
			textWidth := 0
			for index, line := range block.Lines {
				lineWidths[index] = measure(face, line)
				textWidth = max(textWidth, lineWidths[index])
			}
			fit := TextFitResult{
				Lines: block.Lines, LineWidths: lineWidths, TextWidth: textWidth,
				TextHeight: lineHeight + lineStep*max(0, len(block.Lines)-1),
				LineHeight: lineHeight, LineStep: lineStep, Ascent: metrics.Ascent.Ceil(),
			}
			lineLayouts = positionTextLines(fit, block.TextBox, block.Alignment, block.VerticalAlign, r.config.HorizontalTextPadding, r.config.VerticalTextPadding)
		}
		drawer := font.Drawer{Dst: target, Src: image.NewUniform(block.Foreground.NRGBA()), Face: face}
		for _, line := range lineLayouts {
			if err := ctx.Err(); err != nil {
				return err
			}
			drawer.Dot = fixed.P(line.X, line.BaselineY)
			drawer.DrawString(line.Text)
		}
	}
	return nil
}
