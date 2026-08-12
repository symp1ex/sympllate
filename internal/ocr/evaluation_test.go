package ocr

import (
	"math"
	"testing"
)

func TestEvaluateOCRTextGeometryAndDuplicates(t *testing.T) {
	page := OCRPage{Paragraphs: []OCRParagraph{{Text: "Engine oil capacity 2.3 L"}}, Words: []OCRWord{
		{Text: "Engine oil capacity", Accepted: true, TextAccepted: true, Box: OCRBox{X: 10, Y: 10, Width: 100, Height: 20}},
		{Text: "Engine oil capacity", Accepted: true, TextAccepted: true, Box: OCRBox{X: 11, Y: 10, Width: 100, Height: 20}},
		{Text: "2.3 L", Accepted: true, TextAccepted: true, Box: OCRBox{X: 10, Y: 40, Width: 50, Height: 20}},
	}}
	metrics := EvaluateOCR(page, EvaluationFixture{ExpectedStrings: []string{"Engine oil capacity 2.3 L"}, ExpectedRegions: []OCRBox{{X: 10, Y: 10, Width: 100, Height: 20}, {X: 10, Y: 40, Width: 50, Height: 20}}})
	if metrics.CharacterErrorRate != 0 || metrics.WordRecall != 1 || metrics.RegionRecall != 1 {
		t.Fatalf("metrics=%+v", metrics)
	}
	if math.Abs(metrics.DuplicateRate-1.0/3.0) > 1e-9 || metrics.MedianBBoxIoU < .98 || metrics.CoordinateErrorP95 != 0 {
		t.Fatalf("metrics=%+v", metrics)
	}
}

func TestEvaluateOCRReportsMissedTextAndRegion(t *testing.T) {
	page := OCRPage{Paragraphs: []OCRParagraph{{Text: "Engine oil"}}, Words: []OCRWord{{Text: "Engine oil", Accepted: true, Box: OCRBox{X: 100, Y: 100, Width: 40, Height: 10}}}}
	metrics := EvaluateOCR(page, EvaluationFixture{ExpectedStrings: []string{"Engine oil capacity"}, ExpectedRegions: []OCRBox{{X: 10, Y: 10, Width: 40, Height: 10}}})
	if metrics.CharacterErrorRate <= 0 || metrics.WordRecall >= 1 || metrics.RegionRecall != 0 || !math.IsInf(metrics.CoordinateErrorP95, 1) {
		t.Fatalf("metrics=%+v", metrics)
	}
}
