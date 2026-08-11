package imagebatch

import (
	"context"
	"errors"
	"image"
	"image/color"
	"sync"
	"testing"

	"github.com/sympllate/translator/internal/inpaint"
	"github.com/sympllate/translator/internal/ocr"
)

type fakeInpaintEngine struct {
	mu        sync.Mutex
	calls     int
	closed    bool
	err       error
	fillColor color.NRGBA
}

func (f *fakeInpaintEngine) Inpaint(ctx context.Context, source *image.NRGBA, mask *image.Gray) (inpaint.Result, error) {
	if err := ctx.Err(); err != nil {
		return inpaint.Result{}, err
	}
	f.mu.Lock()
	f.calls++
	err := f.err
	fill := f.fillColor
	f.mu.Unlock()
	if err != nil {
		return inpaint.Result{}, err
	}
	if fill == (color.NRGBA{}) {
		fill = color.NRGBA{R: 25, G: 180, B: 90, A: 255}
	}
	result := cloneNRGBA(source)
	for y := mask.Bounds().Min.Y; y < mask.Bounds().Max.Y; y++ {
		for x := mask.Bounds().Min.X; x < mask.Bounds().Max.X; x++ {
			if mask.GrayAt(x, y).Y != 0 {
				value := fill
				value.A = source.NRGBAAt(x, y).A
				result.SetNRGBA(x, y, value)
			}
		}
	}
	return inpaint.Result{Image: result}, nil
}

func (f *fakeInpaintEngine) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeInpaintEngine) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestCleanupClassificationFixtures(t *testing.T) {
	tests := []struct {
		name   string
		image  *image.NRGBA
		box    ocr.OCRBox
		wanted CleanupMode
	}{
		{"white uniform", solidNRGBA(160, 120, color.NRGBA{R: 255, G: 255, B: 255, A: 255}), ocr.OCRBox{X: 60, Y: 45, Width: 40, Height: 30}, CleanupSolid},
		{"colored uniform", solidNRGBA(160, 120, color.NRGBA{R: 35, G: 105, B: 190, A: 255}), ocr.OCRBox{X: 60, Y: 45, Width: 40, Height: 30}, CleanupSolid},
		{"gradient", gradientFixture(160, 120), ocr.OCRBox{X: 60, Y: 45, Width: 40, Height: 30}, CleanupNeural},
		{"button border", buttonFixture(160, 120), ocr.OCRBox{X: 55, Y: 45, Width: 50, Height: 30}, CleanupNeural},
		{"texture", textureFixture(160, 120), ocr.OCRBox{X: 60, Y: 45, Width: 40, Height: 30}, CleanupNeural},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sample, err := SampleBackground(context.Background(), test.image, boxFromOCR(test.box), backgroundSampleWidth, minimumBackgroundSamples, maximumBackgroundSamples)
			if err != nil {
				t.Fatal(err)
			}
			if got := cleanupModeFor(sample); got != test.wanted {
				t.Fatalf("mode=%s variance=%.2f sample=%+v", got, sample.Variance, sample)
			}
		})
	}
}

