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
	fontSizeStep         = 0.25
	sourceMetricFontSize = 64.0
	orphanWidthRatio     = 0.45
)

// EstimateSourceFontSize converts OCR ink geometry to the pixel size of the
// bundled font. Line boxes are preferred because paragraph boxes also include
// inter-line gaps. Word boxes and finally a per-line share of the paragraph
// box are deterministic fallbacks for incomplete OCR geometry.
func EstimateSourceFontSize(ctx context.Context, fonts *fontCache, paragraph ocr.OCRParagraph, transform CoordinateTransform, minimum, maximum float64) (float64, error) {
	face, err := fonts.face(sourceMetricFontSize)
	if err != nil {
		return 0, err
	}
	estimates := make([]float64, 0, len(paragraph.Lines))
	for _, line := range paragraph.Lines {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		box := TransformBox(line.Box, transform)
		if estimate, ok := estimateSizeFromInk(face, line.Text, box.Height); ok {
			estimates = append(estimates, estimate)
		}
	}
	if len(estimates) == 0 {
		for _, line := range paragraph.Lines {
			for _, word := range line.Words {
				box := TransformBox(word.Box, transform)
				if estimate, ok := estimateSizeFromInk(face, word.Text, box.Height); ok {
					estimates = append(estimates, estimate)
				}
			}
		}
	}
	if len(estimates) == 0 {
		box := TransformBox(paragraph.Box, transform)
		logicalLines := nonEmptyTextLines(paragraph.Text)
		if len(logicalLines) == 0 {
			logicalLines = []string{paragraph.Text}
		}
		observedLineHeight := float64(box.Height) / float64(max(1, len(logicalLines)))
		for _, line := range logicalLines {
			if estimate, ok := estimateSizeFromInk(face, line, int(math.Round(observedLineHeight))); ok {
				estimates = append(estimates, estimate)
			}
		}
	}
	preferred := minimum
	if len(estimates) > 0 {
		preferred = median(estimates)
	}
	preferred = math.Max(minimum, math.Min(maximum, preferred))
	return roundFontSize(preferred), nil
}

func estimateSizeFromInk(face font.Face, text string, observedHeight int) (float64, bool) {
	text = strings.TrimSpace(text)
	if text == "" || observedHeight <= 0 || !utf8.ValidString(text) {
		return 0, false
	}
	bounds, _ := font.BoundString(face, text)
	inkHeight := (bounds.Max.Y - bounds.Min.Y).Ceil()
	if inkHeight <= 0 {
		return 0, false
	}
	return sourceMetricFontSize * float64(observedHeight) / float64(inkHeight), true
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
