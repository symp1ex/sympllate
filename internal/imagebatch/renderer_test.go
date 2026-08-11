package imagebatch

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/sympllate/translator/internal/ocr"
)

func TestCleanupChangesOnlyConfirmedBoxesAndPreservesAlpha(t *testing.T) {
	background := color.NRGBA{R: 200, G: 210, B: 220, A: 123}
	source := solidNRGBA(20, 20, background)
	source.SetNRGBA(6, 7, color.NRGBA{R: 10, G: 20, B: 30, A: 111})
	source.SetNRGBA(7, 7, color.NRGBA{R: 10, G: 20, B: 30, A: 111})
	renderer, err := NewRenderer(t.TempDir(), DefaultRenderConfig(), &fakeInpaintEngine{})
	if err != nil {
		t.Fatal(err)
	}
	document := RenderDocument{Blocks: []RenderBlock{{SourceBox: ocr.OCRBox{X: 5, Y: 6, Width: 4, Height: 3}, CleanupBox: ocr.OCRBox{X: 5, Y: 6, Width: 4, Height: 3}, Background: newRenderColor(background), Foreground: newRenderColor(color.NRGBA{R: 10, G: 20, B: 30, A: 111}), CleanupMode: CleanupSolid}}}
	cleaned, document, _, err := renderer.Clean(context.Background(), source, document)
	if err != nil {
		t.Fatal(err)
	}
	if got := cleaned.NRGBAAt(6, 7); got != (color.NRGBA{R: 200, G: 210, B: 220, A: 123}) {
		t.Fatalf("inside=%+v", got)
	}
	if got := cleaned.NRGBAAt(4, 7); got != source.NRGBAAt(4, 7) {
		t.Fatalf("outside changed: %+v", got)
	}
	if got := source.NRGBAAt(6, 7); got != (color.NRGBA{R: 10, G: 20, B: 30, A: 111}) {
		t.Fatalf("source mutated: %+v", got)
	}
}

func TestPrepareSkipsUntranslatedBlockAndDrawsCyrillic(t *testing.T) {
	directory := t.TempDir()
	writeTestFont(t, directory)
	renderer, err := NewRenderer(directory, DefaultRenderConfig(), &fakeInpaintEngine{})
	if err != nil {
		t.Fatal(err)
	}
	defer renderer.Close()
	source := solidNRGBA(300, 120, color.NRGBA{R: 245, G: 245, B: 245, A: 255})
	drawSyntheticWord(source, ocr.OCRBox{X: 10, Y: 10, Width: 120, Height: 40})
	page := ocr.OCRPage{SchemaVersion: 1, Image: ocr.OCRImageInfo{Width: 300, Height: 120}, Paragraphs: []ocr.OCRParagraph{
		{ID: "one", Text: "One", Box: ocr.OCRBox{X: 10, Y: 10, Width: 120, Height: 40}, Lines: []ocr.OCRLine{{Text: "One"}}},
		{ID: "two", Text: "Two", Box: ocr.OCRBox{X: 160, Y: 10, Width: 120, Height: 40}, Lines: []ocr.OCRLine{{Text: "Two"}}},
	}}
	translation := TranslationDocument{SchemaVersion: 1, SourceFile: "page.png", Blocks: []TranslatedBlock{{ID: "one", SourceText: "One", TranslatedText: "Перевод", Status: "translated"}, {ID: "two", SourceText: "Two", Status: "failed"}}}
	document, err := renderer.Prepare(context.Background(), source, page, translation)
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Blocks) != 1 || len(document.SkippedBlocks) != 1 || document.SkippedBlocks[0].ID != "two" {
		t.Fatalf("document=%+v", document)
	}
	preparedBlock := document.Blocks[0]
	if preparedBlock.PreferredFontSize <= 0 || preparedBlock.FontSize > preparedBlock.PreferredFontSize ||
		preparedBlock.MinimumFontSize > preparedBlock.PreferredFontSize || preparedBlock.MaximumFontSize < preparedBlock.PreferredFontSize ||
		len(preparedBlock.LineLayouts) != len(preparedBlock.Lines) {
		t.Fatalf("prepared layout=%+v", preparedBlock)
	}
	cleaned, document, _, err := renderer.Clean(context.Background(), source, document)
	if err != nil {
		t.Fatal(err)
	}
	before := append([]byte(nil), cleaned.Pix...)
	if err := renderer.Draw(context.Background(), cleaned, document); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(before, cleaned.Pix) {
		t.Fatal("renderer did not draw text")
	}
	if got := cleaned.NRGBAAt(170, 20); got != source.NRGBAAt(170, 20) {
		t.Fatalf("skipped block changed: got=%+v want=%+v", got, source.NRGBAAt(170, 20))
	}
}

