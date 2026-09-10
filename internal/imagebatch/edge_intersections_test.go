package imagebatch

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/sympllate/translator/internal/ocr"
)

type edgeIntersectionFixture struct {
	Name              string
	Paragraph         ocr.OCRParagraph
	Protected, Active []ocr.OCRBox
	Parent            ocr.OCRBox
	Width, Height     int
	Text              string
	Preferred, Step   float64
	OldBase           ocr.OCRBox
}

func TestSafeTranslationBaseSavedEdgeIntersections(t *testing.T) {
	data, err := os.ReadFile("testdata/edge_intersections.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []edgeIntersectionFixture
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	// Unit tests use the package test font; the saved-run replay uses the exact
	// portable regular.ttf via this optional directory, without invoking models.
	directory := os.Getenv("SYMPLLATE_LAYOUT_FONT_DIR")
	if directory == "" {
		directory = t.TempDir()
		writeTestFont(t, directory)
	}
	r, err := NewRenderer(directory, DefaultRenderConfig(), &fakeInpaintEngine{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			transform := CoordinateTransform{ScaleX: 1, ScaleY: 1}
			geometry := sourceTextGeometry(f.Paragraph, transform, f.Width, f.Height)
			if geometry.Bounds != f.Paragraph.Box {
				t.Fatalf("source bounds changed: %+v", geometry.Bounds)
			}
			before, err := json.Marshal(geometry)
			if err != nil {
				t.Fatal(err)
			}
			base := safeTranslationBase(geometry, f.Protected)
			after, err := json.Marshal(geometry)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatal("source geometry mutated")
			}
			if intersectsAny(base, f.Protected) || !boxContains(geometry.Bounds, base) {
				t.Fatalf("unsafe base: %+v", base)
			}
			// A narrow edge intersection must preserve every source line's center,
			// including the short last line; choosing one clean line loses this area.
			for _, line := range geometry.LineRegions {
				center := ocr.OCRBox{X: line.X + line.Width/2, Y: line.Y + line.Height/2, Width: 1, Height: 1}
				if !boxContains(base, center) {
					t.Errorf("lost source line center %+v in base %+v", center, base)
				}
			}
			base = clampBoxToBounds(base, f.Parent)
			box, fit, err := r.fitBlockWithin(context.Background(), f.Text, f.Preferred, len(f.Paragraph.Lines), f.Step, "left", base, f.Protected, -1, f.Active, f.Width, f.Height, f.Parent)
			if err != nil {
				t.Fatal(err)
			}
			_, oldFit, err := r.fitBlockWithin(context.Background(), f.Text, f.Preferred, len(f.Paragraph.Lines), f.Step, "left", f.OldBase, f.Protected, -1, f.Active, f.Width, f.Height, f.Parent)
			if err != nil {
				t.Fatal(err)
			}
			if !fit.Fits || fit.FontSize <= oldFit.FontSize {
				t.Errorf("no improvement: old=%v new=%v fits=%v", oldFit.FontSize, fit.FontSize, fit.Fits)
			}
			t.Logf("font %g -> %g; source rows=%d step=%g; base=%+v", oldFit.FontSize, fit.FontSize, len(f.Paragraph.Lines), f.Step, base)
			ink := renderedLineRegions(positionTextLines(fit, box, "left", "top", r.config.HorizontalTextPadding, r.config.VerticalTextPadding), fit.LineHeight, fit.Ascent)
			if intersectsRegionSets(ink, f.Protected) || intersectsRegionSets(ink, f.Active) || containmentViolationPixels(unionOCRBoxes(ink), f.Parent) > 0 {
				t.Errorf("unsafe fit: box=%+v ink=%+v", box, ink)
			}
		})
	}
}

func TestSafeTranslationBaseDoesNotCropWhenNoLineIsClean(t *testing.T) {
	// M18/b6 already used the full source region. Cropping it caused a
	// 29.5 -> 24.25 regression in the sequential replay.
	lines := []ocr.OCRBox{{X: 116, Y: 508, Width: 643, Height: 39}, {X: 140, Y: 541, Width: 237, Height: 37}}
	geometry := SourceTextGeometry{Bounds: ocr.OCRBox{X: 116, Y: 508, Width: 643, Height: 70}, LineRegions: lines, Regions: lines}
	protected := []ocr.OCRBox{{X: 138, Y: 477, Width: 105, Height: 37}, {X: 117, Y: 577, Width: 639, Height: 34}}
	if got := safeTranslationBase(geometry, protected); got != geometry.Bounds {
		t.Fatalf("got=%+v want=%+v", got, geometry.Bounds)
	}
}

