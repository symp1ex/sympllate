package ocr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sympllate/translator/internal/translation"
)

func TestPaddleRecognizerRouting(t *testing.T) {
	t.Parallel()
	want := map[string]string{"ru": "eslav", "uk": "eslav", "en": "latin", "de": "latin", "fr": "latin", "es": "latin", "pl": "latin", "it": "latin", "pt": "latin", "tr": "latin", "zh": "cjk", "ja": "cjk", "ko": "korean", "ar": "arabic"}
	for source, recognizer := range want {
		if got := paddleRecognizerByLanguage[source]; got != recognizer {
			t.Errorf("%s -> %s, want %s", source, got, recognizer)
		}
	}
}

func TestCTCDecode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		classes []string
		indices []int
		want    string
	}{
		{"blank", []string{"a"}, []int{0, 0}, ""},
		{"repeated", []string{"a", "b"}, []int{1, 1, 0, 1, 2}, "aab"},
		{"cyrillic", []string{"Ж"}, []int{1}, "Ж"}, {"cjk", []string{"日"}, []int{1}, "日"},
		{"korean", []string{"한"}, []int{1}, "한"}, {"arabic", []string{"ش"}, []int{1}, "ش"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			classes := len(test.classes) + 1
			values := make([]float32, len(test.indices)*classes)
			for step, index := range test.indices {
				values[step*classes+index] = .9
			}
			got, confidence := ctcDecode(values, len(test.indices), classes, test.classes)
			if got != test.want {
				t.Fatalf("got %q want %q", got, test.want)
			}
			if got != "" && math.Abs(confidence-.9) > .0001 {
				t.Fatalf("confidence=%f", confidence)
			}
		})
	}
}

func TestDetectorCoordinatesReturnToSource(t *testing.T) {
	t.Parallel()
	transform := detectorTransform{SourceWidth: 1200, SourceHeight: 300, InputWidth: 960, InputHeight: 256}
	region := sourceRegion([4]paddlePoint{{80, 32}, {880, 40}, {870, 220}, {70, 210}}, transform)
	if region.Box.X < 0 || region.Box.Y < 0 || region.Box.X+region.Box.Width > 1200 || region.Box.Y+region.Box.Height > 300 {
		t.Fatalf("outside source: %+v", region.Box)
	}
	if region.Box.Width < 990 || region.Box.Height < 200 {
		t.Fatalf("unexpected projection: %+v", region.Box)
	}
}

func TestDetectorInputUpscalesSmallImagesToYAMLResizeLong(t *testing.T) {
	t.Parallel()
	image := image.NewNRGBA(image.Rect(0, 0, 320, 96))
	_, transform := detectorInput(image, detectorConfig{ResizeLong: 960, Mean: [3]float32{}, Std: [3]float32{1, 1, 1}})
	if transform.InputWidth != 960 || transform.InputHeight != 288 {
		t.Fatalf("detector input = %dx%d", transform.InputWidth, transform.InputHeight)
	}
}

func TestRecognizerInputKeepsLongLineDetail(t *testing.T) {
	t.Parallel()
	image := image.NewNRGBA(image.Rect(0, 0, 1000, 48))
	data, width := recognizerInput(image, recognizerConfig{Height: 48, Width: 320, MaxWidth: 3200})
	if width != 1000 || len(data) != 3*48*1000 {
		t.Fatalf("recognizer input width=%d len=%d", width, len(data))
	}
}

func TestDetectorRejectsThinRuleComponents(t *testing.T) {
	t.Parallel()
	values := make([]float32, 20*10)
	for x := 1; x < 19; x++ {
		values[5*20+x] = .9
	}
	regions := detectorRegions(values, 20, 10, detectorTransform{SourceWidth: 200, SourceHeight: 100, InputWidth: 20, InputHeight: 10}, detectorConfig{Threshold: .3, BoxThreshold: .6, UnclipRatio: 1.5})
	if len(regions) != 0 {
		t.Fatalf("thin table rule produced %d OCR regions", len(regions))
	}
}

func TestReadingOrderDeterministic(t *testing.T) {
	t.Parallel()
	regions := []paddleRegion{{Box: OCRBox{X: 100, Y: 52, Width: 20, Height: 20}}, {Box: OCRBox{X: 40, Y: 100, Width: 20, Height: 20}}, {Box: OCRBox{X: 10, Y: 50, Width: 20, Height: 22}}}
	sortPaddleRegions(regions)
	got := []string{}
	for _, r := range regions {
		got = append(got, fmt.Sprintf("%d:%d", r.Box.X, r.Box.Y))
	}
	if strings.Join(got, ",") != "10:50,100:52,40:100" {
		t.Fatalf("order=%v", got)
	}
}