func TestCleanupRegionsPreferOnePreciseOCRLevel(t *testing.T) {
	transform := CoordinateTransform{SourceWidth: 200, SourceHeight: 100, OCRWidth: 200, OCRHeight: 100, ScaleX: 1, ScaleY: 1}
	wordOne := ocr.OCRWord{Box: ocr.OCRBox{X: 10, Y: 12, Width: 25, Height: 10}, Accepted: true}
	wordTwo := ocr.OCRWord{Box: ocr.OCRBox{X: 40, Y: 12, Width: 30, Height: 10}, Accepted: true}
	paragraph := ocr.OCRParagraph{
		Box:   ocr.OCRBox{X: 8, Y: 8, Width: 90, Height: 20},
		Lines: []ocr.OCRLine{{Box: ocr.OCRBox{X: 9, Y: 10, Width: 65, Height: 14}, Words: []ocr.OCRWord{wordOne, wordTwo}}},
	}
	regions := cleanupRegionsFor(paragraph, transform, 200, 100)
	if len(regions) != 2 || regions[0].Level != "word" || regions[0].Box != wordOne.Box || regions[1].Box != wordTwo.Box {
		t.Fatalf("word regions=%+v", regions)
	}

	paragraph.Lines[0].Words = nil
	regions = cleanupRegionsFor(paragraph, transform, 200, 100)
	if len(regions) != 1 || regions[0].Level != "line" || regions[0].Box != paragraph.Lines[0].Box {
		t.Fatalf("line fallback=%+v", regions)
	}

	paragraph.Lines = nil
	regions = cleanupRegionsFor(paragraph, transform, 200, 100)
	if len(regions) != 1 || regions[0].Level != "paragraph" || regions[0].Box != paragraph.Box {
		t.Fatalf("paragraph fallback=%+v", regions)
	}
}

func TestRendererRejectsUnsupportedScriptsBeforeCleanup(t *testing.T) {
	directory := t.TempDir()
	writeTestFont(t, directory)
	renderer, err := NewRenderer(directory, DefaultRenderConfig(), &fakeInpaintEngine{})
	if err != nil {
		t.Fatal(err)
	}
	supported, err := renderer.supportsText("Latin Кириллица 123")
	if err != nil || !supported {
		t.Fatalf("supported=%v err=%v", supported, err)
	}
	supported, err = renderer.supportsText("中文")
	if err != nil || supported {
		t.Fatalf("supported=%v err=%v", supported, err)
	}
}

