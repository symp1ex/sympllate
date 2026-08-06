package ocr

import (
	"strings"
	"testing"
)

const tsvHeader = "level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n"

func tsvWord(page, block, paragraph, line, word int, confidence, text string) string {
	return strings.Join([]string{"5", itoa(page), itoa(block), itoa(paragraph), itoa(line), itoa(word), "10", "20", "30", "10", confidence, text}, "\t") + "\n"
}

func itoa(value int) string { return strings.TrimSpace(strings.Repeat(" ", 0) + fmtInt(value)) }
func fmtInt(value int) string {
	digits := ""
	if value == 0 {
		return "0"
	}
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}

func TestParseTSVHandlesCRLFUnicodeEmptyAndConfidence(t *testing.T) {
	input := strings.ReplaceAll(tsvHeader+tsvWord(1, 1, 1, 1, 1, "96.5", "Привет")+tsvWord(1, 1, 1, 1, 2, "-1", "")+tsvWord(1, 1, 1, 1, 3, "10", "noise"), "\n", "\r\n")
	page, err := ParseTSV(strings.NewReader(input), 100, 100, DefaultFilterConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Words) != 3 || !page.Words[0].Accepted || page.Words[1].Accepted || page.Words[2].Accepted {
		t.Fatalf("words=%+v", page.Words)
	}
	if len(page.Paragraphs) != 1 || page.Paragraphs[0].Text != "Привет" || page.Paragraphs[0].ID != "p1-b1-par1" {
		t.Fatalf("paragraphs=%+v", page.Paragraphs)
	}
}

func TestParseTSVSupportsLargeRowsAndSortsWords(t *testing.T) {
	large := strings.Repeat("x", 70_000)
	input := tsvHeader + tsvWord(1, 1, 1, 1, 2, "90", "world") + tsvWord(1, 1, 1, 1, 1, "90", large)
	page, err := ParseTSV(strings.NewReader(input), 100, 100, DefaultFilterConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Words) != 2 || page.Words[0].Word != 1 || len(page.Words[0].Text) != len(large) {
		t.Fatalf("words=%d first=%+v", len(page.Words), page.Words[0])
	}
}

func TestParseTSVRejectsMalformedInput(t *testing.T) {
	cases := map[string]string{
		"missing header": "level\ttext\n5\tx\n",
		"invalid number": tsvHeader + "5\t1\t1\t1\t1\tx\t10\t20\t30\t10\t90\tword\n",
		"damaged row":    tsvHeader + "5\t1\n",
		"outside bounds": tsvHeader + "5\t1\t1\t1\t1\t1\t90\t20\t30\t10\t90\tword\n",
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseTSV(strings.NewReader(input), 100, 100, DefaultFilterConfig()); err == nil {
				t.Fatal("expected parse error")
			}
		})
	}
}

func TestParseTSVGroupsLinesParagraphsBoxesConfidenceAndPunctuation(t *testing.T) {
	input := tsvHeader +
		"5\t1\t2\t3\t2\t2\t50\t30\t20\t10\t80\tworld\n" +
		"5\t1\t2\t3\t1\t2\t40\t10\t5\t10\t100\t,\n" +
		"5\t1\t2\t3\t1\t1\t10\t10\t25\t10\t90\tHello\n" +
		"5\t1\t3\t1\t1\t1\t5\t60\t10\t10\t70\tNext\n"
	page, err := ParseTSV(strings.NewReader(input), 100, 100, DefaultFilterConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Paragraphs) != 2 {
		t.Fatalf("paragraphs=%d", len(page.Paragraphs))
	}
	first := page.Paragraphs[0]
	if first.Text != "Hello,\nworld" || first.Box != (OCRBox{X: 10, Y: 10, Width: 60, Height: 30}) {
		t.Fatalf("first=%+v", first)
	}
	if first.Confidence != 90 || first.Lines[0].ID != "p1-b2-par3-l1" || first.Lines[0].Words[0].ID != "p1-b2-par3-l1-w1" {
		t.Fatalf("confidence/ids=%+v", first)
	}
}

func TestParseTSVEmptyOCR(t *testing.T) {
	page, err := ParseTSV(strings.NewReader(tsvHeader), 10, 10, DefaultFilterConfig())
	if err != nil || len(page.Words) != 0 || len(page.Paragraphs) != 0 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
}
