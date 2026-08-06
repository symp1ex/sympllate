package imagebatch

import (
	"context"
	"math"
	"strings"
	"unicode/utf8"

	"golang.org/x/image/font"
)

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
	for paragraphIndex, paragraph := range paragraphs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		words := strings.Fields(paragraph)
		if len(words) == 0 {
			if paragraphIndex > 0 && paragraphIndex < len(paragraphs)-1 {
				lines = append(lines, "")
			}
			continue
		}
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
	}
	return lines, nil
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

func FitText(ctx context.Context, fonts *fontCache, request TextFitRequest) (TextFitResult, error) {
	if strings.TrimSpace(request.Text) == "" {
		return TextFitResult{}, nil
	}
	if request.Width <= request.HorizontalPad*2 || request.Height <= request.VerticalPad*2 {
		return TextFitResult{FontSize: request.MinFontSize, Overflow: true}, nil
	}
	minimum := max(1, int(math.Ceil(request.MinFontSize)))
	maximum := max(minimum, int(math.Floor(request.MaxFontSize)))
	low, high := minimum, maximum
	var best TextFitResult
	var minimumResult TextFitResult
	for low <= high {
		if err := ctx.Err(); err != nil {
			return TextFitResult{}, err
		}
		candidate := low + (high-low)/2
		result, err := measureFit(ctx, fonts, request, float64(candidate))
		if err != nil {
			return TextFitResult{}, err
		}
		if candidate == minimum {
			minimumResult = result
		}
		if result.Fits {
			best, low = result, candidate+1
		} else {
			high = candidate - 1
		}
	}
	if best.Fits {
		return best, nil
	}
	if minimumResult.FontSize == 0 {
		var err error
		minimumResult, err = measureFit(ctx, fonts, request, float64(minimum))
		if err != nil {
			return TextFitResult{}, err
		}
	}
	minimumResult.Overflow = true
	return minimumResult, nil
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
	lineHeight := max(1, metrics.Height.Ceil())
	textHeight := 0
	if len(lines) > 0 {
		textHeight = lineHeight + int(math.Ceil(float64(lineHeight)*request.LineSpacing))*max(0, len(lines)-1)
	}
	textWidth := 0
	for _, line := range lines {
		textWidth = max(textWidth, measure(face, line))
	}
	fits := len(lines) > 0 && textWidth <= availableWidth && textHeight <= request.Height-request.VerticalPad*2
	return TextFitResult{FontSize: size, Lines: lines, TextWidth: textWidth, TextHeight: textHeight, Fits: fits, Overflow: !fits}, nil
}

func measure(face font.Face, value string) int {
	if !utf8.ValidString(value) {
		return math.MaxInt
	}
	return font.MeasureString(face, value).Ceil()
}