func TestPrepareIncludesTypographyAndLayoutDiagnostics(t *testing.T) {
	directory := t.TempDir()
	writeTestFont(t, directory)
	renderer, err := NewRenderer(directory, DefaultRenderConfig(), &fakeInpaintEngine{})
	if err != nil {
		t.Fatal(err)
	}
	defer renderer.Close()
	source := solidNRGBA(420, 180, color.NRGBA{R: 245, G: 245, B: 245, A: 255})
	wordOne := ocr.OCRWord{ID: "p1-b1-par1-l1-w1", Text: "Engine", Accepted: true, Box: ocr.OCRBox{X: 20, Y: 20, Width: 54, Height: 14}}
	wordTwo := ocr.OCRWord{ID: "p1-b1-par1-l1-w2", Text: "oil", Accepted: true, Box: ocr.OCRBox{X: 82, Y: 20, Width: 18, Height: 14}}
	wordThree := ocr.OCRWord{ID: "p1-b1-par1-l2-w1", Text: "level", Accepted: true, Box: ocr.OCRBox{X: 20, Y: 44, Width: 38, Height: 14}}
	for _, word := range []ocr.OCRWord{wordOne, wordTwo, wordThree} {
		drawSyntheticWord(source, word.Box)
	}
	paragraph := ocr.OCRParagraph{
		ID: "p1-b1-par1", Text: "Engine oil\nlevel", Box: ocr.OCRBox{X: 20, Y: 20, Width: 80, Height: 38},
		Lines: []ocr.OCRLine{
			{ID: "p1-b1-par1-l1", Text: "Engine oil", Box: ocr.OCRBox{X: 20, Y: 20, Width: 80, Height: 14}, Words: []ocr.OCRWord{wordOne, wordTwo}},
			{ID: "p1-b1-par1-l2", Text: "level", Box: ocr.OCRBox{X: 20, Y: 44, Width: 38, Height: 14}, Words: []ocr.OCRWord{wordThree}},
		},
	}
	page := ocr.OCRPage{Image: ocr.OCRImageInfo{Width: 420, Height: 180}, Paragraphs: []ocr.OCRParagraph{paragraph}}
	translation := TranslationDocument{Blocks: []TranslatedBlock{{ID: paragraph.ID, TranslatedText: "Уровень моторного масла", Status: "translated"}}}
	document, err := renderer.Prepare(context.Background(), source, page, translation)
	if err != nil || len(document.Blocks) != 1 {
		t.Fatalf("document=%+v err=%v", document, err)
	}
	block := document.Blocks[0]
	if len(block.SourceWords) != 3 || len(block.SourceLines) != 2 || len(block.SourceLineHeights) != 2 || len(block.SourceLineWidths) != 2 || len(block.SourceLineGaps) != 1 {
		t.Fatalf("source diagnostics=%+v", block)
	}
	if len(block.FontEstimate.IndividualEstimates) != 2 || block.FontEstimate.LineStep != 24 || block.TranslatedLineCount != len(block.Lines) {
		t.Fatalf("font/layout diagnostics=%+v", block)
	}
	if block.FontReductionRatio <= 0 || block.ExpansionRatio < 0 || block.LayoutScore < 0 {
		t.Fatalf("layout ratios=%+v", block)
	}
}

func TestAtomicPNGAndJPEGEncodingPreservesDimensionsAndExistingFile(t *testing.T) {
	directory := t.TempDir()
	source := solidNRGBA(32, 24, color.NRGBA{R: 120, G: 130, B: 140, A: 200})
	for _, extension := range []string{".png", ".jpg", ".jpeg"} {
		path := filepath.Join(directory, "output"+extension)
		if err := atomicEncodeGoImage(context.Background(), source, path, extension, 92); err != nil {
			t.Fatal(err)
		}
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		configuration, _, err := image.DecodeConfig(file)
		_ = file.Close()
		if err != nil || configuration.Width != 32 || configuration.Height != 24 {
			t.Fatalf("extension=%s config=%+v err=%v", extension, configuration, err)
		}
	}
	existing := filepath.Join(directory, "existing.png")
	if err := os.WriteFile(existing, []byte("good"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := atomicEncodeGoImage(ctx, source, existing, ".png", 92); err == nil {
		t.Fatal("expected cancellation")
	}
	data, _ := os.ReadFile(existing)
	if string(data) != "good" {
		t.Fatalf("existing file changed: %q", data)
	}
}

func TestPNGCleanupKeepsPixelsOutsideBoxByteEquivalentAfterDecode(t *testing.T) {
	source := solidNRGBA(10, 10, color.NRGBA{R: 30, G: 40, B: 50, A: 60})
	source.SetNRGBA(2, 2, color.NRGBA{A: 60})
	source.SetNRGBA(3, 2, color.NRGBA{A: 60})
	renderer, _ := NewRenderer(t.TempDir(), DefaultRenderConfig(), &fakeInpaintEngine{})
	cleaned, _, _, err := renderer.Clean(context.Background(), source, RenderDocument{Blocks: []RenderBlock{{SourceBox: ocr.OCRBox{X: 2, Y: 2, Width: 2, Height: 2}, CleanupBox: ocr.OCRBox{X: 2, Y: 2, Width: 2, Height: 2}, Background: newRenderColor(color.NRGBA{R: 30, G: 40, B: 50, A: 60}), Foreground: newRenderColor(color.NRGBA{A: 60}), CleanupMode: CleanupSolid}}})
	if err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, cleaned); err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(bytes.NewReader(buffer.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if got := color.NRGBAModel.Convert(decoded.At(0, 0)).(color.NRGBA); got != source.NRGBAAt(0, 0) {
		t.Fatalf("outside=%+v", got)
	}
}
