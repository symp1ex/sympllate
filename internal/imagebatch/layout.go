package imagebatch

import (
	"context"
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/sympllate/translator/internal/ocr"
	"golang.org/x/image/font"
)

const (
	fontSizeStep     = 0.25
	orphanWidthRatio = 0.45
)

// EstimateSourceFontSize is the compatibility wrapper for the richer geometry
// estimate used by Renderer.Prepare.
func EstimateSourceFontSize(ctx context.Context, fonts *fontCache, paragraph ocr.OCRParagraph, transform CoordinateTransform, minimum, maximum float64) (float64, error) {
	estimate, err := EstimateSourceTypography(ctx, fonts, paragraph, transform, minimum, maximum)
	return estimate.FontSize, err
}

type sourceLineGeometry struct {
	id, text         string
	box              ocr.OCRBox
	wordBoxes        []ocr.OCRBox
	wordTexts        []string
	medianWordHeight float64
	widthWeight      float64
}

// EstimateSourceTypography treats font size as a fitted parameter of the
// bundled face. It compares candidate font metrics with several observed OCR
// signals and combines lines robustly, so a short or unusual glyph run cannot
// dictate the whole paragraph.
func EstimateSourceTypography(ctx context.Context, fonts *fontCache, paragraph ocr.OCRParagraph, transform CoordinateTransform, minimum, maximum float64) (FontStyleEstimate, error) {
	minimum = math.Max(fontSizeStep, minimum)
	maximum = math.Max(minimum, maximum)
	geometry := sourceGeometry(paragraph, transform)
	if len(geometry) == 0 {
		return FontStyleEstimate{Style: "regular", FontSize: roundFontSize(minimum)}, nil
	}
	bestSize, bestError := minimum, math.MaxFloat64
	for size := roundFontSizeUp(minimum); size <= maximum+fontSizeStep/2; size += fontSizeStep {
		if err := ctx.Err(); err != nil {
			return FontStyleEstimate{}, err
		}
		face, err := fonts.face(size)
		if err != nil {
			return FontStyleEstimate{}, err
		}
		errors := make([]float64, 0, len(geometry))
		for _, line := range geometry {
			errors = append(errors, sourceLineCandidateError(face, line))
		}
		errorValue := trimmedMean(errors)
		if errorValue < bestError {
			bestSize, bestError = size, errorValue
		}
	}
	individual := make([]IndividualFontEstimate, 0, len(geometry))
	individualSizes := make([]float64, 0, len(geometry))
	for _, line := range geometry {
		lineBestSize, lineBestError := bestSize, math.MaxFloat64
		for size := roundFontSizeUp(minimum); size <= maximum+fontSizeStep/2; size += fontSizeStep {
			face, err := fonts.face(size)
			if err != nil {
				return FontStyleEstimate{}, err
			}
			if candidateError := sourceLineCandidateError(face, line); candidateError < lineBestError {
				lineBestSize, lineBestError = size, candidateError
			}
		}
		individualSizes = append(individualSizes, lineBestSize)
		individual = append(individual, IndividualFontEstimate{
			LineID: line.id, Text: line.text, SourceInkHeight: line.box.Height, SourceLineWidth: line.box.Width,
			MedianWordHeight: line.medianWordHeight, EstimatedSize: roundFontSize(lineBestSize),
			NormalizedError: roundMetric(lineBestError), WidthWeight: roundMetric(line.widthWeight),
		})
	}
	// The global metric fit is stabilized by the median individual estimate.
	// This prevents a wide noisy line from outweighing all remaining lines.
	if len(individualSizes) > 1 {
		bestSize = (bestSize + median(individualSizes)) / 2
	}
	bestSize = roundFontSize(math.Max(minimum, math.Min(maximum, bestSize)))
	lineStep := observedSourceLineStep(geometry)
	confidence := math.Max(0, math.Min(1, 1-bestError))
	if len(geometry) == 1 {
		confidence *= 0.75
	}
	contentRunes := 0
	for _, line := range geometry {
		contentRunes += utf8.RuneCountInString(strings.ReplaceAll(line.text, " ", ""))
	}
	bestSize = capShortRunFontSize(bestSize, minimum, contentRunes, geometry)
	if contentRunes <= 4 {
		confidence *= 0.45
	} else if contentRunes <= 8 {
		confidence *= 0.7
	}
	return FontStyleEstimate{
		Style: "regular", FontSize: bestSize, LineStep: roundMetric(lineStep),
		Confidence: roundMetric(confidence), IndividualEstimates: individual,
	}, nil
}