func TestPaddleCanonicalRecognizeParity(t *testing.T) {
	t.Parallel()
	validated := paddleTestImage(t, 320, 160)
	newEngine := func() *PaddleEngine {
		engine := &PaddleEngine{root: paddleFakeModelRoot(t, "latin"), timeout: time.Second, documentProfile: defaultPaddleDocumentProfile()}
		engine.detectOverride = func(_ context.Context, _ image.Image, offset, original image.Point) ([]paddleRegion, detectorTransform, error) {
			transform := detectorTransform{SourceWidth: 320, SourceHeight: 160, InputWidth: 960, InputHeight: 480, ScaleX: 3, ScaleY: 3, OffsetX: float64(offset.X), OffsetY: float64(offset.Y), OriginalWidth: original.X, OriginalHeight: original.Y}
			return []paddleRegion{{Polygon: [4]paddlePoint{{10, 20}, {130, 20}, {130, 50}, {10, 50}}, Box: OCRBox{X: 10, Y: 20, Width: 120, Height: 30}, DetectorConfidence: .91}}, transform, nil
		}
		engine.recognizeOverride = func(_ context.Context, _ image.Image, plan recognizerPlan) (string, float64, string, error) {
			return "Technical manual", .84, plan.Names[0], nil
		}
		return engine
	}
	structuredEngine := newEngine()
	page, err := structuredEngine.RecognizeStructured(context.Background(), validated, "en")
	if err != nil {
		t.Fatal(err)
	}
	plainEngine := newEngine()
	text, err := plainEngine.Recognize(context.Background(), validated, "en")
	if err != nil {
		t.Fatal(err)
	}
	if text != plainText(page) {
		t.Fatalf("Recognize=%q plainText=%q", text, plainText(page))
	}
	pageAgain, err := newEngine().RecognizeStructured(context.Background(), validated, "en")
	if err != nil {
		t.Fatal(err)
	}
	left, _ := json.Marshal(page)
	right, _ := json.Marshal(pageAgain)
	if !bytes.Equal(left, right) {
		t.Fatalf("canonical pages differ:\n%s\n%s", left, right)
	}
	if len(page.Words) != 1 || page.Words[0].Box != (OCRBox{X: 10, Y: 20, Width: 120, Height: 30}) {
		t.Fatalf("page=%+v", page)
	}
	if page.Diagnostics.RequestedSource != "en" || page.Diagnostics.ResolvedOCRLanguage != "en" || page.Diagnostics.RecognizerModel != "latin_rec.onnx" {
		t.Fatalf("diagnostics=%+v", page.Diagnostics)
	}
}

func TestPaddleAutoAndConcreteLanguagePlansAreDeterministic(t *testing.T) {
	t.Parallel()
	auto, err := resolvePaddleRecognizerPlan("auto")
	if err != nil || auto.Resolved != "auto:per-region-script-selection" || len(auto.Names) != len(paddleRecognizerNames) {
		t.Fatalf("auto=%+v err=%v", auto, err)
	}
	english, err := resolvePaddleRecognizerPlan("en")
	if err != nil || english.Resolved != "en" || len(english.Names) != 1 || english.Names[0] != "latin" {
		t.Fatalf("en=%+v err=%v", english, err)
	}
}

func TestCoordinateTransformRoundTripAndConservativeBox(t *testing.T) {
	t.Parallel()
	transform := detectorTransform{SourceWidth: 640, SourceHeight: 480, InputWidth: 960, InputHeight: 736, ScaleX: 1.5, ScaleY: 1.5, PaddingLeft: 0, PaddingTop: 8, OffsetX: 100, OffsetY: 50, OriginalWidth: 1200, OriginalHeight: 900}
	source := paddlePoint{X: 123.25, Y: 77.75}
	tensor := paddlePoint{X: (source.X-transform.OffsetX)*transform.ScaleX + transform.PaddingLeft, Y: (source.Y-transform.OffsetY)*transform.ScaleY + transform.PaddingTop}
	projected := transform.sourcePoint(tensor)
	if math.Abs(projected.X-source.X) > 1e-6 || math.Abs(projected.Y-source.Y) > 1e-6 {
		t.Fatalf("projected=%+v source=%+v", projected, source)
	}
	box := polygonBox([4]paddlePoint{{10.8, 20.9}, {30.1, 20.9}, {30.1, 40.01}, {10.8, 40.01}}, 100, 100)
	if box != (OCRBox{X: 10, Y: 20, Width: 21, Height: 21}) {
		t.Fatalf("box=%+v", box)
	}
}

