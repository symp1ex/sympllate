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

func TestPaddleSeparatesHeadingFromBodyAndBoundsUIColumnGroups(t *testing.T) {
	words := []OCRWord{
		testAdaptiveWord("ENGINE OIL", 20, 20, 190, 28, 0, 0, 0, 0),
		testAdaptiveWord("Check the engine oil before starting.", 20, 62, 360, 16, 0, 0, 0, 0),
		testAdaptiveWord("Damage may result from a low level.", 20, 84, 330, 16, 0, 0, 0, 0),
		testAdaptiveWord("Products", 500, 20, 80, 14, 0, 0, 0, 0),
		testAdaptiveWord("Items", 500, 40, 50, 14, 0, 0, 0, 0),
		testAdaptiveWord("Modifiers", 500, 60, 90, 14, 0, 0, 0, 0),
		testAdaptiveWord("Stock list", 500, 80, 85, 14, 0, 0, 0, 0),
	}
	paragraphs := buildPaddleParagraphs(words)
	heading := findAdaptiveParagraphContaining(t, paragraphs, "ENGINE OIL")
	if len(heading.Lines) != 1 {
		t.Fatalf("heading=%+v", heading)
	}
	for _, paragraph := range paragraphs {
		if paragraph.Box.X >= 490 && len(paragraph.Lines) > 2 {
			t.Fatalf("UI column merged: %+v", paragraph)
		}
	}
}

func TestPaddleUIColumnWithSearchInstructionStaysControlLevel(t *testing.T) {
	words := []OCRWord{
		testAdaptiveWord("Enter the text to search by menu.", 120, 20, 190, 25, 0, 0, 0, 0),
		testAdaptiveWord("Favourites", 130, 55, 105, 30, 0, 0, 0, 0),
		testAdaptiveWord("Retail Sales", 130, 90, 90, 30, 0, 0, 0, 0),
		testAdaptiveWord("Inventory Management", 130, 125, 170, 30, 0, 0, 0, 0),
		testAdaptiveWord("Reference books", 150, 160, 105, 26, 0, 0, 0, 0),
		testAdaptiveWord("EAEU commodity code reference book", 150, 191, 250, 26, 0, 0, 0, 0),
		testAdaptiveWord("Products", 150, 222, 80, 26, 0, 0, 0, 0),
	}
	paragraphs := buildPaddleParagraphs(words)
	for _, paragraph := range paragraphs {
		if len(paragraph.Lines) > 2 {
			t.Fatalf("UI navigation column merged: %+v", paragraph)
		}
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

func TestPaddleTaxAndServiceTabsAndControlsRemainIndependent(t *testing.T) {
	words := []OCRWord{
		testAdaptiveWord("How to configure tax and service charge", 30, 20, 410, 18, 0, 0, 0, 0),
		testAdaptiveWord("Open Back Office and select a tax category", 30, 46, 390, 16, 0, 0, 0, 0),
		testAdaptiveWord("Tax categories", 30, 92, 115, 16, 0, 0, 0, 0),
		testAdaptiveWord("Service charges", 175, 92, 135, 16, 0, 0, 0, 0),
		testAdaptiveWord("Search by menu", 350, 92, 125, 16, 0, 0, 0, 0),
		testAdaptiveWord("Name", 30, 132, 55, 15, 0, 0, 0, 0),
		testAdaptiveWord("VAT rate", 220, 132, 72, 15, 0, 0, 0, 0),
		testAdaptiveWord("Enabled", 390, 132, 68, 15, 0, 0, 0, 0),
		testAdaptiveWord("Restaurant tax", 30, 156, 120, 15, 0, 0, 0, 0),
		testAdaptiveWord("20%", 220, 156, 38, 15, 0, 0, 0, 0),
		testAdaptiveWord("Active", 390, 156, 55, 15, 0, 0, 0, 0),
	}
	paragraphs := buildPaddleParagraphs(words)
	for _, labels := range [][]string{{"Tax categories", "Service charges"}, {"Service charges", "Search by menu"}, {"Restaurant tax", "20%"}, {"20%", "Active"}} {
		for _, paragraph := range paragraphs {
			if strings.Contains(paragraph.Text, labels[0]) && strings.Contains(paragraph.Text, labels[1]) {
				t.Fatalf("independent TAX/Service controls merged: labels=%v paragraph=%+v", labels, paragraph)
			}
		}
	}
	prose := findAdaptiveParagraphContaining(t, paragraphs, "How to configure")
	if len(prose.Lines) != 2 {
		t.Fatalf("normal prose grouping regressed: %+v", prose)
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