func TestSafeTranslationBaseEdgeCropPreservesImageAndParentBounds(t *testing.T) {
	paragraph := geometryParagraph("edge", 95, []ocr.OCRBox{{X: -2, Y: -2, Width: 190, Height: 22}, {X: 0, Y: 30, Width: 190, Height: 20}, {X: 0, Y: 60, Width: 80, Height: 23}})
	geometry := sourceTextGeometry(paragraph, CoordinateTransform{ScaleX: 1, ScaleY: 1}, 200, 80)
	protected := []ocr.OCRBox{{X: 90, Y: 78, Width: 110, Height: 2}}
	base := safeTranslationBase(geometry, protected)
	if base != (ocr.OCRBox{X: 0, Y: 0, Width: 190, Height: 78}) {
		t.Fatalf("base=%+v", base)
	}
	bounds := ocr.OCRBox{X: 0, Y: 0, Width: 160, Height: 76}
	base = clampBoxToBounds(base, bounds)
	dir := t.TempDir()
	writeTestFont(t, dir)
	r, err := NewRenderer(dir, DefaultRenderConfig(), &fakeInpaintEngine{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, text := range []string{"Several translated words in a small region.", strings.Repeat("A long translation that must remain local. ", 30)} {
		box, fit, err := r.fitBlockWithin(context.Background(), text, 18, 3, 30, "left", base, protected, -1, nil, 200, 80, bounds)
		if err != nil {
			t.Fatal(err)
		}
		if !fit.Fits {
			if len(text) < 100 {
				t.Fatal("short translation unexpectedly failed")
			}
			continue
		} // An honest hard failure is valid for a long translation.
		ink := renderedLineRegions(positionTextLines(fit, box, "left", "top", 2, 2), fit.LineHeight, fit.Ascent)
		if containmentViolationPixels(box, bounds) > 0 || containmentViolationPixels(unionOCRBoxes(ink), bounds) > 0 || intersectsRegionSets(ink, protected) {
			t.Fatalf("unsafe fit box=%+v ink=%+v", box, ink)
		}
	}
	blocked := ocr.OCRBox{Width: 2, Height: 2}
	_, fit, err := r.fitBlockWithin(context.Background(), "No room for this translation", 18, 3, 30, "left", blocked, []ocr.OCRBox{blocked}, -1, nil, 2, 2, blocked)
	if err != nil {
		t.Fatal(err)
	}
	if fit.Fits {
		t.Fatal("a fully blocked two-pixel region must not fabricate a fit")
	}
}

func TestPrepareRetainsRotatedOCRPolicy(t *testing.T) {
	paragraph := geometryParagraph("rotated", 95, []ocr.OCRBox{{X: 20, Y: 20, Width: 180, Height: 24}, {X: 20, Y: 48, Width: 150, Height: 24}})
	for i := range paragraph.Lines {
		word := &paragraph.Lines[i].Words[0]
		word.Polygon[0].Y += 5
		word.Polygon[2].Y -= 5
		word.CleanupSafe = false
	}
	doc := prepareGeometryDocument(t, []ocr.OCRParagraph{paragraph}, []TranslatedBlock{{ID: paragraph.ID, TranslatedText: "Rotated OCR still renders horizontally", Status: "translated"}})
	if len(doc.Blocks) != 1 || len(doc.SkippedBlocks) != 0 {
		t.Fatalf("unexpected fates: %+v", doc.PipelineMetrics)
	}
	b := doc.Blocks[0]
	geometry := sourceTextGeometry(paragraph, CoordinateTransform{ScaleX: 1, ScaleY: 1}, 320, 130)
	if !reflect.DeepEqual(b.SourceGeometry, geometry) || b.CleanupSafe || !b.CleanupSafetyKnown {
		t.Fatalf("rotated source/cleanup policy changed: %+v", b)
	}
}

func TestSafeTranslationBaseKeepsBlockersAndLocalBounds(t *testing.T) {
	lines := []ocr.OCRBox{{X: 10, Y: 10, Width: 200, Height: 20}, {X: 10, Y: 40, Width: 200, Height: 20}, {X: 10, Y: 70, Width: 80, Height: 20}}
	geometry := SourceTextGeometry{Bounds: unionOCRBoxes(lines), LineRegions: lines, Regions: lines}
	for _, tc := range []struct {
		name      string
		geometry  SourceTextGeometry
		protected []ocr.OCRBox
		want      ocr.OCRBox
	}{
		{"independent paragraphs", geometry, []ocr.OCRBox{{X: 10, Y: 95, Width: 200, Height: 20}}, geometry.Bounds},
		{"different columns", geometry, []ocr.OCRBox{{X: 220, Y: 10, Width: 200, Height: 80}}, geometry.Bounds},
		{"heading above body", geometry, []ocr.OCRBox{{X: 10, Y: 0, Width: 200, Height: 10}}, geometry.Bounds},
		{"interior blocker", geometry, []ocr.OCRBox{{X: 50, Y: 40, Width: 80, Height: 20}}, lines[0]},
		{"ragged whitespace diagram label", geometry, []ocr.OCRBox{{X: 130, Y: 65, Width: 50, Height: 18}}, lines[0]},
		{"no clean region", geometry, []ocr.OCRBox{geometry.Bounds}, geometry.Bounds},
		{"single line", SourceTextGeometry{Bounds: lines[0], LineRegions: lines[:1]}, []ocr.OCRBox{{X: 200, Y: 10, Width: 20, Height: 20}}, lines[0]},
		{"tiny source", SourceTextGeometry{Bounds: ocr.OCRBox{Width: 2, Height: 2}}, []ocr.OCRBox{{X: 1, Width: 2, Height: 2}}, ocr.OCRBox{Width: 2, Height: 2}},
		{"narrow top and bottom", geometry, []ocr.OCRBox{{X: 50, Y: 0, Width: 80, Height: 12}, {X: 50, Y: 88, Width: 80, Height: 20}}, ocr.OCRBox{X: 10, Y: 12, Width: 200, Height: 76}},
		{"reversed obstacle order", geometry, []ocr.OCRBox{{X: 50, Y: 88, Width: 80, Height: 20}, {X: 50, Y: 0, Width: 80, Height: 12}}, ocr.OCRBox{X: 10, Y: 12, Width: 200, Height: 76}},
		{"substantial edge intrusion", geometry, []ocr.OCRBox{{X: 50, Y: 0, Width: 80, Height: 20}}, lines[1]},
		{"edge plus interior label", geometry, []ocr.OCRBox{{X: 50, Y: 0, Width: 80, Height: 12}, {X: 50, Y: 32, Width: 80, Height: 5}}, lines[1]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := safeTranslationBase(tc.geometry, tc.protected); got != tc.want {
				t.Fatalf("got=%+v want=%+v", got, tc.want)
			}
		})
	}
}
