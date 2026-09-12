package imagebatch

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/sympllate/translator/internal/ocr"
)

func TestBestCandidateFitSavedCollisions(t *testing.T) {
	var fixtures []struct {
		Name, Text, Alignment                string
		Preferred, Step, Admissible, Maximum float64
		SourceLines                          int
		Base, Box                            ocr.OCRBox
		Protected, Active                    []ocr.OCRBox
	}
	data, err := os.ReadFile("testdata/candidate_font_search.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &fixtures); err != nil {
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
			vertical := "top"
			if f.SourceLines <= 1 {
				vertical = "middle"
			}
			request := r.textFitRequest(f.Text, r.config.MinimumFontSize, f.Preferred, f.Preferred, f.Step)
			request.Width, request.Height = f.Box.Width, f.Box.Height
			maximum, err := FitText(context.Background(), r.fonts, request)
			if err != nil {
				t.Fatal(err)
			}
			collides := func(fit TextFitResult) bool {
				ink := renderedLineRegions(positionTextLines(fit, f.Box, f.Alignment, vertical, r.config.HorizontalTextPadding, r.config.VerticalTextPadding), fit.LineHeight, fit.Ascent)
				return intersectsRegionSets(ink, f.Protected) || intersectsRegionSets(ink, f.Active)
			}
			if exactFont && (!maximum.Fits || maximum.FontSize != f.Maximum || !collides(maximum)) {
				t.Fatalf("saved maximum did not reproduce: %+v", maximum)
			}
			candidate := layoutCandidate{box: f.Box, expanded: f.Box != f.Base}
			var want TextFitResult
			for size := maximum.FontSize; size >= maximum.MinimumFontSize; size -= fontSizeStep {
				fit, err := measureFit(context.Background(), r.fonts, request, size)
				if err != nil {
					t.Fatal(err)
				}
				if !fit.Fits || collides(fit) {
					continue
				}
				decorateLayoutResult(&fit, f.Preferred, f.SourceLines, f.Step, f.Alignment, f.Base, candidate, true)
				if !want.Fits || fit.Score < want.Score {
					want = fit
				}
				if !collides(maximum) {
					break
				} // The unchanged fast path.
			}
			if exactFont {
				probe, err := measureFit(context.Background(), r.fonts, request, f.Admissible)
				if err != nil || !probe.Fits || collides(probe) {
					t.Fatalf("saved admissible size: %+v %v", probe, err)
				}
			}
			box, got, err := r.bestCandidateFit(context.Background(), f.Text, f.Preferred, f.Preferred, r.config.MinimumFontSize, f.SourceLines, f.Step, f.Alignment, vertical, f.Base, []layoutCandidate{candidate}, f.Protected, f.Active, true, true)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Fits || got.FontSize != want.FontSize || got.Score != want.Score || box != f.Box || collides(got) {
				t.Fatalf("max=%g want=%g/%g got=%g/%g fits=%v", maximum.FontSize, want.FontSize, want.Score, got.FontSize, got.Score, got.Fits)
			}
			t.Logf("maximum=%g recovered=%g score=%g", maximum.FontSize, got.FontSize, got.Score)
		})
	}
}

