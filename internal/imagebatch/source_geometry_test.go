package imagebatch

import (
	"context"
	"image/color"
	"testing"

	"github.com/sympllate/translator/internal/ocr"
)

func TestPrepareKeepsEmptySpaceParagraphAABBOverlap(t *testing.T) {
	paragraphs := []ocr.OCRParagraph{
		geometryParagraph("a", 95, []ocr.OCRBox{{X: 10, Y: 10, Width: 220, Height: 18}, {X: 10, Y: 72, Width: 220, Height: 18}}),
		geometryParagraph("b", 94, []ocr.OCRBox{{X: 90, Y: 40, Width: 80, Height: 18}}),
	}
	document := prepareGeometryDocument(t, paragraphs, []TranslatedBlock{
		{ID: "a", TranslatedText: "First group", Status: "translated"},
		{ID: "b", TranslatedText: "Middle label", Status: "translated"},
	})
	if len(document.Blocks) != 2 || len(document.SkippedBlocks) != 0 {
		t.Fatalf("rendered=%d skipped=%+v", len(document.Blocks), document.SkippedBlocks)
	}
	if len(document.Collisions) != 1 || document.Collisions[0].CollisionClass != "aabb_only" || document.Collisions[0].Decision != "kept_both" {
		t.Fatalf("collisions=%+v", document.Collisions)
	}
}

func TestPrepareKeepsSemanticallyIndependentOverlappingText(t *testing.T) {
	paragraphs := []ocr.OCRParagraph{
		geometryParagraph("preferred", 98, []ocr.OCRBox{{X: 20, Y: 20, Width: 120, Height: 24}}),
		geometryParagraph("lower", 72, []ocr.OCRBox{{X: 35, Y: 22, Width: 110, Height: 24}}),
	}
	document := prepareGeometryDocument(t, paragraphs, []TranslatedBlock{
		{ID: "preferred", TranslatedText: "Primary", Status: "translated"},
		{ID: "lower", TranslatedText: "Secondary", Status: "translated"},
	})
	if len(document.Blocks) != 2 || len(document.SkippedBlocks) != 0 {
		t.Fatalf("blocks=%+v skipped=%+v", document.Blocks, document.SkippedBlocks)
	}
	if len(document.Collisions) != 1 || document.Collisions[0].CollisionClass != "ambiguous_overlap" || document.Collisions[0].Decision != "kept_both" {
		t.Fatalf("collisions=%+v", document.Collisions)
	}
}

func TestPrepareKeepsDuplicateWinnerDeterministically(t *testing.T) {
	paragraphs := []ocr.OCRParagraph{
		geometryParagraph("weak", 70, []ocr.OCRBox{{X: 20, Y: 20, Width: 120, Height: 24}}),
		geometryParagraph("strong", 96, []ocr.OCRBox{{X: 21, Y: 20, Width: 120, Height: 24}}),
	}
	paragraphs[0].Text, paragraphs[1].Text = "Duplicate text", "Duplicate text"
	paragraphs[0].Lines[0].Text, paragraphs[1].Lines[0].Text = "Duplicate text", "Duplicate text"
	paragraphs[0].Lines[0].Words[0].Text, paragraphs[1].Lines[0].Words[0].Text = "Duplicate text", "Duplicate text"
	document := prepareGeometryDocument(t, paragraphs, []TranslatedBlock{
		{ID: "weak", TranslatedText: "Weak", Status: "translated"},
		{ID: "strong", TranslatedText: "Strong", Status: "translated"},
	})
	if len(document.Blocks) != 1 || document.Blocks[0].ID != "strong" || len(document.SkippedBlocks) != 0 || len(document.DeduplicatedBlocks) != 1 || document.DeduplicatedBlocks[0].DuplicateOf != "strong" {
		t.Fatalf("blocks=%+v skipped=%+v", document.Blocks, document.SkippedBlocks)
	}
}

func TestContainedFragmentCannotBeatCompleteParagraphOnConfidence(t *testing.T) {
	large := geometryParagraph("paragraph", 99.33, []ocr.OCRBox{{X: 10, Y: 10, Width: 280, Height: 22}})
	large.Text = "the engine serial number equally qualified service facility"
	large.Lines[0].Text = large.Text
	large.Lines[0].Words[0].Text = large.Text
	fragment := geometryParagraph("fragment", 99.99, []ocr.OCRBox{{X: 130, Y: 10, Width: 48, Height: 22}})
	fragment.Text, fragment.Lines[0].Text, fragment.Lines[0].Words[0].Text = "equally", "equally", "equally"
	document := prepareGeometryDocument(t, []ocr.OCRParagraph{large, fragment}, []TranslatedBlock{
		{ID: "paragraph", TranslatedText: "полный перевод абзаца", Status: "translated"},
		{ID: "fragment", TranslatedText: "равно", Status: "translated"},
	})
	if len(document.Blocks) != 1 || document.Blocks[0].ID != "paragraph" || len(document.DeduplicatedBlocks) != 1 || document.DeduplicatedBlocks[0].DuplicateOf != "paragraph" {
		t.Fatalf("document=%+v", document)
	}
	if document.PipelineMetrics.HardFailedBlocks != 0 || document.PipelineMetrics.DeduplicatedBlocks != 1 {
		t.Fatalf("metrics=%+v", document.PipelineMetrics)
	}
}

