package imagebatch

import (
	"context"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/sympllate/translator/internal/ocr"
	"golang.org/x/image/font"
)

func TestWrapTextPreservesUnicodeNewlinesAndLongWords(t *testing.T) {
	directory := t.TempDir()
	writeTestFont(t, directory)
	fonts := newFontCache(directory)
	defer fonts.close()
	face, err := fonts.face(14)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := WrapText(context.Background(), face, "Короткая строка\r\nURL:https://example.com/очень-длинный-путь", 90)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) < 3 {
		t.Fatalf("lines=%v", lines)
	}
	joined := strings.ReplaceAll(strings.Join(lines, ""), " ", "")
	want := strings.ReplaceAll("Короткая строкаURL:https://example.com/очень-длинный-путь", " ", "")
	if joined != want || !utf8.ValidString(joined) {
		t.Fatalf("joined=%q want=%q lines=%v", joined, want, lines)
	}
	if empty, err := WrapText(context.Background(), face, "  ", 100); err != nil || len(empty) != 0 {
		t.Fatalf("empty=%v err=%v", empty, err)
	}
}

func TestFitTextChoosesLargestDeterministicSizeAndReportsOverflow(t *testing.T) {
	directory := t.TempDir()
	writeTestFont(t, directory)
	fonts := newFontCache(directory)
	defer fonts.close()
	request := TextFitRequest{Text: "Несколько слов для переноса", Width: 180, Height: 70, MinFontSize: 10, MaxFontSize: 30, LineSpacing: 1.15, HorizontalPad: 2, VerticalPad: 2}
	first, err := FitText(context.Background(), fonts, request)
	if err != nil || !first.Fits || first.FontSize < request.MinFontSize || first.FontSize > request.MaxFontSize {
		t.Fatalf("fit=%+v err=%v", first, err)
	}
	second, err := FitText(context.Background(), fonts, request)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("first=%+v second=%+v err=%v", first, second, err)
	}
	request.Width, request.Height = 2, 2
	overflow, err := FitText(context.Background(), fonts, request)
	if err != nil || overflow.Fits || !overflow.Overflow {
		t.Fatalf("overflow=%+v err=%v", overflow, err)
	}
}

func TestLayoutCancellationAndMissingFont(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fonts := newFontCache(t.TempDir())
	if _, err := FitText(ctx, fonts, TextFitRequest{Text: "text", Width: 100, Height: 20, MinFontSize: 10, MaxFontSize: 12, LineSpacing: 1}); err == nil {
		t.Fatal("expected cancellation")
	}
	ctx = context.Background()
	_, err := FitText(ctx, fonts, TextFitRequest{Text: "text", Width: 100, Height: 20, MinFontSize: 10, MaxFontSize: 12, LineSpacing: 1})
	if err == nil || !strings.Contains(err.Error(), filepath.Join("bin", "fonts", "regular.ttf")) {
		t.Fatalf("err=%v", err)
	}
}