func TestMergePaddleRegionsUsesPolygonGeometryWithoutDuplicates(t *testing.T) {
	t.Parallel()
	full := paddleRegion{Polygon: [4]paddlePoint{{10, 10}, {110, 10}, {110, 30}, {10, 30}}, Box: OCRBox{X: 10, Y: 10, Width: 100, Height: 20}, Text: "Engine speed", RecognizerConfidence: .82, DetectorConfidence: .8, Pass: "full"}
	tile := paddleRegion{Polygon: [4]paddlePoint{{11, 10}, {111, 10}, {111, 30}, {11, 30}}, Box: OCRBox{X: 11, Y: 10, Width: 100, Height: 20}, Text: "Engine speed", RecognizerConfidence: .91, DetectorConfidence: .85, Pass: "tile-01"}
	other := paddleRegion{Polygon: [4]paddlePoint{{10, 40}, {90, 40}, {90, 60}, {10, 60}}, Box: OCRBox{X: 10, Y: 40, Width: 80, Height: 20}, Text: "Oil pressure", RecognizerConfidence: .9, Pass: "tile-01"}
	merged, duplicates := mergePaddleRegions([]paddleRegion{full, tile, other})
	if duplicates != 1 || len(merged) != 2 || merged[0].Pass != "tile-01" {
		t.Fatalf("merged=%+v duplicates=%d", merged, duplicates)
	}
}

func TestMergePaddleRegionsDropsContainedTileTextFragment(t *testing.T) {
	t.Parallel()
	full := paddleRegion{Polygon: [4]paddlePoint{{10, 10}, {210, 10}, {210, 30}, {10, 30}}, Box: OCRBox{X: 10, Y: 10, Width: 200, Height: 20}, Text: "Check the engine oil daily", RecognizerConfidence: .96, Pass: "full"}
	fragment := paddleRegion{Polygon: [4]paddlePoint{{150, 10}, {210, 10}, {210, 30}, {150, 30}}, Box: OCRBox{X: 150, Y: 10, Width: 60, Height: 20}, Text: "oil daily", RecognizerConfidence: .99, Pass: "tile-02"}
	merged, duplicates := mergePaddleRegions([]paddleRegion{full, fragment})
	if duplicates != 1 || len(merged) != 1 || merged[0].Text != full.Text {
		t.Fatalf("merged=%+v duplicates=%d", merged, duplicates)
	}
}

func TestPaddleFiltersLowConfidenceIsolatedNoiseButKeepsCallout(t *testing.T) {
	regions := []paddleRegion{
		{Text: "X", RecognizerConfidence: .42, DetectorConfidence: .60, Box: OCRBox{X: 10, Y: 10, Width: 12, Height: 14}},
		{Text: "-", RecognizerConfidence: .97, DetectorConfidence: .91, Box: OCRBox{X: 100, Y: 10, Width: 12, Height: 3}},
		{Text: "a", RecognizerConfidence: .31, DetectorConfidence: .91, Box: OCRBox{X: 200, Y: 10, Width: 9, Height: 13}},
		{Text: "4", RecognizerConfidence: .49, DetectorConfidence: .62, Box: OCRBox{X: 300, Y: 10, Width: 10, Height: 14}},
		{Text: "A", RecognizerConfidence: .99, DetectorConfidence: .96, Box: OCRBox{X: 400, Y: 10, Width: 12, Height: 14}},
	}
	kept, rejected := filterNonSemanticPaddleRegions(regions)
	if len(kept) != 1 || kept[0].Text != "A" || len(rejected) != 4 {
		t.Fatalf("kept=%+v rejected=%+v", kept, rejected)
	}
}

func TestMergePaddleRegionsPreservesConflictingContainedText(t *testing.T) {
	t.Parallel()
	full := paddleRegion{Polygon: [4]paddlePoint{{10, 10}, {210, 10}, {210, 30}, {10, 30}}, Box: OCRBox{X: 10, Y: 10, Width: 200, Height: 20}, Text: "Engine oil capacity", RecognizerConfidence: .80}
	conflict := paddleRegion{Polygon: [4]paddlePoint{{120, 10}, {210, 10}, {210, 30}, {120, 30}}, Box: OCRBox{X: 120, Y: 10, Width: 90, Height: 20}, Text: "filter removed", RecognizerConfidence: .82}
	merged, duplicates := mergePaddleRegions([]paddleRegion{full, conflict})
	if duplicates != 0 || len(merged) != 2 {
		t.Fatalf("merged=%+v duplicates=%d", merged, duplicates)
	}
}

