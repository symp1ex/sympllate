package imagebatch

import (
	"context"
	"fmt"
	"image"
	"math"
	"strings"
	"unicode"

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
			document.SkippedBlocks = append(document.SkippedBlocks, SkippedRenderBlock{ID: block.ID, Stage: "layout", Reason: "unknown_block_id", SourceText: block.SourceText, OCRConfidence: block.Confidence, SourceBox: block.Box, TranslationText: block.TranslatedText})
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
		case hasAmbiguousOverlap(sourceBoxes[index], sourceBoxes, index):
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
			document.SkippedBlocks = append(document.SkippedBlocks, skippedRenderBlock("layout", reason, paragraph, block, sourceBoxes[index]))
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
		container := sourceTextContainer(sourceBoxes[index], sourceBoxes, index, width, height)
		alignment, verticalAlignment := chooseAlignment(paragraph, transform, container)
		fontEstimate, err := EstimateSourceTypography(ctx, r.fonts, paragraph, transform, r.config.MinimumFontSize, r.config.MaximumFontSize)
		if err != nil {
			return RenderDocument{}, fmt.Errorf("estimate source font size for block %s: %w", paragraph.ID, err)
		}
		preferredFontSize := fontEstimate.FontSize
		textBox, fit, err := r.fitBlock(ctx, block.TranslatedText, preferredFontSize, sourceLineCount(paragraph), fontEstimate.LineStep, alignment, sourceBoxes[index], sourceBoxes, index, activeTextBoxes, width, height)
		if err != nil {
			return RenderDocument{}, fmt.Errorf("layout block %s: %w", paragraph.ID, err)
		}
		if !fit.Fits {
			document.SkippedBlocks = append(document.SkippedBlocks, skippedRenderBlock("layout", "text_does_not_fit_safely", paragraph, block, sourceBoxes[index]))
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
		cleanupSafe := paragraphCleanupSafe(paragraph)
		if !cleanupSafe {
			cleanupRegions = nil
			document.CleanupDiagnostics.UnsafeBlocks++
		} else {
			document.CleanupDiagnostics.SafeBlocks++
		}
		sourceWords, sourceLines, sourceHeights, sourceWidths, sourceGaps := sourceLayoutDiagnostics(paragraph, transform)
		document.Blocks = append(document.Blocks, RenderBlock{
			ID: paragraph.ID, SourceText: paragraph.Text, TranslatedText: block.TranslatedText,
			SourceBox: sourceBoxes[index], SourcePolygon: paragraphPolygon(paragraph), CleanupBox: cleanup, CleanupRegions: cleanupRegions, TextBox: textBox,
			SourceWords: sourceWords, SourceLines: sourceLines, SourceLineHeights: sourceHeights, SourceLineWidths: sourceWidths, SourceLineGaps: sourceGaps,
			FontEstimate: fontEstimate,
			Background:   newRenderColor(background.Color), Foreground: newRenderColor(foreground),
			CleanupMode: cleanupModeFor(background), CleanupSafe: cleanupSafe, CleanupSafetyKnown: true,
			FontSize: fit.FontSize, PreferredFontSize: preferredFontSize,
			MinimumFontSize: math.Max(r.config.MinimumFontSize, preferredFontSize*r.config.Layout.PreferredShrinkRatio),
			MaximumFontSize: math.Min(r.config.MaximumFontSize, preferredFontSize*r.config.Layout.MaximumUpscaleRatio),
			LineSpacing:     r.config.LineSpacing, Lines: fit.Lines, LineLayouts: lineLayouts,
			SourceLineCount: sourceLineCount(paragraph), LineHeight: fit.LineHeight, LineStep: fit.LineStep, Ascent: fit.Ascent,
			TextWidth: fit.TextWidth, TextHeight: fit.TextHeight, TranslatedLineCount: len(fit.Lines),
			Alignment: alignment, VerticalAlign: verticalAlignment,
			BoxExpanded: fit.BoxExpanded, FontReduced: fit.FontReduced, EmergencyShrink: fit.EmergencyShrink,
			LayoutScore: fit.Score, FontReductionRatio: fit.FontReductionRatio, ExpansionRatio: fit.ExpansionRatio,
			AnchorDisplacement: fit.AnchorDisplacement, LineStepRatio: fit.LineStepRatio, FallbackReason: fit.FallbackReason,
			Status: "renderable", Warning: strings.Join(warnings, ","),
		})
		activeTextBoxes = append(activeTextBoxes, textBox)
	}
	document.LayoutDiagnostics = LayoutDiagnostics{TranslatedBlocks: len(translation.Blocks), RenderableBlocks: len(document.Blocks), SkippedBlocks: len(document.SkippedBlocks)}
	return document, nil
}

