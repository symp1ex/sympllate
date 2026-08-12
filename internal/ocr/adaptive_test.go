package ocr

import (
	"strings"
	"testing"
)

func TestRebuildOCRPagePreservesTesseractParagraphs(t *testing.T) {
	words := []OCRWord{
		testAdaptiveWord("HEADING", 20, 20, 150, 24, 1, 1, 1, 1),
		testAdaptiveWord("Body", 20, 52, 45, 12, 1, 2, 1, 1),
		testAdaptiveWord("copy", 70, 52, 40, 12, 1, 2, 1, 1),
	}
	page := rebuildOCRPage(words, OCRImageInfo{Width: 400, Height: 200})
	if len(page.Paragraphs) != 2 {
		t.Fatalf("paragraphs=%+v", page.Paragraphs)
	}
	if page.Paragraphs[0].Text != "HEADING" || page.Paragraphs[1].Text != "Body copy" {
		t.Fatalf("paragraph texts=%q, %q", page.Paragraphs[0].Text, page.Paragraphs[1].Text)
	}
}

func TestSpatialParagraphsSeparatesHeadingFromNearbyBody(t *testing.T) {
	lines := [][]OCRWord{
		{testAdaptiveWord("HEADING", 20, 20, 150, 24, 0, 0, 0, 0)},
		{testAdaptiveWord("Body", 20, 52, 100, 12, 0, 0, 0, 0)},
		{testAdaptiveWord("continues", 20, 68, 120, 12, 0, 0, 0, 0)},
	}
	paragraphs := spatialParagraphs(lines)
	if len(paragraphs) != 2 || len(paragraphs[0]) != 1 || len(paragraphs[1]) != 2 {
		t.Fatalf("paragraph line counts=%v", adaptiveParagraphLengths(paragraphs))
	}
}

func TestSpatialParagraphsKeepsColumnsIndependent(t *testing.T) {
	words := []OCRWord{
		testAdaptiveWord("Left", 20, 20, 50, 12, 0, 0, 0, 0),
		testAdaptiveWord("Right", 240, 20, 55, 12, 0, 0, 0, 0),
		testAdaptiveWord("column", 20, 38, 70, 12, 0, 0, 0, 0),
		testAdaptiveWord("column", 240, 38, 70, 12, 0, 0, 0, 0),
	}
	paragraphs := spatialParagraphs(spatialLines(words))
	if len(paragraphs) != 2 {
		t.Fatalf("paragraphs=%d line-counts=%v", len(paragraphs), adaptiveParagraphLengths(paragraphs))
	}
	for _, paragraph := range paragraphs {
		if len(paragraph) != 2 {
			t.Fatalf("paragraph line-counts=%v", adaptiveParagraphLengths(paragraphs))
		}
		first, second := unionWordBoxes(paragraph[0]), unionWordBoxes(paragraph[1])
		if abs(first.X-second.X) > 2 {
			t.Fatalf("mixed columns: first=%+v second=%+v", first, second)
		}
	}
}

func TestSpatialParagraphsDistinguishesLineGapFromParagraphBreak(t *testing.T) {
	lines := [][]OCRWord{
		{testAdaptiveWord("First", 20, 20, 80, 12, 0, 0, 0, 0)},
		{testAdaptiveWord("line", 20, 36, 70, 12, 0, 0, 0, 0)},
		{testAdaptiveWord("New", 20, 60, 75, 12, 0, 0, 0, 0)},
	}
	paragraphs := spatialParagraphs(lines)
	if len(paragraphs) != 2 || len(paragraphs[0]) != 2 || len(paragraphs[1]) != 1 {
		t.Fatalf("paragraph line counts=%v", adaptiveParagraphLengths(paragraphs))
	}
}

func TestPaddleTableCellsRemainSeparateParagraphs(t *testing.T) {
	words := []OCRWord{
		testAdaptiveWord("Torque", 20, 20, 80, 14, 0, 0, 0, 0), testAdaptiveWord("45 Nm", 180, 20, 55, 14, 0, 0, 0, 0),
		testAdaptiveWord("Pressure", 20, 42, 90, 14, 0, 0, 0, 0), testAdaptiveWord("2.5 bar", 180, 42, 65, 14, 0, 0, 0, 0),
	}
	paragraphs := buildPaddleParagraphs(words)
	if len(paragraphs) != 2 {
		t.Fatalf("paragraphs=%+v", paragraphs)
	}
	for _, paragraph := range paragraphs {
		if paragraph.Box.Width > 120 {
			t.Fatalf("table cells merged into wide paragraph: %+v", paragraph)
		}
	}
}

func TestPaddleColumnAwareReadingOrder(t *testing.T) {
	words := []OCRWord{
		testAdaptiveWord("L1", 20, 20, 60, 12, 0, 0, 0, 0), testAdaptiveWord("R1", 240, 20, 60, 12, 0, 0, 0, 0),
		testAdaptiveWord("L2", 20, 40, 60, 12, 0, 0, 0, 0), testAdaptiveWord("R2", 240, 40, 60, 12, 0, 0, 0, 0),
	}
	paragraphs := buildPaddleParagraphs(words)
	if len(paragraphs) != 2 || paragraphs[0].Text != "L1\nL2" || paragraphs[1].Text != "R1\nR2" {
		t.Fatalf("order=%+v", paragraphs)
	}
}

