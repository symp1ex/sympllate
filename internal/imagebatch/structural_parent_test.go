package imagebatch

import (
	"image"
	"image/color"
	"image/draw"
	"testing"

	"github.com/sympllate/translator/internal/ocr"
)

func TestInferStructuralParentsFindsWarningHeaderAndBody(t *testing.T) {
	source := structuralTestImage(220, 150)
	drawStructuralTestBox(source, ocr.OCRBox{X: 20, Y: 20, Width: 160, Height: 100})
	drawStructuralTestHorizontal(source, 20, 179, 45)
	paragraphs := []ocr.OCRParagraph{
		structuralTestParagraph("warning", "WARNING", ocr.OCRBox{X: 65, Y: 25, Width: 70, Height: 14}),
		structuralTestParagraph("body", "Engine exhaust may ignite combustible materials.", ocr.OCRBox{X: 30, Y: 55, Width: 140, Height: 50}),
	}
	geometries := structuralTestGeometries(paragraphs)
	parents := inferStructuralParents(source, paragraphs, geometries)
	if parents[0].Type != "warning_header" || !structuralBoxNear(parents[0].Bounds, ocr.OCRBox{X: 20, Y: 20, Width: 160, Height: 26}, 2) {
		t.Fatalf("header parent=%+v", parents[0])
	}
	if parents[1].Type != "warning_body" || !structuralBoxNear(parents[1].Bounds, ocr.OCRBox{X: 20, Y: 45, Width: 160, Height: 75}, 2) {
		t.Fatalf("body parent=%+v", parents[1])
	}
}

func TestInferStructuralParentsFindsTableCells(t *testing.T) {
	source := structuralTestImage(220, 100)
	drawStructuralTestBox(source, ocr.OCRBox{X: 10, Y: 20, Width: 90, Height: 40})
	drawStructuralTestBox(source, ocr.OCRBox{X: 99, Y: 20, Width: 90, Height: 40})
	paragraphs := []ocr.OCRParagraph{
		structuralTestParagraph("left", "Ignition Timing", ocr.OCRBox{X: 20, Y: 30, Width: 60, Height: 15}),
		structuralTestParagraph("right", "Unadjustable", ocr.OCRBox{X: 110, Y: 30, Width: 65, Height: 15}),
	}
	parents := inferStructuralParents(source, paragraphs, structuralTestGeometries(paragraphs))
	for index, parent := range parents {
		if parent.Type != "table_cell" || parent.SourceCell == "" {
			t.Fatalf("cell %d parent=%+v", index, parent)
		}
		if !boxContains(parent.Bounds, paragraphs[index].Box) {
			t.Fatalf("cell %d does not contain source: parent=%+v source=%+v", index, parent.Bounds, paragraphs[index].Box)
		}
	}
}

func TestStructuralParentClampsNormalAndFallbackCandidates(t *testing.T) {
	bounds := ocr.OCRBox{X: 40, Y: 30, Width: 100, Height: 60}
	candidates := constrainLayoutCandidates([]layoutCandidate{
		{box: ocr.OCRBox{X: 0, Y: 0, Width: 220, Height: 140}, expanded: true},
		{box: ocr.OCRBox{X: 50, Y: 40, Width: 30, Height: 20}},
	}, bounds)
	if len(candidates) != 2 {
		t.Fatalf("candidates=%+v", candidates)
	}
	for _, candidate := range candidates {
		if violation := containmentViolationPixels(candidate.box, bounds); violation != 0 {
			t.Fatalf("candidate escaped structural parent by %d pixels: %+v", violation, candidate.box)
		}
	}
	escaped := ocr.OCRBox{X: 30, Y: 40, Width: 30, Height: 20}
	if containmentViolationPixels(escaped, bounds) == 0 {
		t.Fatalf("escaped candidate was not detected: %+v", escaped)
	}
}

func TestContainerBorderOverlapRequiresDetectedContainer(t *testing.T) {
	box := ocr.OCRBox{X: 40, Y: 30, Width: 20, Height: 10}
	bounds := ocr.OCRBox{X: 40, Y: 30, Width: 100, Height: 60}
	if overlapsContainerBorder(box, StructuralParent{Bounds: bounds, Detection: "source_text_cluster"}) {
		t.Fatal("synthetic OCR-cluster edge reported as a real container border")
	}
	if !overlapsContainerBorder(box, StructuralParent{Bounds: bounds, Detection: "stable_border_rectangle"}) {
		t.Fatal("text touching a detected container border was not reported")
	}
}

func structuralTestImage(width, height int) *image.NRGBA {
	result := image.NewNRGBA(image.Rect(0, 0, width, height))
	draw.Draw(result, result.Bounds(), image.NewUniform(color.NRGBA{R: 255, G: 255, B: 255, A: 255}), image.Point{}, draw.Src)
	return result
}

func drawStructuralTestBox(target *image.NRGBA, box ocr.OCRBox) {
	drawStructuralTestHorizontal(target, box.X, box.X+box.Width-1, box.Y)
	drawStructuralTestHorizontal(target, box.X, box.X+box.Width-1, box.Y+box.Height-1)
	for y := box.Y; y < box.Y+box.Height; y++ {
		target.SetNRGBA(box.X, y, color.NRGBA{A: 255})
		target.SetNRGBA(box.X+box.Width-1, y, color.NRGBA{A: 255})
	}
}

func drawStructuralTestHorizontal(target *image.NRGBA, left, right, y int) {
	for x := left; x <= right; x++ {
		target.SetNRGBA(x, y, color.NRGBA{A: 255})
	}
}

func structuralTestParagraph(id, text string, box ocr.OCRBox) ocr.OCRParagraph {
	return ocr.OCRParagraph{ID: id, Text: text, Box: box, Lines: []ocr.OCRLine{{ID: id + "-line", Text: text, Box: box}}}
}

func structuralTestGeometries(paragraphs []ocr.OCRParagraph) []SourceTextGeometry {
	result := make([]SourceTextGeometry, len(paragraphs))
	for index, paragraph := range paragraphs {
		result[index] = SourceTextGeometry{ID: paragraph.ID, Bounds: paragraph.Box, Regions: []ocr.OCRBox{paragraph.Box}, LineRegions: []ocr.OCRBox{paragraph.Box}, Level: "line"}
	}
	return result
}

func structuralBoxNear(got, want ocr.OCRBox, tolerance int) bool {
	return absInt(got.X-want.X) <= tolerance && absInt(got.Y-want.Y) <= tolerance && absInt(got.Width-want.Width) <= tolerance*2 && absInt(got.Height-want.Height) <= tolerance*2
}