func TestEstimateSourceFontSizeUsesLineGeometry(t *testing.T) {
	directory := t.TempDir()
	writeTestFont(t, directory)
	fonts := newFontCache(directory)
	defer fonts.close()
	transform, err := NewCoordinateTransform(600, 400, 600, 400)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("paragraph and oversized paragraph box", func(t *testing.T) {
		paragraph := layoutTestParagraph(t, fonts, "body", []string{"First body line", "Second body line", "Third body line"}, 14, 20, 20, 260)
		paragraph.Box.Height = 180
		preferred, err := EstimateSourceFontSize(context.Background(), fonts, paragraph, transform, 8, 48)
		if err != nil {
			t.Fatal(err)
		}
		if math.Abs(preferred-14) > 0.5 {
			t.Fatalf("preferred=%v, want approximately 14", preferred)
		}
	})

	t.Run("equal UI labels", func(t *testing.T) {
		labels := []string{"Products", "Items", "Modifiers", "Stock list"}
		values := make([]float64, 0, len(labels))
		for index, label := range labels {
			paragraph := layoutTestParagraph(t, fonts, label, []string{label}, 12, 20, 20+index*30, 120)
			preferred, err := EstimateSourceFontSize(context.Background(), fonts, paragraph, transform, 8, 48)
			if err != nil {
				t.Fatal(err)
			}
			values = append(values, preferred)
		}
		for _, value := range values[1:] {
			if math.Abs(value-values[0]) > 0.5 {
				t.Fatalf("label sizes are not stable: %v", values)
			}
		}
	})

	t.Run("word and paragraph fallbacks", func(t *testing.T) {
		wordHeight := sourceInkHeight(t, fonts, 12, "Fallback")
		withWord := ocr.OCRParagraph{
			ID: "word", Text: "Fallback", Box: ocr.OCRBox{X: 20, Y: 20, Width: 120, Height: wordHeight},
			Lines: []ocr.OCRLine{{Text: "Fallback", Words: []ocr.OCRWord{{Text: "Fallback", Box: ocr.OCRBox{X: 20, Y: 20, Width: 80, Height: wordHeight}}}}},
		}
		wordSize, err := EstimateSourceFontSize(context.Background(), fonts, withWord, transform, 8, 48)
		if err != nil || math.Abs(wordSize-12) > 0.5 {
			t.Fatalf("word fallback size=%v err=%v", wordSize, err)
		}
		paragraphOnly := ocr.OCRParagraph{
			ID: "paragraph", Text: "Body\nBody",
			Box: ocr.OCRBox{X: 20, Y: 20, Width: 120, Height: sourceInkHeight(t, fonts, 12, "Body") * 2},
		}
		paragraphSize, err := EstimateSourceFontSize(context.Background(), fonts, paragraphOnly, transform, 8, 48)
		if err != nil || math.Abs(paragraphSize-12) > 0.5 {
			t.Fatalf("paragraph fallback size=%v err=%v", paragraphSize, err)
		}
	})

	t.Run("heading hierarchy", func(t *testing.T) {
		heading := layoutTestParagraph(t, fonts, "heading", []string{"Heading"}, 26, 20, 20, 240)
		body := layoutTestParagraph(t, fonts, "body", []string{"Body text"}, 12, 20, 80, 240)
		headingSize, err := EstimateSourceFontSize(context.Background(), fonts, heading, transform, 8, 48)
		if err != nil {
			t.Fatal(err)
		}
		bodySize, err := EstimateSourceFontSize(context.Background(), fonts, body, transform, 8, 48)
		if err != nil {
			t.Fatal(err)
		}
		if headingSize <= bodySize*1.7 {
			t.Fatalf("heading=%v body=%v", headingSize, bodySize)
		}
	})
}

func TestPreferredFirstFitting(t *testing.T) {
	directory := t.TempDir()
	writeTestFont(t, directory)
	fonts := newFontCache(directory)
	defer fonts.close()
	renderer := &Renderer{config: DefaultRenderConfig(), fonts: fonts}

	t.Run("same size", func(t *testing.T) {
		base := ocr.OCRBox{X: 20, Y: 20, Width: 120, Height: sourceInkHeight(t, fonts, 12, "Products")}
		_, fit, err := renderer.fitBlock(context.Background(), "Продукты", 12, 1, base, []ocr.OCRBox{base}, 0, nil, 400, 200)
		if err != nil || !fit.Fits {
			t.Fatalf("fit=%+v err=%v", fit, err)
		}
		if math.Abs(fit.FontSize-12) > fontSizeStep || fit.FontSize > fit.PreferredFontSize {
			t.Fatalf("fit=%+v", fit)
		}
	})

	t.Run("longer translation wraps before shrinking", func(t *testing.T) {
		base := ocr.OCRBox{X: 20, Y: 20, Width: 92, Height: sourceInkHeight(t, fonts, 12, "Tax category")}
		box, fit, err := renderer.fitBlock(context.Background(), "Категория налога", 12, 1, base, []ocr.OCRBox{base}, 0, nil, 400, 200)
		if err != nil || !fit.Fits {
			t.Fatalf("box=%+v fit=%+v err=%v", box, fit, err)
		}
		if fit.FontSize != 12 || len(fit.Lines) < 2 || !fit.BoxExpanded || fit.FontReduced {
			t.Fatalf("box=%+v fit=%+v", box, fit)
		}
	})

	t.Run("very long translation shrinks within preferred range", func(t *testing.T) {
		request := TextFitRequest{
			Text: "Очень длинный перевод технического описания", Width: 180, Height: 55,
			MinFontSize: 14, MaxFontSize: 21, PreferredFontSize: 20,
			LineSpacing: 1.15, HorizontalPad: 2, VerticalPad: 2,
		}
		fit, err := FitText(context.Background(), fonts, request)
		if err != nil || !fit.Fits {
			t.Fatalf("fit=%+v err=%v", fit, err)
		}
		if fit.FontSize >= 20 || fit.FontSize < 14 || !fit.FontReduced {
			t.Fatalf("fit=%+v", fit)
		}
	})

	t.Run("emergency shrink is diagnosed", func(t *testing.T) {
		base := ocr.OCRBox{X: 20, Y: 20, Width: 180, Height: 40}
		blocker := ocr.OCRBox{X: 10, Y: 62, Width: 220, Height: 30}
		_, fit, err := renderer.fitBlock(context.Background(), "Очень длинный перевод технического описания", 20, 1, base, []ocr.OCRBox{base, blocker}, 0, nil, 400, 200)
		if err != nil || !fit.Fits {
			t.Fatalf("fit=%+v err=%v", fit, err)
		}
		if !fit.EmergencyShrink || fit.FontSize >= 14 || fit.FallbackReason != "emergency_font_reduction" {
			t.Fatalf("fit=%+v", fit)
		}
	})
}