func capShortRunFontSize(size, minimum float64, contentRunes int, geometry []sourceLineGeometry) float64 {
	if contentRunes > 8 || len(geometry) == 0 {
		return size
	}
	heights := make([]float64, 0, len(geometry))
	for _, line := range geometry {
		height := line.medianWordHeight
		if height <= 0 {
			height = float64(line.box.Height)
		}
		if height > 0 {
			heights = append(heights, height)
		}
	}
	if len(heights) == 0 {
		return size
	}
	factor := 1.6
	if contentRunes <= 4 {
		factor = 1.35
	}
	return roundFontSize(math.Max(minimum, math.Min(size, median(heights)*factor)))
}

func sourceGeometry(paragraph ocr.OCRParagraph, transform CoordinateTransform) []sourceLineGeometry {
	result := make([]sourceLineGeometry, 0, len(paragraph.Lines))
	for _, line := range paragraph.Lines {
		text := strings.TrimSpace(line.Text)
		box := TransformBox(line.Box, transform)
		if text == "" || !utf8.ValidString(text) {
			continue
		}
		geometry := sourceLineGeometry{id: line.ID, text: text, box: box}
		heights := make([]float64, 0, len(line.Words))
		for _, word := range line.Words {
			wordText := strings.TrimSpace(word.Text)
			wordBox := TransformBox(word.Box, transform)
			if wordText == "" || wordBox.Width <= 0 || wordBox.Height <= 0 {
				continue
			}
			geometry.wordTexts = append(geometry.wordTexts, wordText)
			geometry.wordBoxes = append(geometry.wordBoxes, wordBox)
			heights = append(heights, float64(wordBox.Height))
		}
		if len(heights) > 0 {
			geometry.medianWordHeight = median(heights)
		}
		if (geometry.box.Width <= 0 || geometry.box.Height <= 0) && len(geometry.wordBoxes) > 0 {
			geometry.box = unionLayoutBoxes(geometry.wordBoxes)
		}
		if geometry.box.Width <= 0 || geometry.box.Height <= 0 {
			continue
		}
		runes := utf8.RuneCountInString(text)
		geometry.widthWeight = 0.01
		if len(geometry.wordBoxes) > 0 {
			geometry.widthWeight = math.Min(1, 0.60+float64(min(4, len(geometry.wordBoxes)))*0.05+float64(runes)/100)
			// A detector quad around one short glyph/run has much less reliable
			// horizontal geometry than its observed ink height. Letting width fit
			// dominate here is what produced giant isolated letters.
			averageRuneWidth := float64(geometry.box.Width) / float64(max(1, runes))
			if runes <= 4 && averageRuneWidth > float64(geometry.box.Height)*1.5 {
				geometry.widthWeight = 0.08
			} else if runes <= 8 && averageRuneWidth > float64(geometry.box.Height)*1.2 {
				geometry.widthWeight = math.Min(geometry.widthWeight, 0.25)
			}
		}
		result = append(result, geometry)
	}
	if len(result) == 0 {
		text := strings.TrimSpace(paragraph.Text)
		box := TransformBox(paragraph.Box, transform)
		if text != "" && box.Width > 0 && box.Height > 0 {
			lines := max(1, len(nonEmptyTextLines(text)))
			box.Height = max(1, box.Height/lines)
			result = append(result, sourceLineGeometry{text: text, box: box, widthWeight: 0.01})
		}
	}
	return result
}

