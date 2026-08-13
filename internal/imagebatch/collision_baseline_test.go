package imagebatch

import (
	"testing"

	"github.com/sympllate/translator/internal/ocr"
)

func TestLegacyOverlapBaselineVersusSourceGeometryPolicy(t *testing.T) {
	tests := []struct {
		name       string
		paragraphs []ocr.OCRParagraph
		eligible   []bool
		wantLegacy int
		wantNew    int
	}{
		{
			name: "paragraph_union_empty_space",
			paragraphs: []ocr.OCRParagraph{
				geometryParagraph("a", 95, []ocr.OCRBox{{X: 10, Y: 10, Width: 220, Height: 18}, {X: 10, Y: 72, Width: 220, Height: 18}}),
				geometryParagraph("b", 94, []ocr.OCRBox{{X: 90, Y: 40, Width: 80, Height: 18}}),
			},
			eligible: []bool{true, true}, wantLegacy: 2, wantNew: 0,
		},
		{
			name: "real_collision",
			paragraphs: []ocr.OCRParagraph{
				geometryParagraph("a", 95, []ocr.OCRBox{{X: 20, Y: 20, Width: 120, Height: 24}}),
				geometryParagraph("b", 80, []ocr.OCRBox{{X: 35, Y: 22, Width: 110, Height: 24}}),
			},
			eligible: []bool{true, true}, wantLegacy: 2, wantNew: 1,
		},
		{
			name: "failed_translation_blocker",
			paragraphs: []ocr.OCRParagraph{
				geometryParagraph("valid", 90, []ocr.OCRBox{{X: 20, Y: 20, Width: 120, Height: 20}, {X: 20, Y: 70, Width: 120, Height: 20}}),
				geometryParagraph("failed", 99, []ocr.OCRBox{{X: 45, Y: 45, Width: 70, Height: 18}}),
			},
			eligible: []bool{true, false}, wantLegacy: 1, wantNew: 0,
		},
	}
	transform, _ := NewCoordinateTransform(320, 130, 320, 130)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			boxes := make([]ocr.OCRBox, len(test.paragraphs))
			candidates := make([]renderCandidate, 0)
			for index, paragraph := range test.paragraphs {
				geometry := sourceTextGeometry(paragraph, transform, 320, 130)
				boxes[index] = geometry.Bounds
				if test.eligible[index] {
					candidates = append(candidates, renderCandidate{index: index, paragraph: paragraph, geometry: geometry})
				}
			}
			legacy := 0
			for index, box := range boxes {
				if test.eligible[index] && hasAmbiguousOverlap(box, boxes, index) {
					legacy++
				}
			}
			rejected, _ := resolveSourceCollisions(candidates)
			if legacy != test.wantLegacy || len(rejected) != test.wantNew {
				t.Fatalf("legacy=%d new=%d", legacy, len(rejected))
			}
		})
	}
}
