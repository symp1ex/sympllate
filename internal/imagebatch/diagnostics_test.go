package imagebatch

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sympllate/translator/internal/ocr"
)

func TestCollisionAndBackendDiagnosticsMarshalAdditively(t *testing.T) {
	report := JobReport{OCRBackend: "paddleocr", Files: []JobFileReport{{
		OCRBackend:       "paddleocr",
		SourceGeometries: []SourceTextGeometry{{ID: "a", Bounds: ocr.OCRBox{X: 1, Y: 2, Width: 3, Height: 4}, Regions: []ocr.OCRBox{{X: 1, Y: 2, Width: 3, Height: 1}}, Level: "line"}},
		Collisions:       []CollisionDiagnostic{{BlockID: "a", ConflictingBlockID: "b", ParagraphOverlapRatio: .82, TextRegionOverlapRatio: .03, CollisionClass: "aabb_only", Decision: "kept_both"}},
	}}}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	value := string(data)
	for _, expected := range []string{`"ocrBackend":"paddleocr"`, `"sourceGeometries"`, `"conflictingBlockId":"b"`, `"collisionClass":"aabb_only"`, `"decision":"kept_both"`} {
		if !strings.Contains(value, expected) {
			t.Fatalf("missing %s in %s", expected, value)
		}
	}
}