func TestPartialContinuationTrimsRepeatedTranslatedPhrase(t *testing.T) {
	first := geometryParagraph("first", 96, []ocr.OCRBox{{X: 10, Y: 10, Width: 190, Height: 20}})
	second := geometryParagraph("second", 95, []ocr.OCRBox{{X: 150, Y: 11, Width: 160, Height: 20}})
	first.Text, first.Lines[0].Text, first.Lines[0].Words[0].Text = "the equipment on which the engine is used", "the equipment on which the engine is used", "the equipment on which the engine is used"
	second.Text, second.Lines[0].Text, second.Lines[0].Words[0].Text = "the engine is used refer to specification", "the engine is used refer to specification", "the engine is used refer to specification"
	candidates := []renderCandidate{
		{index: 0, paragraph: first, translation: TranslatedBlock{TranslatedText: "оборудование на котором используется двигатель"}, geometry: SourceTextGeometry{Bounds: first.Box, Regions: []ocr.OCRBox{first.Box}}},
		{index: 1, paragraph: second, translation: TranslatedBlock{TranslatedText: "используется двигатель см спецификацию"}, geometry: SourceTextGeometry{Bounds: second.Box, Regions: []ocr.OCRBox{second.Box}}},
	}
	normalized := normalizeCandidateContinuations(candidates)
	if !normalized[1] || candidates[1].translation.TranslatedText != "см спецификацию" {
		t.Fatalf("candidate=%+v normalized=%v", candidates[1], normalized)
	}
}

func TestPrepareNonRenderableParagraphDoesNotBlockTranslatedCandidate(t *testing.T) {
	paragraphs := []ocr.OCRParagraph{
		geometryParagraph("valid", 90, []ocr.OCRBox{{X: 20, Y: 20, Width: 120, Height: 20}, {X: 20, Y: 70, Width: 120, Height: 20}}),
		geometryParagraph("failed", 99, []ocr.OCRBox{{X: 45, Y: 45, Width: 70, Height: 18}}),
	}
	document := prepareGeometryDocument(t, paragraphs, []TranslatedBlock{
		{ID: "valid", TranslatedText: "Translated", Status: "translated"},
		{ID: "failed", Status: "failed"},
	})
	if len(document.Blocks) != 1 || document.Blocks[0].ID != "valid" {
		t.Fatalf("blocks=%+v skipped=%+v", document.Blocks, document.SkippedBlocks)
	}
	if len(document.SkippedBlocks) != 1 || document.SkippedBlocks[0].Reason != "translation_not_successful" {
		t.Fatalf("skipped=%+v", document.SkippedBlocks)
	}
}

func TestPrepareAxisAlignedPaddleGeometryAllowsIndependentLabels(t *testing.T) {
	left := geometryParagraph("left", 92, []ocr.OCRBox{{X: 20, Y: 18, Width: 80, Height: 18}, {X: 20, Y: 70, Width: 80, Height: 18}})
	right := geometryParagraph("right", 91, []ocr.OCRBox{{X: 40, Y: 42, Width: 70, Height: 20}})
	for lineIndex := range left.Lines {
		left.Lines[lineIndex].Words[0].GeometryLevel = "full"
		left.Lines[lineIndex].Words[0].Polygon = ocr.OCRPolygon{}
	}
	right.Lines[0].Words[0].GeometryLevel = "full"
	right.Lines[0].Words[0].Polygon = ocr.OCRPolygon{}
	document := prepareGeometryDocument(t, []ocr.OCRParagraph{left, right}, []TranslatedBlock{
		{ID: "left", TranslatedText: "Left labels", Status: "translated"},
		{ID: "right", TranslatedText: "UI", Status: "translated"},
	})
	if len(document.Blocks) != 2 {
		t.Fatalf("blocks=%+v skipped=%+v", document.Blocks, document.SkippedBlocks)
	}
}

func prepareGeometryDocument(t *testing.T, paragraphs []ocr.OCRParagraph, blocks []TranslatedBlock) RenderDocument {
	t.Helper()
	directory := t.TempDir()
	writeTestFont(t, directory)
	renderer, err := NewRenderer(directory, DefaultRenderConfig(), &fakeInpaintEngine{})
	if err != nil {
		t.Fatal(err)
	}
	defer renderer.Close()
	source := solidNRGBA(320, 130, color.NRGBA{R: 248, G: 248, B: 248, A: 255})
	for _, paragraph := range paragraphs {
		for _, line := range paragraph.Lines {
			for _, word := range line.Words {
				drawSyntheticWord(source, word.Box)
			}
		}
	}
	page := ocr.OCRPage{Image: ocr.OCRImageInfo{Width: 320, Height: 130}, Paragraphs: paragraphs, Diagnostics: ocr.OCRDiagnostics{Backend: "paddleocr"}}
	document, err := renderer.Prepare(context.Background(), source, page, TranslationDocument{Blocks: blocks})
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func geometryParagraph(id string, confidence float64, boxes []ocr.OCRBox) ocr.OCRParagraph {
	paragraph := ocr.OCRParagraph{ID: id, Confidence: confidence}
	for index, box := range boxes {
		text := id
		polygon := ocr.OCRPolygon{{X: float64(box.X), Y: float64(box.Y)}, {X: float64(box.X + box.Width), Y: float64(box.Y)}, {X: float64(box.X + box.Width), Y: float64(box.Y + box.Height)}, {X: float64(box.X), Y: float64(box.Y + box.Height)}}
		word := ocr.OCRWord{ID: id, Text: text, Confidence: confidence, RecognizerConfidence: confidence, DetectorConfidence: confidence, Box: box, Polygon: polygon, Accepted: true}
		paragraph.Lines = append(paragraph.Lines, ocr.OCRLine{ID: id, Text: text, Confidence: confidence, Box: box, Words: []ocr.OCRWord{word}, Line: index + 1})
	}
	paragraph.Text = id
	lineBoxes := make([]ocr.OCRBox, 0, len(paragraph.Lines))
	for _, line := range paragraph.Lines {
		lineBoxes = append(lineBoxes, line.Box)
	}
	paragraph.Box = unionOCRBoxes(lineBoxes)
	return paragraph
}