func unionLayoutBoxes(boxes []ocr.OCRBox) ocr.OCRBox {
	if len(boxes) == 0 {
		return ocr.OCRBox{}
	}
	left, top := boxes[0].X, boxes[0].Y
	right, bottom := boxes[0].X+boxes[0].Width, boxes[0].Y+boxes[0].Height
	for _, box := range boxes[1:] {
		left, top = min(left, box.X), min(top, box.Y)
		right, bottom = max(right, box.X+box.Width), max(bottom, box.Y+box.Height)
	}
	return ocr.OCRBox{X: left, Y: top, Width: right - left, Height: bottom - top}
}

func sourceLineCandidateError(face font.Face, line sourceLineGeometry) float64 {
	bounds, _ := font.BoundString(face, line.text)
	predictedHeight := (bounds.Max.Y - bounds.Min.Y).Ceil()
	predictedWidth := measure(face, line.text)
	heightError := normalizedDifference(float64(predictedHeight), float64(line.box.Height))
	widthError := normalizedDifference(float64(predictedWidth), float64(line.box.Width))
	score := heightError*0.35 + widthError*line.widthWeight*0.65
	if len(line.wordBoxes) > 1 {
		wordErrors := make([]float64, 0, len(line.wordBoxes))
		for index, box := range line.wordBoxes {
			wordBounds, _ := font.BoundString(face, line.wordTexts[index])
			wordHeight := (wordBounds.Max.Y - wordBounds.Min.Y).Ceil()
			wordErrors = append(wordErrors,
				normalizedDifference(float64(wordHeight), float64(box.Height))*0.35+
					normalizedDifference(float64(measure(face, line.wordTexts[index])), float64(box.Width))*0.65,
			)
		}
		score = score*0.75 + trimmedMean(wordErrors)*0.25
	}
	return score
}

func observedSourceLineStep(lines []sourceLineGeometry) float64 {
	steps := make([]float64, 0, len(lines)-1)
	for index := 1; index < len(lines); index++ {
		previousCenter := float64(lines[index-1].box.Y) + float64(lines[index-1].box.Height)/2
		currentCenter := float64(lines[index].box.Y) + float64(lines[index].box.Height)/2
		if currentCenter > previousCenter {
			steps = append(steps, currentCenter-previousCenter)
		}
	}
	if len(steps) == 0 {
		return 0
	}
	return median(steps)
}

func normalizedDifference(left, right float64) float64 {
	return math.Abs(left-right) / math.Max(1, math.Max(math.Abs(left), math.Abs(right)))
}

func trimmedMean(values []float64) float64 {
	if len(values) == 0 {
		return 1
	}
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	trim := 0
	if len(ordered) >= 5 {
		trim = len(ordered) / 5
	}
	ordered = ordered[trim : len(ordered)-trim]
	total := 0.0
	for _, value := range ordered {
		total += value
	}
	return total / float64(len(ordered))
}

func roundMetric(value float64) float64 {
	return math.Round(value*1000) / 1000
}

func median(values []float64) float64 {
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	middle := len(ordered) / 2
	if len(ordered)%2 != 0 {
		return ordered[middle]
	}
	return (ordered[middle-1] + ordered[middle]) / 2
}

func nonEmptyTextLines(value string) []string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	result := make([]string, 0)
	for _, line := range strings.Split(value, "\n") {
		if strings.TrimSpace(line) != "" {
			result = append(result, line)
		}
	}
	return result
}

func WrapText(ctx context.Context, face font.Face, text string, maximumWidth int) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	if strings.TrimSpace(text) == "" || maximumWidth <= 0 {
		return []string{}, nil
	}
	paragraphs := strings.Split(text, "\n")
	lines := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if strings.TrimSpace(paragraph) == "" {
			lines = append(lines, "")
			continue
		}
		wrapped := wrapParagraph(face, paragraph, maximumWidth)
		lines = append(lines, balanceLastLine(face, wrapped, maximumWidth)...)
	}
	return lines, nil
}

