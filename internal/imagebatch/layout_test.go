package imagebatch

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
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
