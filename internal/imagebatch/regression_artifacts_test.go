package imagebatch

import (
	"context"
	"image"
	"image/color"
	"image/draw"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sympllate/translator/internal/ocr"
	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

// TestWriteTypographyRegressionArtifacts produces review artifacts only when
// explicitly requested. Normal test runs remain hermetic and do not mutate the
// repository.
func TestWriteTypographyRegressionArtifacts(t *testing.T) {
	outputDirectory := os.Getenv("SYMPLLATE_WRITE_TYPOGRAPHY_FIXTURES")
	if outputDirectory == "" {
		t.Skip("set SYMPLLATE_WRITE_TYPOGRAPHY_FIXTURES to write review artifacts")
	}
	if err := os.MkdirAll(outputDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writeTestFont(t, directory)
	renderer, err := NewRenderer(directory, DefaultRenderConfig(), &fakeInpaintEngine{})
	if err != nil {
		t.Fatal(err)
	}
	defer renderer.Close()

	source := solidNRGBA(640, 360, color.NRGBA{R: 248, G: 248, B: 246, A: 255})
	page := typographyFixturePage(t, renderer.fonts, source)
	translation := TranslationDocument{SourceFile: "typography-synthetic.png", Blocks: []TranslatedBlock{
		{ID: "title", TranslatedText: "ТЕХНИЧЕСКОЕ ОБСЛУЖИВАНИЕ", Status: "translated"},
		{ID: "left-heading", TranslatedText: "ЛЕВЫЙ ЗАГОЛОВОК", Status: "translated"},
		{ID: "left-body", TranslatedText: "Перед запуском проверьте уровень моторного масла и состояние фильтра.", Status: "translated"},
		{ID: "right-body", TranslatedText: "Затяните крепёж и убедитесь, что защитная крышка установлена правильно.", Status: "translated"},
		{ID: "table", TranslatedText: "ДАВЛЕНИЕ МАСЛА 240 кПа", Status: "translated"},
	}}
	document, err := renderer.Prepare(context.Background(), source, page, translation)
	if err != nil {
		t.Fatal(err)
	}
	cleaned := cloneNRGBA(source)
	eraseFixtureParagraphs(cleaned, page)
	if err := renderer.Draw(context.Background(), cleaned, document); err != nil {
		t.Fatal(err)
	}
	legacy, err := legacyTypographyDocument(renderer, page, translation)
	if err != nil {
		t.Fatal(err)
	}
	legacyRendered := cloneNRGBA(source)
	eraseFixtureParagraphs(legacyRendered, page)
	if err := renderer.Draw(context.Background(), legacyRendered, legacy); err != nil {
		t.Fatal(err)
	}
	if err := atomicEncodeGoImage(context.Background(), source, filepath.Join(outputDirectory, "typography.original.png"), ".png", 92); err != nil {
		t.Fatal(err)
	}
	if err := writeLayoutDebug(context.Background(), legacyRendered, legacy, filepath.Join(outputDirectory, "typography.legacy-layout.png"), 92); err != nil {
		t.Fatal(err)
	}
	if err := writeLayoutDebug(context.Background(), cleaned, document, filepath.Join(outputDirectory, "typography.new-layout.png"), 92); err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteJSON(filepath.Join(outputDirectory, "typography.new-render.json"), document); err != nil {
		t.Fatal(err)
	}
}

func eraseFixtureParagraphs(target *image.NRGBA, page ocr.OCRPage) {
	for _, paragraph := range page.Paragraphs {
		box := paragraph.Box
		draw.Draw(target, image.Rect(box.X-2, box.Y-2, box.X+box.Width+2, box.Y+box.Height+2), image.NewUniform(color.NRGBA{R: 248, G: 248, B: 246, A: 255}), image.Point{}, draw.Src)
	}
}

func typographyFixturePage(t *testing.T, fonts *fontCache, target *image.NRGBA) ocr.OCRPage {
	t.Helper()
	paragraphs := []ocr.OCRParagraph{
		drawFixtureParagraph(t, fonts, target, "title", []string{"MAINTENANCE MANUAL"}, 24, 190, 18, 0),
		drawFixtureParagraph(t, fonts, target, "left-heading", []string{"ENGINE CHECK"}, 18, 28, 66, 0),
		drawFixtureParagraph(t, fonts, target, "left-body", []string{"Check engine oil level", "before starting the unit", "and inspect the filter"}, 14, 28, 105, 22),
		drawFixtureParagraph(t, fonts, target, "right-body", []string{"Tighten all fasteners", "Install the safety cover", "before normal operation"}, 14, 350, 105, 22),
		drawFixtureParagraph(t, fonts, target, "table", []string{"OIL PRESSURE 240 kPa", "MAXIMUM 300 kPa"}, 12, 350, 250, 19),
	}
	return ocr.OCRPage{SchemaVersion: 1, Image: ocr.OCRImageInfo{Width: 640, Height: 360, MediaType: "image/png"}, Paragraphs: paragraphs}
}

func drawFixtureParagraph(t *testing.T, fonts *fontCache, target *image.NRGBA, id string, lines []string, size float64, x, y, step int) ocr.OCRParagraph {
	t.Helper()
	face, err := fonts.face(size)
	if err != nil {
		t.Fatal(err)
	}
	result := ocr.OCRParagraph{ID: id, Text: strings.Join(lines, "\n"), Page: 1, Block: 1, Paragraph: 1}
	drawer := font.Drawer{Dst: target, Src: image.NewUniform(color.NRGBA{R: 32, G: 34, B: 38, A: 255}), Face: face}
	for index, text := range lines {
		bounds, _ := font.BoundString(face, text)
		width, height := (bounds.Max.X - bounds.Min.X).Ceil(), (bounds.Max.Y - bounds.Min.Y).Ceil()
		lineY := y
		if step > 0 {
			lineY += index * step
		}
		box := ocr.OCRBox{X: x, Y: lineY, Width: width, Height: height}
		drawer.Dot = fixed.P(x-bounds.Min.X.Ceil(), lineY-bounds.Min.Y.Ceil())
		drawer.DrawString(text)
		word := ocr.OCRWord{ID: id + "-w", Text: text, Confidence: 95, Box: box, Accepted: true, Page: 1, Block: 1, Paragraph: 1, Line: index + 1, Word: 1}
		result.Lines = append(result.Lines, ocr.OCRLine{ID: id + "-l", Text: text, Confidence: 95, Box: box, Words: []ocr.OCRWord{word}, Page: 1, Block: 1, Paragraph: 1, Line: index + 1})
	}
	result.Box = unionLayoutBoxes(func() []ocr.OCRBox {
		boxes := make([]ocr.OCRBox, len(result.Lines))
		for index := range result.Lines {
			boxes[index] = result.Lines[index].Box
		}
		return boxes
	}())
	return result
}

func legacyTypographyDocument(renderer *Renderer, page ocr.OCRPage, translation TranslationDocument) (RenderDocument, error) {
	translated := make(map[string]string, len(translation.Blocks))
	for _, block := range translation.Blocks {
		translated[block.ID] = block.TranslatedText
	}
	result := RenderDocument{ImageWidth: 640, ImageHeight: 360}
	for _, paragraph := range page.Paragraphs {
		base := paragraph.Box
		box := ocr.OCRBox{X: max(0, base.X-base.Width/3), Y: base.Y, Width: min(640, base.Width+base.Width*2/3), Height: min(360-base.Y, base.Height*2)}
		alignment := "left"
		if len(paragraph.Lines) == 1 && len([]rune(translated[paragraph.ID])) <= 40 {
			alignment = "center"
		}
		preferred := math.Min(48, math.Max(10, float64(base.Height)*1.45))
		fit, err := FitText(context.Background(), renderer.fonts, TextFitRequest{
			Text: translated[paragraph.ID], Width: box.Width, Height: box.Height,
			MinFontSize: 10, MaxFontSize: preferred, PreferredFontSize: preferred,
			LineSpacing: 1.15, HorizontalPad: 2, VerticalPad: 2,
		})
		if err != nil {
			return RenderDocument{}, err
		}
		words, lines, _, _, _ := sourceLayoutDiagnostics(paragraph, CoordinateTransform{ScaleX: 1, ScaleY: 1})
		result.Blocks = append(result.Blocks, RenderBlock{
			ID: paragraph.ID, SourceBox: base, TextBox: box, SourceWords: words, SourceLines: lines,
			PreferredFontSize: preferred, FontSize: fit.FontSize, Lines: fit.Lines,
			LineLayouts: positionTextLines(fit, box, alignment, "top", 2, 2),
			Alignment:   alignment, BoxExpanded: true, LayoutScore: 0, Foreground: newRenderColor(color.NRGBA{R: 32, G: 34, B: 38, A: 255}),
		})
	}
	return result, nil
}