func TestMergePaddleRegionsDropsNearlyIdenticalOverlappingText(t *testing.T) {
	t.Parallel()
	first := paddleRegion{Polygon: [4]paddlePoint{{10, 10}, {180, 10}, {180, 30}, {10, 30}}, Box: OCRBox{X: 10, Y: 10, Width: 170, Height: 20}, Text: "Search by menu", RecognizerConfidence: .91}
	second := paddleRegion{Polygon: [4]paddlePoint{{13, 11}, {183, 11}, {183, 31}, {13, 31}}, Box: OCRBox{X: 13, Y: 11, Width: 170, Height: 20}, Text: "Search by menu", RecognizerConfidence: .99}
	merged, duplicates := mergePaddleRegions([]paddleRegion{first, second})
	if duplicates != 1 || len(merged) != 1 {
		t.Fatalf("merged=%+v duplicates=%d", merged, duplicates)
	}
}

func TestMergePaddleRegionsDropsBoundaryDuplicateWithThinOverlap(t *testing.T) {
	t.Parallel()
	first := paddleRegion{Polygon: [4]paddlePoint{{10, 10}, {150, 10}, {150, 35}, {10, 35}}, Box: OCRBox{X: 10, Y: 10, Width: 140, Height: 25}, Text: "Internal Price List", RecognizerConfidence: .97, Pass: "full"}
	second := paddleRegion{Polygon: [4]paddlePoint{{12, 32}, {152, 32}, {152, 57}, {12, 57}}, Box: OCRBox{X: 12, Y: 32, Width: 140, Height: 25}, Text: "Internal Price List", RecognizerConfidence: .95, Pass: "tile-02"}
	merged, duplicates := mergePaddleRegions([]paddleRegion{first, second})
	if duplicates != 1 || len(merged) != 1 {
		t.Fatalf("merged=%+v duplicates=%d", merged, duplicates)
	}
}

func TestPaddleTilePlanCoversDocumentWithinBudget(t *testing.T) {
	t.Parallel()
	profile := defaultPaddleDocumentProfile()
	tiles := paddleTileCrops(2480, 3508, profile, detectorTransform{InputWidth: 672, InputHeight: 960})
	if len(tiles) == 0 || len(tiles) > profile.MaximumTiles || len(tiles)+1 > profile.MaximumDetectorPasses {
		t.Fatalf("tiles=%+v", tiles)
	}
	for _, point := range []image.Point{{0, 0}, {2479, 0}, {0, 3507}, {2479, 3507}} {
		covered := false
		for _, tile := range tiles {
			if point.In(image.Rect(tile.X, tile.Y, tile.X+tile.Width, tile.Y+tile.Height)) {
				covered = true
				break
			}
		}
		if !covered {
			t.Fatalf("corner %v not covered by %+v", point, tiles)
		}
	}
}

func TestLowConfidencePaddleTextRemainsAcceptedButCleanupUnsafe(t *testing.T) {
	t.Parallel()
	validated := paddleTestImage(t, 200, 100)
	engine := &PaddleEngine{root: paddleFakeModelRoot(t, "latin"), timeout: time.Second, documentProfile: defaultPaddleDocumentProfile()}
	engine.detectOverride = func(_ context.Context, _ image.Image, offset, original image.Point) ([]paddleRegion, detectorTransform, error) {
		return []paddleRegion{{Polygon: [4]paddlePoint{{10, 10}, {80, 10}, {80, 30}, {10, 30}}, Box: OCRBox{X: 10, Y: 10, Width: 70, Height: 20}, DetectorConfidence: .8}}, detectorTransform{SourceWidth: 200, SourceHeight: 100, InputWidth: 960, InputHeight: 480, ScaleX: 4.8, ScaleY: 4.8, OriginalWidth: original.X, OriginalHeight: original.Y}, nil
	}
	engine.recognizeOverride = func(context.Context, image.Image, recognizerPlan) (string, float64, string, error) {
		return "small text", .42, "latin", nil
	}
	page, err := engine.RecognizeStructured(context.Background(), validated, "en")
	if err != nil || len(page.Paragraphs) != 1 || len(page.Words) != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	word := page.Words[0]
	if !word.Accepted || !word.TextAccepted || word.CleanupSafe {
		t.Fatalf("word=%+v", word)
	}
}

func paddleTestImage(t *testing.T, width, height int) translation.ValidatedImage {
	t.Helper()
	source := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			source.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, source); err != nil {
		t.Fatal(err)
	}
	return translation.ValidatedImage{Data: buffer.Bytes(), MediaType: "image/png", Width: width, Height: height}
}

func paddleFakeModelRoot(t *testing.T, names ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range names {
		for _, suffix := range []string{"_rec.onnx", "_rec.yml"} {
			if err := os.WriteFile(filepath.Join(root, name+suffix), []byte("test"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}