func TestPaddleParagraphsDoNotAttachEmbeddedUILabelToProse(t *testing.T) {
	words := []OCRWord{
		testAdaptiveWord("How to Service Charge and Goverment Tax in Syrve", 113, 117, 902, 18, 0, 0, 0, 0),
		testAdaptiveWord("To allocate sales tax to separate accounts...", 113, 154, 620, 16, 0, 0, 0, 0),
		testAdaptiveWord("You must add a tax category...", 113, 181, 560, 16, 0, 0, 0, 0),
		testAdaptiveWord("Enter the Tax name and the VAT rate and click save.", 113, 208, 690, 16, 0, 0, 0, 0),
		testAdaptiveWord("The resulting tax amount is shown in the final receipt.", 113, 235, 690, 16, 0, 0, 0, 0),
		testAdaptiveWord("Review each configured menu item before saving.", 113, 262, 620, 16, 0, 0, 0, 0),
		testAdaptiveWord("Click Save to apply the tax category.", 113, 286, 540, 18, 0, 0, 0, 0),
		testAdaptiveWord("Back Office", 113, 306, 102, 16, 0, 0, 0, 0),
		testAdaptiveWord("Search by menu", 284, 319, 128, 15, 0, 0, 0, 0),
		testAdaptiveWord("Cash Management", 113, 338, 168, 16, 0, 0, 0, 0),
	}
	paragraphs := buildPaddleParagraphs(words)
	prose := findAdaptiveParagraphContaining(t, paragraphs, "How to Service")
	if strings.Contains(prose.Text, "Search by menu") {
		t.Fatalf("UI label attached to prose paragraph: %+v", prose)
	}
	if prose.Box.Y+prose.Box.Height >= 306 {
		t.Fatalf("prose paragraph swallowed embedded UI area: %+v", prose.Box)
	}
	ui := findAdaptiveParagraphContaining(t, paragraphs, "Search by menu")
	if adaptiveBoxIntersectionArea(prose.Box, ui.Box) > 0 {
		t.Fatalf("prose and UI paragraphs overlap: prose=%+v ui=%+v", prose.Box, ui.Box)
	}
}

func TestPaddleParagraphsKeepNormalRaggedBodyTogether(t *testing.T) {
	words := []OCRWord{
		testAdaptiveWord("Inspect the connector before powering the unit", 40, 40, 360, 14, 0, 0, 0, 0),
		testAdaptiveWord("and verify that the indicator turns green", 40, 60, 310, 14, 0, 0, 0, 0),
		testAdaptiveWord("after the startup sequence completes.", 40, 80, 260, 14, 0, 0, 0, 0),
	}
	paragraphs := buildPaddleParagraphs(words)
	if len(paragraphs) != 1 || len(paragraphs[0].Lines) != 3 {
		t.Fatalf("ragged body split unexpectedly: %+v", paragraphs)
	}
}

func TestPaddleUIGridCellsRemainSeparateParagraphs(t *testing.T) {
	words := []OCRWord{
		testAdaptiveWord("Name", 20, 20, 55, 14, 0, 0, 0, 0), testAdaptiveWord("Value", 140, 20, 62, 14, 0, 0, 0, 0), testAdaptiveWord("Status", 260, 20, 70, 14, 0, 0, 0, 0),
		testAdaptiveWord("Tax", 20, 42, 45, 14, 0, 0, 0, 0), testAdaptiveWord("20%", 140, 42, 42, 14, 0, 0, 0, 0), testAdaptiveWord("Active", 260, 42, 68, 14, 0, 0, 0, 0),
	}
	paragraphs := buildPaddleParagraphs(words)
	if len(paragraphs) != 3 {
		t.Fatalf("grid columns merged unexpectedly: %+v", paragraphs)
	}
	for _, paragraph := range paragraphs {
		if len(paragraph.Lines) != 2 || paragraph.Box.Width > 80 {
			t.Fatalf("grid paragraph shape=%+v", paragraph)
		}
	}
}

func testAdaptiveWord(text string, x, y, width, height, page, block, paragraph, line int) OCRWord {
	return OCRWord{
		Text: text, Confidence: 90, Box: OCRBox{X: x, Y: y, Width: width, Height: height}, Accepted: true,
		Page: page, Block: block, Paragraph: paragraph, Line: line, Word: 1,
	}
}

func findAdaptiveParagraphContaining(t *testing.T, paragraphs []OCRParagraph, text string) OCRParagraph {
	t.Helper()
	for _, paragraph := range paragraphs {
		if strings.Contains(paragraph.Text, text) {
			return paragraph
		}
	}
	t.Fatalf("paragraph containing %q not found in %+v", text, paragraphs)
	return OCRParagraph{}
}

func adaptiveParagraphLengths(paragraphs [][][]OCRWord) []int {
	result := make([]int, len(paragraphs))
	for index := range paragraphs {
		result[index] = len(paragraphs[index])
	}
	return result
}

func adaptiveBoxIntersectionArea(left, right OCRBox) int {
	width := overlapLength(left.X, left.X+left.Width, right.X, right.X+right.Width)
	height := overlapLength(left.Y, left.Y+left.Height, right.Y, right.Y+right.Height)
	return width * height
}