func TestTextMaskSelectsForegroundDilatesAndAvoidsNeighbor(t *testing.T) {
	source := solidNRGBA(40, 20, color.NRGBA{R: 245, G: 245, B: 245, A: 255})
	for y := 6; y <= 12; y++ {
		source.SetNRGBA(10, y, color.NRGBA{A: 255})
	}
	for x := 10; x <= 15; x++ {
		source.SetNRGBA(x, 9, color.NRGBA{A: 255})
	}
	for y := 6; y <= 12; y++ {
		source.SetNRGBA(24, y, color.NRGBA{R: 200, A: 255})
	}
	mask, err := buildTextMask(context.Background(), source, RenderBlock{
		ID: "one", SourceBox: ocr.OCRBox{X: 8, Y: 4, Width: 10, Height: 11},
		Background: newRenderColor(color.NRGBA{R: 245, G: 245, B: 245, A: 255}),
		Foreground: newRenderColor(color.NRGBA{A: 255}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if mask.GrayAt(10, 6).Y != 255 || mask.GrayAt(9, 6).Y != 255 {
		t.Fatal("text seed or one-pixel dilation is missing")
	}
	if image.Pt(24, 9).In(mask.Bounds()) && mask.GrayAt(24, 9).Y != 0 {
		t.Fatal("neighboring OCR text was included")
	}
}

func TestDilationRadiusIsBounded(t *testing.T) {
	seed := image.NewGray(image.Rect(0, 0, 7, 7))
	seed.SetGray(3, 3, color.Gray{Y: 255})
	mask := dilateMask(seed, 1)
	if mask.GrayAt(2, 2).Y != 255 || mask.GrayAt(4, 4).Y != 255 || mask.GrayAt(1, 3).Y != 0 {
		t.Fatalf("unexpected dilation mask: near=%d far=%d", mask.GrayAt(2, 2).Y, mask.GrayAt(1, 3).Y)
	}
}

func TestCropClippingAndClusterMerging(t *testing.T) {
	bounds := image.Rect(0, 0, 400, 300)
	if got := expandRectangle(image.Rect(0, 0, 20, 10), neuralContextPadding, bounds); got != image.Rect(0, 0, 68, 58) {
		t.Fatalf("clipped crop=%v", got)
	}
	regions := []maskedRegion{
		{bounds: image.Rect(20, 20, 40, 35)},
		{bounds: image.Rect(60, 22, 85, 38)},
		{bounds: image.Rect(330, 240, 360, 260)},
	}
	clusters := clusterMaskedRegions(regions, bounds)
	if len(clusters) != 2 || len(clusters[0].indices) != 2 || len(clusters[1].indices) != 1 {
		t.Fatalf("clusters=%+v", clusters)
	}
	farWide := []maskedRegion{{bounds: image.Rect(0, 20, 20, 40)}, {bounds: image.Rect(1500, 20, 1520, 40)}}
	if got := clusterMaskedRegions(farWide, image.Rect(0, 0, 2000, 300)); len(got) != 2 {
		t.Fatalf("distant regions merged: %+v", got)
	}
}

func TestLargeSingleOCRMaskIsSplitBeforeClustering(t *testing.T) {
	mask := image.NewGray(image.Rect(0, 0, 2480, 3508))
	for y := 20; y < 3480; y += 40 {
		for x := 20; x < 2460; x += 40 {
			mask.SetGray(x, y, color.Gray{Y: 255})
		}
	}
	regions := splitMaskedRegion(mask)
	if len(regions) < 8 {
		t.Fatalf("large page mask was not tiled: regions=%d", len(regions))
	}
	clusters := clusterMaskedRegions(regions, mask.Bounds())
	for _, cluster := range clusters {
		crop := expandRectangle(cluster.bounds, neuralContextPadding, mask.Bounds())
		if crop.Dx() > maximumClusterDimension || crop.Dy() > maximumClusterDimension || crop.Dx()*crop.Dy() > maximumClusterArea {
			t.Fatalf("oversized crop=%v", crop)
		}
	}
}

func TestHybridCleanupRoutesUniformAndNeuralAndPreservesOutsideMask(t *testing.T) {
	source := textureFixture(160, 100)
	for y := 35; y < 55; y++ {
		for x := 40; x < 44; x++ {
			source.SetNRGBA(x, y, color.NRGBA{A: 77})
		}
	}
	engine := &fakeInpaintEngine{}
	renderer, err := NewRenderer(t.TempDir(), DefaultRenderConfig(), engine)
	if err != nil {
		t.Fatal(err)
	}
	document := RenderDocument{Blocks: []RenderBlock{
		{ID: "solid", SourceBox: ocr.OCRBox{X: 100, Y: 10, Width: 15, Height: 10}, CleanupBox: ocr.OCRBox{X: 99, Y: 9, Width: 17, Height: 12}, Background: newRenderColor(color.NRGBA{R: 12, G: 34, B: 56, A: 255}), Foreground: newRenderColor(color.NRGBA{A: 255}), CleanupMode: CleanupSolid},
		{ID: "neural", SourceBox: ocr.OCRBox{X: 35, Y: 30, Width: 20, Height: 30}, CleanupBox: ocr.OCRBox{X: 34, Y: 29, Width: 22, Height: 32}, Background: newRenderColor(color.NRGBA{R: 210, G: 210, B: 210, A: 255}), Foreground: newRenderColor(color.NRGBA{A: 255}), CleanupMode: CleanupNeural},
	}}
	originalOutside := source.NRGBAAt(10, 10)
	cleaned, stats, err := renderer.Clean(context.Background(), source, document)
	if err != nil {
		t.Fatal(err)
	}
	if engine.callCount() != 1 || stats.UniformRegions != 1 || stats.NeuralRegions != 1 || stats.NeuralClusters != 1 {
		t.Fatalf("calls=%d stats=%+v", engine.callCount(), stats)
	}
	if got := cleaned.NRGBAAt(10, 10); got != originalOutside {
		t.Fatalf("outside changed: got=%+v want=%+v", got, originalOutside)
	}
	if got := cleaned.NRGBAAt(40, 40); got.A != 77 || got.G != 180 {
		t.Fatalf("neural pixel=%+v", got)
	}
	if got := cleaned.NRGBAAt(100, 10); got != (color.NRGBA{R: 12, G: 34, B: 56, A: 255}) {
		t.Fatalf("solid pixel=%+v", got)
	}
}

func TestUniformCleanupDoesNotInvokeInpaintAndNeuralErrorsAreReturned(t *testing.T) {
	source := solidNRGBA(30, 20, color.NRGBA{R: 240, G: 240, B: 240, A: 255})
	engine := &fakeInpaintEngine{}
	renderer, _ := NewRenderer(t.TempDir(), DefaultRenderConfig(), engine)
	block := RenderBlock{ID: "text", SourceBox: ocr.OCRBox{X: 5, Y: 5, Width: 10, Height: 5}, CleanupBox: ocr.OCRBox{X: 4, Y: 4, Width: 12, Height: 7}, Background: newRenderColor(color.NRGBA{R: 240, G: 240, B: 240, A: 255}), Foreground: newRenderColor(color.NRGBA{A: 255}), CleanupMode: CleanupSolid}
	if _, _, err := renderer.Clean(context.Background(), source, RenderDocument{Blocks: []RenderBlock{block}}); err != nil {
		t.Fatal(err)
	}
	if engine.callCount() != 0 {
		t.Fatal("uniform cleanup invoked LaMa")
	}
	for x := 6; x < 12; x++ {
		source.SetNRGBA(x, 7, color.NRGBA{A: 255})
	}
	engine.err = errors.New("inference failed")
	block.CleanupMode = CleanupNeural
	if _, _, err := renderer.Clean(context.Background(), source, RenderDocument{Blocks: []RenderBlock{block}}); err == nil || !errors.Is(err, engine.err) {
		t.Fatalf("err=%v", err)
	}
}

func gradientFixture(width, height int) *image.NRGBA {
	result := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			value := uint8(100 + x*60/max(1, width-1))
			result.SetNRGBA(x, y, color.NRGBA{R: value, G: value, B: value, A: 255})
		}
	}
	return result
}

func buttonFixture(width, height int) *image.NRGBA {
	result := solidNRGBA(width, height, color.NRGBA{R: 245, G: 245, B: 245, A: 255})
	for y := 28; y < 92; y++ {
		for x := 40; x < 120; x++ {
			value := color.NRGBA{R: 70, G: 125, B: 210, A: 255}
			if x == 40 || x == 119 || y == 28 || y == 91 {
				value = color.NRGBA{R: 20, G: 45, B: 90, A: 255}
			}
			result.SetNRGBA(x, y, value)
		}
	}
	return result
}

func textureFixture(width, height int) *image.NRGBA {
	result := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			value := uint8(70)
			if (x/3+y/3)%2 == 0 {
				value = 210
			}
			result.SetNRGBA(x, y, color.NRGBA{R: value, G: uint8(255 - int(value)/2), B: 120, A: 255})
		}
	}
	return result
}