func skippedRenderBlock(stage, reason string, paragraph ocr.OCRParagraph, block TranslatedBlock, box ocr.OCRBox) SkippedRenderBlock {
	return SkippedRenderBlock{ID: paragraph.ID, Stage: stage, Reason: reason, SourceText: paragraph.Text, OCRConfidence: paragraph.Confidence, SourcePolygon: paragraphPolygon(paragraph), SourceBox: box, TranslationText: block.TranslatedText}
}

func paragraphPolygon(paragraph ocr.OCRParagraph) ocr.OCRPolygon {
	for _, line := range paragraph.Lines {
		for _, word := range line.Words {
			if word.Polygon != (ocr.OCRPolygon{}) {
				return word.Polygon
			}
		}
	}
	return ocr.OCRPolygon{}
}

func paragraphCleanupSafe(paragraph ocr.OCRParagraph) bool {
	for _, line := range paragraph.Lines {
		for _, word := range line.Words {
			if !word.CleanupSafe && word.RecognizerConfidence > 0 {
				return false
			}
		}
	}
	return true
}

func cleanupRegionsFor(paragraph ocr.OCRParagraph, transform CoordinateTransform, width, height int) []CleanupRegion {
	regions := make([]CleanupRegion, 0)
	for _, line := range paragraph.Lines {
		for _, word := range line.Words {
			if !word.Accepted || (!word.CleanupSafe && word.RecognizerConfidence > 0) {
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

func (r *Renderer) fitBlock(ctx context.Context, text string, preferredFontSize float64, sourceLines int, sourceLineStep float64, alignment string, base ocr.OCRBox, occupied []ocr.OCRBox, own int, active []ocr.OCRBox, width, height int) (ocr.OCRBox, TextFitResult, error) {
	if hasAmbiguousOverlap(base, active, -1) {
		return base, TextFitResult{}, nil
	}
	preferredFontSize = math.Max(r.config.MinimumFontSize, math.Min(r.config.MaximumFontSize, preferredFontSize))
	maximumFontSize := math.Min(r.config.MaximumFontSize, preferredFontSize*r.config.Layout.MaximumUpscaleRatio)
	candidates, err := r.layoutCandidates(text, preferredFontSize, sourceLines, sourceLineStep, alignment, base, occupied, own, active, width, height)
	if err != nil {
		return ocr.OCRBox{}, TextFitResult{}, err
	}
	normalMinimum := math.Max(r.config.MinimumFontSize, preferredFontSize*r.config.Layout.PreferredShrinkRatio)
	box, fit, err := r.bestCandidateFit(ctx, text, preferredFontSize, maximumFontSize, normalMinimum, sourceLines, sourceLineStep, alignment, base, candidates, false)
	if err != nil || fit.Fits {
		return box, fit, err
	}
	if normalMinimum > r.config.MinimumFontSize {
		box, fit, err = r.bestCandidateFit(ctx, text, preferredFontSize, maximumFontSize, r.config.MinimumFontSize, sourceLines, sourceLineStep, alignment, base, candidates, true)
		if err != nil || fit.Fits {
			return box, fit, err
		}
	}
	return base, fit, nil
}

func (r *Renderer) layoutCandidates(text string, preferred float64, sourceLines int, sourceLineStep float64, alignment string, base ocr.OCRBox, occupied []ocr.OCRBox, own int, active []ocr.OCRBox, width, height int) ([]layoutCandidate, error) {
	face, err := r.fonts.face(preferred)
	if err != nil {
		return nil, err
	}
	left, right, bottom := freeSpaceLimits(base, occupied, own, active, width, height)
	step := max(1, int(math.Round(sourceLineStep)))
	if sourceLineStep <= 0 {
		step = max(1, int(math.Ceil(float64(face.Metrics().Height.Ceil())*r.config.LineSpacing)))
	}
	lineHeight := max(1, (face.Metrics().Ascent + face.Metrics().Descent).Ceil())
	naturalWidth := measure(face, strings.Join(strings.Fields(text), " ")) + r.config.HorizontalTextPadding*2 + 1
	result := []layoutCandidate{{box: base}}
	seen := map[ocr.OCRBox]struct{}{base: {}}
	boxes := make([]ocr.OCRBox, 0, 2)
	for _, targetLines := range []int{max(1, sourceLines), max(1, sourceLines+1)} {
		targetWidth := max(base.Width, int(math.Ceil(float64(naturalWidth)/float64(targetLines))))
		targetWidth = min(targetWidth, min(right-left, base.Width+step*6))
		targetHeight := lineHeight + step*max(0, targetLines-1) + r.config.VerticalTextPadding*2 + 1
		targetHeight = min(bottom-base.Y, max(base.Height, targetHeight))
		if targetHeight-base.Height < max(2, step/2) {
			targetHeight = base.Height
		}
		box := anchoredWidth(base, targetWidth, alignment, left, right)
		box.Height = targetHeight
		boxes = append(boxes, box)
	}
	for _, box := range boxes {
		box = ClampBox(box, width, height)
		if box == base || box.Width <= 0 || box.Height <= 0 {
			continue
		}
		if _, ok := seen[box]; ok || hasAmbiguousOverlap(box, occupied, own) || hasAmbiguousOverlap(box, active, -1) {
			continue
		}
		seen[box] = struct{}{}
		result = append(result, layoutCandidate{box: box, expanded: true})
	}
	return result, nil
}

func freeSpaceLimits(base ocr.OCRBox, occupied []ocr.OCRBox, own int, active []ocr.OCRBox, width, height int) (int, int, int) {
	left, right, bottom := 0, width, height
	boxes := append(append([]ocr.OCRBox(nil), occupied...), active...)
	for index, other := range boxes {
		if index == own || other == base || other.Width <= 0 || other.Height <= 0 {
			continue
		}
		verticalOverlap := overlapPixels(base.Y, base.Y+base.Height, other.Y, other.Y+other.Height)
		centersNear := absInt((base.Y+base.Height/2)-(other.Y+other.Height/2)) <= max(base.Height, other.Height)*2
		if verticalOverlap > 0 || centersNear {
			if other.X+other.Width <= base.X {
				left = max(left, other.X+other.Width)
			} else if other.X >= base.X+base.Width {
				right = min(right, other.X)
			}
		}
		if overlapPixels(base.X, base.X+base.Width, other.X, other.X+other.Width) > 0 && other.Y >= base.Y+base.Height {
			bottom = min(bottom, other.Y)
		}
	}
	return left, right, bottom
}

func anchoredWidth(base ocr.OCRBox, width int, alignment string, left, right int) ocr.OCRBox {
	width = min(max(base.Width, width), right-left)
	x := base.X
	switch alignment {
	case "right":
		x = base.X + base.Width - width
	case "center":
		x = base.X + (base.Width-width)/2
	}
	if x < left {
		x = left
	}
	if x+width > right {
		x = right - width
	}
	return ocr.OCRBox{X: x, Y: base.Y, Width: width, Height: base.Height}
}

func (r *Renderer) textFitRequest(text string, minimum, maximum, preferred, sourceLineStep float64) TextFitRequest {
	return TextFitRequest{
		Text: text, MinFontSize: minimum, MaxFontSize: maximum, PreferredFontSize: preferred,
		LineSpacing: r.config.LineSpacing, SourceLineStep: sourceLineStep,
		HorizontalPad: r.config.HorizontalTextPadding, VerticalPad: r.config.VerticalTextPadding,
	}
}

func (r *Renderer) bestCandidateFit(ctx context.Context, text string, preferred, maximum, minimum float64, sourceLines int, sourceLineStep float64, alignment string, base ocr.OCRBox, candidates []layoutCandidate, emergency bool) (ocr.OCRBox, TextFitResult, error) {
	request := r.textFitRequest(text, minimum, maximum, preferred, sourceLineStep)
	bestBox := base
	var best TextFitResult
	for _, candidate := range candidates {
		request.Width, request.Height = candidate.box.Width, candidate.box.Height
		fit, err := FitText(ctx, r.fonts, request)
		if err != nil {
			return ocr.OCRBox{}, TextFitResult{}, err
		}
		decorateLayoutResult(&fit, preferred, sourceLines, sourceLineStep, alignment, base, candidate, emergency)
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

func decorateLayoutResult(result *TextFitResult, preferred float64, sourceLines int, sourceLineStep float64, alignment string, base ocr.OCRBox, candidate layoutCandidate, emergency bool) {
	result.PreferredFontSize = preferred
	result.BoxExpanded = candidate.expanded
	result.FontReduced = result.FontSize+fontSizeStep/2 < preferred
	result.EmergencyShrink = emergency && result.FontReduced
	result.FontReductionRatio = result.FontSize / math.Max(preferred, fontSizeStep)
	result.ExpansionRatio = boxExpansionRatio(base, candidate.box)
	result.AnchorDisplacement = layoutAnchorDisplacement(base, candidate.box, alignment)
	if sourceLineStep > 0 {
		result.LineStepRatio = float64(result.LineStep) / sourceLineStep
	}
	result.Score = scoreLayout(*result, preferred, sourceLines, sourceLineStep, base, candidate.box, emergency)
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

func scoreLayout(result TextFitResult, preferred float64, sourceLines int, sourceLineStep float64, base, candidate ocr.OCRBox, emergency bool) float64 {
	visualScaleChange := math.Abs(math.Log(math.Max(result.FontSize, fontSizeStep) / math.Max(preferred, fontSizeStep)))
	lineDifference := math.Abs(float64(len(result.Lines) - max(1, sourceLines)))
	stepDifference := 0.0
	if sourceLineStep > 0 {
		stepDifference = normalizedDifference(float64(result.LineStep), sourceLineStep)
	}
	anchor := result.AnchorDisplacement / math.Max(preferred, 1)
	score := visualScaleChange*120 + lineDifference*18 + stepDifference*25 + result.ExpansionRatio*10 + anchor*35
	if emergency {
		score += 35
	}
	return roundMetric(score)
}

func boxExpansionRatio(base, candidate ocr.OCRBox) float64 {
	baseArea, candidateArea := base.Width*base.Height, candidate.Width*candidate.Height
	if baseArea <= 0 || candidateArea <= baseArea {
		return 0
	}
	return roundMetric(float64(candidateArea-baseArea) / float64(baseArea))
}

func layoutAnchorDisplacement(base, candidate ocr.OCRBox, alignment string) float64 {
	baseAnchor, candidateAnchor := float64(base.X), float64(candidate.X)
	switch alignment {
	case "right":
		baseAnchor, candidateAnchor = float64(base.X+base.Width), float64(candidate.X+candidate.Width)
	case "center":
		baseAnchor, candidateAnchor = float64(base.X)+float64(base.Width)/2, float64(candidate.X)+float64(candidate.Width)/2
	}
	return roundMetric(math.Hypot(candidateAnchor-baseAnchor, float64(candidate.Y-base.Y)))
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
		switch alignment {
		case "center":
			x = box.X + (box.Width-lineWidth)/2
		case "right":
			x = box.X + box.Width - horizontalPadding - lineWidth
		}
		result = append(result, RenderLineLayout{Text: line, X: x, BaselineY: y, Width: lineWidth})
		y += fit.LineStep
	}
	return result
}

func chooseAlignment(paragraph ocr.OCRParagraph, transform CoordinateTransform, container ocr.OCRBox) (string, string) {
	lineBoxes := make([]ocr.OCRBox, 0, len(paragraph.Lines))
	for _, line := range paragraph.Lines {
		box := TransformBox(line.Box, transform)
		if box.Width > 0 && box.Height > 0 {
			lineBoxes = append(lineBoxes, box)
		}
	}
	if len(lineBoxes) == 0 {
		lineBoxes = append(lineBoxes, TransformBox(paragraph.Box, transform))
	}
	vertical := "top"
	if len(lineBoxes) == 1 {
		vertical = "middle"
		box := lineBoxes[0]
		leftMargin := box.X - container.X
		rightMargin := container.X + container.Width - (box.X + box.Width)
		centerDistance := math.Abs((float64(box.X) + float64(box.Width)/2) - (float64(container.X) + float64(container.Width)/2))
		tolerance := max(box.Height, int(math.Round(float64(container.Width)*0.03)))
		switch {
		case centerDistance <= float64(tolerance):
			return "center", vertical
		case rightMargin+tolerance < leftMargin:
			return "right", vertical
		default:
			return "left", vertical
		}
	}
	lefts, rights, centers := make([]float64, 0, len(lineBoxes)), make([]float64, 0, len(lineBoxes)), make([]float64, 0, len(lineBoxes))
	for _, box := range lineBoxes {
		lefts = append(lefts, float64(box.X))
		rights = append(rights, float64(box.X+box.Width))
		centers = append(centers, float64(box.X)+float64(box.Width)/2)
	}
	leftSpread, rightSpread, centerSpread := valueSpread(lefts), valueSpread(rights), valueSpread(centers)
	tolerance := float64(max(2, medianBoxHeight(lineBoxes)/2))
	if centerSpread <= tolerance && leftSpread > tolerance && rightSpread > tolerance {
		return "center", vertical
	}
	if rightSpread <= tolerance && leftSpread > tolerance {
		return "right", vertical
	}
	return "left", vertical
}

func sourceTextContainer(base ocr.OCRBox, boxes []ocr.OCRBox, own, width, height int) ocr.OCRBox {
	left, right := 0, width
	for index, other := range boxes {
		if index == own || overlapPixels(base.Y, base.Y+base.Height, other.Y, other.Y+other.Height) == 0 {
			continue
		}
		if other.X+other.Width <= base.X {
			left = max(left, (other.X+other.Width+base.X)/2)
		} else if other.X >= base.X+base.Width {
			right = min(right, (base.X+base.Width+other.X)/2)
		}
	}
	return ocr.OCRBox{X: left, Y: 0, Width: max(1, right-left), Height: height}
}

func sourceLayoutDiagnostics(paragraph ocr.OCRParagraph, transform CoordinateTransform) ([]SourceWordLayout, []SourceLineLayout, []int, []int, []int) {
	words := make([]SourceWordLayout, 0)
	lines := make([]SourceLineLayout, 0, len(paragraph.Lines))
	heights := make([]int, 0, len(paragraph.Lines))
	widths := make([]int, 0, len(paragraph.Lines))
	gaps := make([]int, 0, max(0, len(paragraph.Lines)-1))
	previous := ocr.OCRBox{}
	for index, line := range paragraph.Lines {
		box := TransformBox(line.Box, transform)
		lines = append(lines, SourceLineLayout{ID: line.ID, Text: line.Text, Box: box, Width: box.Width, Height: box.Height})
		heights = append(heights, box.Height)
		widths = append(widths, box.Width)
		if index > 0 {
			gaps = append(gaps, box.Y-(previous.Y+previous.Height))
		}
		previous = box
		for _, word := range line.Words {
			words = append(words, SourceWordLayout{Text: word.Text, Box: TransformBox(word.Box, transform)})
		}
	}
	return words, lines, heights, widths, gaps
}

func valueSpread(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	minimum, maximum := values[0], values[0]
	for _, value := range values[1:] {
		minimum = math.Min(minimum, value)
		maximum = math.Max(maximum, value)
	}
	return maximum - minimum
}

func medianBoxHeight(boxes []ocr.OCRBox) int {
	heights := make([]float64, 0, len(boxes))
	for _, box := range boxes {
		heights = append(heights, float64(box.Height))
	}
	return int(math.Round(median(heights)))
}

func overlapPixels(a0, a1, b0, b1 int) int {
	return max(0, min(a1, b1)-max(a0, b0))
}

func intersectsOther(candidate ocr.OCRBox, boxes []ocr.OCRBox, own int) bool {
	for index, other := range boxes {
		if index != own && BoxesIntersect(candidate, other) {
			return true
		}
	}
	return false
}

func hasAmbiguousOverlap(candidate ocr.OCRBox, boxes []ocr.OCRBox, own int) bool {
	for index, other := range boxes {
		if index == own {
			continue
		}
		intersection := boxIntersectionArea(candidate, other)
		smaller := min(candidate.Width*candidate.Height, other.Width*other.Height)
		if smaller <= 0 || intersection == 0 {
			continue
		}
		ratio := float64(intersection) / float64(smaller)
		contained := ratio >= .85
		if contained || ratio >= .35 {
			return true
		}
	}
	return false
}

func boxIntersectionArea(left, right ocr.OCRBox) int {
	width := overlapPixels(left.X, left.X+left.Width, right.X, right.X+right.Width)
	height := overlapPixels(left.Y, left.Y+left.Height, right.Y, right.Y+right.Height)
	return width * height
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