func wrapParagraph(face font.Face, paragraph string, maximumWidth int) []string {
	words := layoutWords(paragraph)
	lines := make([]string, 0, 1)
	current := ""
	for _, word := range words {
		candidate := word
		if current != "" {
			candidate = current + " " + word
		}
		if measure(face, candidate) <= maximumWidth {
			current = candidate
			continue
		}
		if current != "" {
			lines = append(lines, current)
			current = ""
		}
		pieces := splitLongWord(face, word, maximumWidth)
		if len(pieces) > 1 {
			lines = append(lines, pieces[:len(pieces)-1]...)
		}
		if len(pieces) > 0 {
			current = pieces[len(pieces)-1]
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func layoutWords(value string) []string {
	fields := strings.Fields(value)
	result := make([]string, 0, len(fields))
	for _, field := range fields {
		if len(result) > 0 && isPunctuationToken(field, ".,;:!?%)]}»”") {
			result[len(result)-1] += field
			continue
		}
		if len(result) > 0 && isPunctuationToken(result[len(result)-1], "([{«“") {
			result[len(result)-1] += field
			continue
		}
		result = append(result, field)
	}
	return result
}

func isPunctuationToken(value, punctuation string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune(punctuation, character) {
			return false
		}
	}
	return true
}

// balanceLastLine fixes the most visible greedy-wrap defect: a single short
// word orphaned on the last line. It only moves whole words and only accepts a
// move that decreases the width difference without exceeding the box.
func balanceLastLine(face font.Face, lines []string, maximumWidth int) []string {
	if len(lines) < 2 {
		return lines
	}
	result := append([]string(nil), lines...)
	for {
		last := len(result) - 1
		previousWords := strings.Fields(result[last-1])
		if len(previousWords) < 2 {
			break
		}
		previousWidth := measure(face, result[last-1])
		lastWidth := measure(face, result[last])
		if float64(lastWidth) >= float64(previousWidth)*orphanWidthRatio {
			break
		}
		candidatePrevious := strings.Join(previousWords[:len(previousWords)-1], " ")
		candidateLast := previousWords[len(previousWords)-1] + " " + result[last]
		candidatePreviousWidth := measure(face, candidatePrevious)
		candidateLastWidth := measure(face, candidateLast)
		if candidateLastWidth > maximumWidth || absInt(candidatePreviousWidth-candidateLastWidth) >= absInt(previousWidth-lastWidth) {
			break
		}
		result[last-1], result[last] = candidatePrevious, candidateLast
	}
	return result
}

func splitLongWord(face font.Face, word string, maximumWidth int) []string {
	runes := []rune(word)
	result := make([]string, 0, 2)
	for len(runes) > 0 {
		low, high, best := 1, len(runes), 0
		for low <= high {
			middle := low + (high-low)/2
			if measure(face, string(runes[:middle])) <= maximumWidth {
				best, low = middle, middle+1
			} else {
				high = middle - 1
			}
		}
		if best == 0 {
			best = 1
		}
		result = append(result, string(runes[:best]))
		runes = runes[best:]
	}
	return result
}

// FitText preserves PreferredFontSize when it fits and otherwise finds the
// largest smaller size in quarter-pixel steps. A zero preferred size keeps the
// previous API behavior by treating MaxFontSize as preferred.
func FitText(ctx context.Context, fonts *fontCache, request TextFitRequest) (TextFitResult, error) {
	if err := ctx.Err(); err != nil {
		return TextFitResult{}, err
	}
	minimum, preferred := normalizedFontRange(request)
	if strings.TrimSpace(request.Text) == "" {
		return TextFitResult{PreferredFontSize: preferred, MinimumFontSize: minimum}, nil
	}
	if request.Width <= request.HorizontalPad*2 || request.Height <= request.VerticalPad*2 {
		return TextFitResult{FontSize: preferred, PreferredFontSize: preferred, MinimumFontSize: minimum, Overflow: true}, nil
	}
	preferredResult, err := measureFit(ctx, fonts, request, preferred)
	if err != nil {
		return TextFitResult{}, err
	}
	preferredResult.PreferredFontSize = preferred
	preferredResult.MinimumFontSize = minimum
	if preferredResult.Fits {
		return preferredResult, nil
	}
	minimumUnit := int(math.Ceil(minimum / fontSizeStep))
	preferredUnit := int(math.Floor(preferred/fontSizeStep)) - 1
	low, high := minimumUnit, preferredUnit
	var best TextFitResult
	var minimumResult TextFitResult
	for low <= high {
		if err := ctx.Err(); err != nil {
			return TextFitResult{}, err
		}
		candidateUnit := low + (high-low)/2
		candidate := float64(candidateUnit) * fontSizeStep
		result, err := measureFit(ctx, fonts, request, candidate)
		if err != nil {
			return TextFitResult{}, err
		}
		if candidateUnit == minimumUnit {
			minimumResult = result
		}
		if result.Fits {
			best, low = result, candidateUnit+1
		} else {
			high = candidateUnit - 1
		}
	}
	if best.Fits {
		best.PreferredFontSize = preferred
		best.MinimumFontSize = minimum
		best.FontReduced = best.FontSize < preferred
		best.FallbackReason = "font_reduced"
		return best, nil
	}
	if minimumResult.FontSize == 0 {
		minimumResult, err = measureFit(ctx, fonts, request, minimum)
		if err != nil {
			return TextFitResult{}, err
		}
	}
	minimumResult.PreferredFontSize = preferred
	minimumResult.MinimumFontSize = minimum
	minimumResult.FontReduced = minimum < preferred
	minimumResult.Overflow = true
	minimumResult.FallbackReason = "minimum_size_overflow"
	return minimumResult, nil
}

func normalizedFontRange(request TextFitRequest) (float64, float64) {
	minimum := math.Max(fontSizeStep, request.MinFontSize)
	maximum := math.Max(minimum, request.MaxFontSize)
	preferred := request.PreferredFontSize
	if preferred <= 0 {
		preferred = maximum
	}
	preferred = math.Max(minimum, math.Min(maximum, preferred))
	minimum = roundFontSizeUp(minimum)
	preferred = math.Max(minimum, roundFontSize(preferred))
	return minimum, preferred
}

func measureFit(ctx context.Context, fonts *fontCache, request TextFitRequest, size float64) (TextFitResult, error) {
	face, err := fonts.face(size)
	if err != nil {
		return TextFitResult{}, err
	}
	availableWidth := request.Width - request.HorizontalPad*2
	lines, err := WrapText(ctx, face, request.Text, availableWidth)
	if err != nil {
		return TextFitResult{}, err
	}
	metrics := face.Metrics()
	ascent, descent := metrics.Ascent.Ceil(), metrics.Descent.Ceil()
	lineHeight := max(1, (metrics.Ascent + metrics.Descent).Ceil())
	lineStep := max(lineHeight, int(math.Ceil(float64(metrics.Height.Ceil())*request.LineSpacing)))
	if request.SourceLineStep > 0 && request.PreferredFontSize > 0 {
		scaledSourceStep := int(math.Round(request.SourceLineStep * size / request.PreferredFontSize))
		lineStep = max(lineHeight, scaledSourceStep)
	}
	textHeight := 0
	if len(lines) > 0 {
		textHeight = lineHeight + lineStep*max(0, len(lines)-1)
	}
	textWidth := 0
	lineWidths := make([]int, len(lines))
	for index, line := range lines {
		lineWidths[index] = measure(face, line)
		textWidth = max(textWidth, lineWidths[index])
	}
	fits := len(lines) > 0 && textWidth <= availableWidth && textHeight <= request.Height-request.VerticalPad*2
	return TextFitResult{
		FontSize: size, Lines: lines, LineWidths: lineWidths, TextWidth: textWidth, TextHeight: textHeight,
		LineHeight: lineHeight, LineStep: lineStep, Ascent: ascent, Descent: descent,
		Fits: fits, Overflow: !fits,
	}, nil
}

func measure(face font.Face, value string) int {
	if !utf8.ValidString(value) {
		return math.MaxInt
	}
	return font.MeasureString(face, value).Ceil()
}

func roundFontSize(value float64) float64 {
	return math.Round(value/fontSizeStep) * fontSizeStep
}

func roundFontSizeUp(value float64) float64 {
	return math.Ceil(value/fontSizeStep) * fontSizeStep
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