func TestBestCandidateFitSearchesBelowCollidingMaximum(t *testing.T) {
	dir := t.TempDir()
	writeTestFont(t, dir)
	r, err := NewRenderer(dir, DefaultRenderConfig(), &fakeInpaintEngine{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	box := ocr.OCRBox{X: 20, Y: 30, Width: 240, Height: 120}
	for _, text := range []string{"Alpha beta gamma delta epsilon zeta eta theta", "Single line", strings.Repeat("LongToken", 8)} {
		for _, alignment := range []string{"left", "right", "center"} {
			for _, vertical := range []string{"top", "middle"} {
				for _, source := range []bool{true, false} {
					name := text[:10] + "/" + alignment + "/" + vertical
					if source {
						name += "/source"
					} else {
						name += "/active"
					}
					t.Run(name, func(t *testing.T) {
						request := r.textFitRequest(text, 10, 24, 24, 26)
						request.Width, request.Height = box.Width, box.Height
						maximum, err := FitText(context.Background(), r.fonts, request)
						if err != nil || !maximum.Fits {
							t.Fatalf("maximum: %+v, %v", maximum, err)
						}
						regions := func(f TextFitResult) []ocr.OCRBox {
							return renderedLineRegions(positionTextLines(f, box, alignment, vertical, r.config.HorizontalTextPadding, r.config.VerticalTextPadding), f.LineHeight, f.Ascent)
						}
						ink := regions(maximum)
						last := ink[len(ink)-1]
						blockers := []ocr.OCRBox{{X: box.X, Y: last.Y + last.Height - 1, Width: box.Width, Height: 1}}
						var occupied, active []ocr.OCRBox
						if source {
							occupied = blockers
						} else {
							active = blockers
						}
						// Independently enumerate every existing quarter-pixel size. A
						// smaller admissible fit need not be the best score or have the
						// same line breaks as its larger neighbor.
						var want TextFitResult
						candidate := layoutCandidate{box: box}
						for size := maximum.FontSize; size >= 10; size -= fontSizeStep {
							f, err := measureFit(context.Background(), r.fonts, request, size)
							if err != nil {
								t.Fatal(err)
							}
							if !f.Fits || intersectsRegionSets(regions(f), blockers) {
								continue
							}
							decorateLayoutResult(&f, 24, 2, 26, alignment, box, candidate, true)
							if !want.Fits || f.Score < want.Score {
								want = f
							}
						}
						if !want.Fits || want.FontSize >= maximum.FontSize {
							t.Fatal("fixture needs a smaller admissible fit")
						}
						gotBox, got, err := r.bestCandidateFit(context.Background(), text, 24, 24, 10, 2, 26, alignment, vertical, box, []layoutCandidate{candidate}, occupied, active, true, true)
						if err != nil {
							t.Fatal(err)
						}
						if !got.Fits || got.FontSize != want.FontSize || got.Score != want.Score || !reflect.DeepEqual(got.Lines, want.Lines) {
							t.Fatalf("max %g collides; want admissible %g score %g; got %g fits %v score %g", maximum.FontSize, want.FontSize, want.Score, got.FontSize, got.Fits, got.Score)
						}
						if gotBox != box || intersectsRegionSets(regions(got), blockers) || containmentViolationPixels(unionOCRBoxes(regions(got)), box) != 0 {
							t.Fatal("unsafe placement")
						}
						if got.MinimumFontSize != 10 {
							t.Fatalf("minimum metadata: %g", got.MinimumFontSize)
						}
					})
				}
			}
		}
	}
}

func TestBestCandidateFitNoAdmissibleFontAndCancellation(t *testing.T) {
	dir := t.TempDir()
	writeTestFont(t, dir)
	r, err := NewRenderer(dir, DefaultRenderConfig(), &fakeInpaintEngine{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	box := ocr.OCRBox{Width: 200, Height: 100}
	for _, minimum := range []float64{10, 24} {
		_, fit, err := r.bestCandidateFit(context.Background(), "Blocked text", 24, 24, minimum, 1, 26, "center", "middle", box, []layoutCandidate{{box: box}}, []ocr.OCRBox{box}, nil, minimum < 24, true)
		if err != nil || fit.Fits || !fit.Overflow {
			t.Fatalf("blocked fit: %+v, %v", fit, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = r.bestCandidateFit(ctx, "Blocked text", 24, 24, 10, 1, 26, "left", "top", box, []layoutCandidate{{box: box}}, []ocr.OCRBox{box}, nil, true, true)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestBestCandidateFitRespectsPhaseMinimum(t *testing.T) {
	dir := t.TempDir()
	writeTestFont(t, dir)
	r, err := NewRenderer(dir, DefaultRenderConfig(), &fakeInpaintEngine{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	box := ocr.OCRBox{Width: 240, Height: 120}
	req := r.textFitRequest("Single line", 10, 24, 24, 26)
	req.Width, req.Height = box.Width, box.Height
	maximum, err := FitText(context.Background(), r.fonts, req)
	if err != nil {
		t.Fatal(err)
	}
	ink := renderedLineRegions(positionTextLines(maximum, box, "left", "top", 2, 2), maximum.LineHeight, maximum.Ascent)
	blocker := []ocr.OCRBox{{Y: ink[0].Y + ink[0].Height - 1, Width: box.Width, Height: 1}}
	_, normal, err := r.bestCandidateFit(context.Background(), req.Text, 24, 24, 23.6, 1, 26, "left", "top", box, []layoutCandidate{{box: box}}, blocker, nil, false, true)
	if err != nil || normal.Fits || normal.MinimumFontSize != 23.75 {
		t.Fatalf("normal crossed minimum: %+v %v", normal, err)
	}
	_, emergency, err := r.bestCandidateFit(context.Background(), req.Text, 24, 24, 10, 1, 26, "left", "top", box, []layoutCandidate{{box: box}}, blocker, nil, true, true)
	if err != nil || !emergency.Fits || emergency.FontSize != 23.5 || !emergency.EmergencyShrink || emergency.MinimumFontSize != 10 {
		t.Fatalf("emergency: %+v %v", emergency, err)
	}
}

// Cancel deterministically after the initial FitText checks, while the new
// search is in progress; a pre-cancelled context alone does not cover that path.
type candidateSearchContext struct {
	context.Context
	calls, cancelAfter int
	cancel             context.CancelFunc
}

func (c *candidateSearchContext) Err() error {
	c.calls++
	if c.cancelAfter > 0 && c.calls > c.cancelAfter {
		c.cancel()
	}
	return c.Context.Err()
}

func TestBestCandidateFitCancellationDuringSearch(t *testing.T) {
	dir := t.TempDir()
	writeTestFont(t, dir)
	r, err := NewRenderer(dir, DefaultRenderConfig(), &fakeInpaintEngine{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	box := ocr.OCRBox{Width: 240, Height: 120}
	req := r.textFitRequest("Blocked text", 10, 24, 24, 26)
	req.Width, req.Height = box.Width, box.Height
	probe := &candidateSearchContext{Context: context.Background()}
	if _, err := FitText(probe, r.fonts, req); err != nil {
		t.Fatal(err)
	}
	// Allow the first smaller measurement, then cancel during the scan.
	if _, err := measureFit(probe, r.fonts, req, 23.75); err != nil {
		t.Fatal(err)
	}
	cancellable, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &candidateSearchContext{Context: cancellable, cancel: cancel, cancelAfter: probe.calls + 1}
	_, _, err = r.bestCandidateFit(ctx, req.Text, 24, 24, 10, 1, 26, "left", "top", box, []layoutCandidate{{box: box}}, []ocr.OCRBox{box}, nil, true, true)
	if !errors.Is(err, context.Canceled) || ctx.calls <= ctx.cancelAfter {
		t.Fatalf("search did not propagate cancellation: %v, checks=%d", err, ctx.calls)
	}
}
