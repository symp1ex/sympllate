package imagebatch

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"testing"

	"github.com/sympllate/translator/internal/ocr"
)

func TestFitBlockWithinGuardsRecoveredCandidate(t *testing.T) {
	var fixtures []struct {
		Name, Text, Alignment                       string
		Preferred, Step                             float64
		SourceLines, Width, Height                  int
		Base, Parent                                ocr.OCRBox
		Protected, Active                           []ocr.OCRBox
		IncumbentFont, IncumbentScore, ExpectedFont float64
	}
	data, err := os.ReadFile("testdata/candidate_font_guard.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	dir := os.Getenv("SYMPLLATE_LAYOUT_FONT_DIR")
	exactFont := dir != ""
	if dir == "" {
		dir = t.TempDir()
		writeTestFont(t, dir)
	}
	r, err := NewRenderer(dir, DefaultRenderConfig(), &fakeInpaintEngine{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			ctx := context.Background()
			vertical := "top"
			if f.SourceLines <= 1 {
				vertical = "middle"
			}
			candidates, err := r.layoutCandidates(f.Text, f.Preferred, f.SourceLines, f.Step, f.Alignment, f.Base, f.Protected, -1, f.Active, f.Width, f.Height)
			if err != nil {
				t.Fatal(err)
			}
			candidates = constrainLayoutCandidates(candidates, f.Parent)
			// Reconstruct the previous phase policy without collision font search.
			// This is also the reference for fonts other than the saved run's font.
			minimum := math.Max(r.config.MinimumFontSize, f.Preferred*r.config.Layout.PreferredShrinkRatio)
			maximum := math.Min(r.config.MaximumFontSize, f.Preferred*r.config.Layout.MaximumUpscaleRatio)
			oldBox, old, err := r.bestCandidateFit(ctx, f.Text, f.Preferred, maximum, minimum, f.SourceLines, f.Step, f.Alignment, vertical, f.Base, candidates, f.Protected, f.Active, false, false)
			if err != nil {
				t.Fatal(err)
			}
			if !old.Fits && minimum > r.config.MinimumFontSize {
				oldBox, old, err = r.bestCandidateFit(ctx, f.Text, f.Preferred, maximum, r.config.MinimumFontSize, f.SourceLines, f.Step, f.Alignment, vertical, f.Base, candidates, f.Protected, f.Active, true, false)
				if err != nil {
					t.Fatal(err)
				}
			}
			if !old.Fits {
				oldBox, old, err = r.forceFitBlockWithin(ctx, f.Text, f.Preferred, f.SourceLines, f.Step, f.Alignment, vertical, f.Base, f.Protected, f.Active, f.Width, f.Height, f.Parent)
				if err != nil {
					t.Fatal(err)
				}
			}
			if !old.Fits {
				t.Fatal("fixture needs a valid incumbent")
			}
			box, got, err := r.fitBlockWithin(ctx, f.Text, f.Preferred, f.SourceLines, f.Step, f.Alignment, f.Base, f.Protected, -1, f.Active, f.Width, f.Height, f.Parent)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Fits || got.FontSize < old.FontSize || got.Score > old.Score {
				t.Fatalf("regression: incumbent %g/%g, got %g/%g fits=%v", old.FontSize, old.Score, got.FontSize, got.Score, got.Fits)
			}
			if got.Score == old.Score && (box != oldBox || !reflect.DeepEqual(got, old)) {
				t.Fatal("a score tie must preserve the incumbent")
			}
			if exactFont {
				if old.FontSize != f.IncumbentFont || old.Score != f.IncumbentScore || got.FontSize != f.ExpectedFont {
					t.Fatalf("saved result changed: incumbent %g/%g, got %g; expected %g/%g -> %g", old.FontSize, old.Score, got.FontSize, f.IncumbentFont, f.IncumbentScore, f.ExpectedFont)
				}
				if f.ExpectedFont == f.IncumbentFont && (box != oldBox || !reflect.DeepEqual(got, old)) {
					t.Fatal("rejected recovery must preserve the entire incumbent")
				}
			}
			t.Logf("incumbent %g/%g -> %g/%g", old.FontSize, old.Score, got.FontSize, got.Score)
		})
	}
}
