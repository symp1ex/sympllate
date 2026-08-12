package ocr

import "testing"

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

func testAdaptiveWord(text string, x, y, width, height, page, block, paragraph, line int) OCRWord {
	return OCRWord{
		Text: text, Confidence: 90, Box: OCRBox{X: x, Y: y, Width: width, Height: height}, Accepted: true,
		Page: page, Block: block, Paragraph: paragraph, Line: line, Word: 1,
	}
}

func adaptiveParagraphLengths(paragraphs [][][]OCRWord) []int {
	result := make([]int, len(paragraphs))
	for index := range paragraphs {
		result[index] = len(paragraphs[index])
	}
	return result
}