func TestWrapTextBalancesExplicitLinesAndLongRussianWord(t *testing.T) {
	directory := t.TempDir()
	writeTestFont(t, directory)
	fonts := newFontCache(directory)
	defer fonts.close()
	face, err := fonts.face(14)
	if err != nil {
		t.Fatal(err)
	}
	lines, err := WrapText(context.Background(), face, "Первая строка\nВторая строка", 500)
	if err != nil || !reflect.DeepEqual(lines, []string{"Первая строка", "Вторая строка"}) {
		t.Fatalf("lines=%v err=%v", lines, err)
	}
	lines, err = WrapText(context.Background(), face, "Привет ,   мир !", 500)
	if err != nil || !reflect.DeepEqual(lines, []string{"Привет, мир!"}) {
		t.Fatalf("punctuation lines=%v err=%v", lines, err)
	}
	balancedText := "Очень длинная строка переведённого текста"
	lines, err = WrapText(context.Background(), face, balancedText, measure(face, "Очень длинная строка переведённого"))
	if err != nil || !reflect.DeepEqual(lines, []string{"Очень длинная строка", "переведённого текста"}) {
		t.Fatalf("balanced lines=%v err=%v", lines, err)
	}
	word := "рентгеноэлектрокардиографический"
	lines, err = WrapText(context.Background(), face, word, 45)
	if err != nil || len(lines) < 2 || strings.Join(lines, "") != word {
		t.Fatalf("lines=%v err=%v", lines, err)
	}
	for _, line := range lines {
		if line == "" || measure(face, line) > 45 && utf8.RuneCountInString(line) > 1 {
			t.Fatalf("invalid long-word geometry: %v", lines)
		}
	}
}

func TestFitTextTinyBoxIsDeterministic(t *testing.T) {
	directory := t.TempDir()
	writeTestFont(t, directory)
	fonts := newFontCache(directory)
	defer fonts.close()
	request := TextFitRequest{Text: "Текст", Width: 3, Height: 2, MinFontSize: 10, MaxFontSize: 12, PreferredFontSize: 12, LineSpacing: 1.15, HorizontalPad: 2, VerticalPad: 2}
	first, err := FitText(context.Background(), fonts, request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := FitText(context.Background(), fonts, request)
	if err != nil || !reflect.DeepEqual(first, second) || first.Fits || !first.Overflow {
		t.Fatalf("first=%+v second=%+v err=%v", first, second, err)
	}
}

func layoutTestParagraph(t *testing.T, fonts *fontCache, id string, texts []string, size float64, x, y, width int) ocr.OCRParagraph {
	t.Helper()
	lines := make([]ocr.OCRLine, 0, len(texts))
	currentY := y
	for index, text := range texts {
		height := sourceInkHeight(t, fonts, size, text)
		box := ocr.OCRBox{X: x, Y: currentY, Width: width, Height: height}
		lines = append(lines, ocr.OCRLine{ID: id, Text: text, Box: box, Line: index + 1})
		currentY += height + 5
	}
	paragraphHeight := 0
	if len(lines) > 0 {
		last := lines[len(lines)-1].Box
		paragraphHeight = last.Y + last.Height - y
	}
	return ocr.OCRParagraph{ID: id, Text: strings.Join(texts, "\n"), Box: ocr.OCRBox{X: x, Y: y, Width: width, Height: paragraphHeight}, Lines: lines}
}

func sourceInkHeight(t *testing.T, fonts *fontCache, size float64, text string) int {
	t.Helper()
	face, err := fonts.face(size)
	if err != nil {
		t.Fatal(err)
	}
	bounds, _ := font.BoundString(face, text)
	return max(1, (bounds.Max.Y - bounds.Min.Y).Ceil())
}
