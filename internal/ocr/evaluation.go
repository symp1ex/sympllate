package ocr

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

type EvaluationFixture struct {
	ExpectedStrings []string
	ExpectedRegions []OCRBox
}

type EvaluationMetrics struct {
	CharacterErrorRate     float64 `json:"characterErrorRate"`
	NormalizedEditDistance float64 `json:"normalizedEditDistance"`
	WordRecall             float64 `json:"wordRecall"`
	RegionRecall           float64 `json:"regionRecall"`
	DuplicateRate          float64 `json:"duplicateRate"`
	MedianBBoxIoU          float64 `json:"medianBBoxIoU"`
	CoordinateErrorP90     float64 `json:"coordinateErrorP90"`
	CoordinateErrorP95     float64 `json:"coordinateErrorP95"`
}

// EvaluateOCR is an offline regression helper. It measures text and geometry
// together so detector box count cannot be optimized at the expense of OCR
// accuracy or duplicate suppression.
func EvaluateOCR(page OCRPage, fixture EvaluationFixture) EvaluationMetrics {
	expectedText := normalizeEvaluationText(strings.Join(fixture.ExpectedStrings, " "))
	actualText := normalizeEvaluationText(plainText(page))
	distance := levenshteinRunes([]rune(expectedText), []rune(actualText))
	denominator := maximum(1, len([]rune(expectedText)))
	metrics := EvaluationMetrics{CharacterErrorRate: float64(distance) / float64(denominator)}
	metrics.NormalizedEditDistance = metrics.CharacterErrorRate
	metrics.WordRecall = recalledWords(evaluationWords(expectedText), evaluationWords(actualText))
	metrics.DuplicateRate = duplicateWordRate(page.Words)
	metrics.RegionRecall, metrics.MedianBBoxIoU, metrics.CoordinateErrorP90, metrics.CoordinateErrorP95 = evaluateRegions(page.Words, fixture.ExpectedRegions)
	return metrics
}

func normalizeEvaluationText(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func evaluationWords(value string) []string {
	return strings.FieldsFunc(value, func(character rune) bool { return !unicode.IsLetter(character) && !unicode.IsNumber(character) })
}

func recalledWords(expected, actual []string) float64 {
	if len(expected) == 0 {
		return 1
	}
	counts := make(map[string]int, len(actual))
	for _, word := range actual {
		counts[word]++
	}
	recalled := 0
	for _, word := range expected {
		if counts[word] > 0 {
			recalled++
			counts[word]--
		}
	}
	return float64(recalled) / float64(len(expected))
}

func duplicateWordRate(words []OCRWord) float64 {
	accepted := make([]OCRWord, 0, len(words))
	for _, word := range words {
		if word.TextAccepted || word.Accepted {
			accepted = append(accepted, word)
		}
	}
	if len(accepted) == 0 {
		return 0
	}
	duplicates := 0
	for index, word := range accepted {
		for previous := 0; previous < index; previous++ {
			if normalizedOCRText(word.Text) == normalizedOCRText(accepted[previous].Text) && overlapOverSmaller(word.Box, accepted[previous].Box) >= .30 {
				duplicates++
				break
			}
		}
	}
	return float64(duplicates) / float64(len(accepted))
}

func evaluateRegions(words []OCRWord, expected []OCRBox) (float64, float64, float64, float64) {
	if len(expected) == 0 {
		return 1, 1, 0, 0
	}
	used := make([]bool, len(words))
	ious, errors := make([]float64, 0, len(expected)), make([]float64, 0, len(expected))
	for _, wanted := range expected {
		best, bestIoU := -1, 0.0
		for index, word := range words {
			if used[index] || (!word.Accepted && !word.TextAccepted) {
				continue
			}
			if value := boxIoU(wanted, word.Box); value > bestIoU {
				best, bestIoU = index, value
			}
		}
		if best < 0 || bestIoU < .25 {
			continue
		}
		used[best] = true
		ious = append(ious, bestIoU)
		errors = append(errors, boxCoordinateError(wanted, words[best].Box))
	}
	recall := float64(len(ious)) / float64(len(expected))
	if len(ious) == 0 {
		return recall, 0, math.Inf(1), math.Inf(1)
	}
	sort.Float64s(ious)
	sort.Float64s(errors)
	return recall, percentile(ious, .5), percentile(errors, .90), percentile(errors, .95)
}

func boxIoU(left, right OCRBox) float64 {
	intersection := overlapLength(left.X, left.X+left.Width, right.X, right.X+right.Width) * overlapLength(left.Y, left.Y+left.Height, right.Y, right.Y+right.Height)
	union := left.Width*left.Height + right.Width*right.Height - intersection
	if union <= 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func boxCoordinateError(expected, actual OCRBox) float64 {
	values := []float64{math.Abs(float64(expected.X - actual.X)), math.Abs(float64(expected.Y - actual.Y)), math.Abs(float64(expected.X + expected.Width - actual.X - actual.Width)), math.Abs(float64(expected.Y + expected.Height - actual.Y - actual.Height))}
	sort.Float64s(values)
	return values[len(values)-1]
}

func percentile(values []float64, quantile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	index := int(math.Ceil(quantile*float64(len(values)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(values) {
		index = len(values) - 1
	}
	return values[index]
}

func levenshteinRunes(left, right []rune) int {
	previous := make([]int, len(right)+1)
	for index := range previous {
		previous[index] = index
	}
	for leftIndex, leftRune := range left {
		current := make([]int, len(right)+1)
		current[0] = leftIndex + 1
		for rightIndex, rightRune := range right {
			cost := 1
			if leftRune == rightRune {
				cost = 0
			}
			current[rightIndex+1] = minimum(minimum(current[rightIndex]+1, previous[rightIndex+1]+1), previous[rightIndex]+cost)
		}
		previous = current
	}
	return previous[len(right)]
}
