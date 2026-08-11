package inpaint

import (
	"image"
	"image/color"
	"testing"
)

func TestPreprocessPreservesAspectRatioUsesBGRAndBinaryMask(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	source.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	source.SetNRGBA(1, 0, color.NRGBA{B: 255, A: 255})
	mask := image.NewGray(source.Bounds())
	mask.SetGray(0, 0, color.Gray{Y: 12})
	imageData, maskData, transform, err := preprocess(source, mask)
	if err != nil {
		t.Fatal(err)
	}
	if transform.resizedWidth != 512 || transform.resizedHeight != 256 || transform.padLeft != 0 || transform.padTop != 128 {
		t.Fatalf("transform=%+v", transform)
	}
	pixels := modelSize * modelSize
	redIndex := transform.padTop*modelSize + 64
	blueIndex := transform.padTop*modelSize + 448
	if imageData[redIndex] != 0 || imageData[pixels*2+redIndex] < 0.99 || imageData[blueIndex] < 0.99 || imageData[pixels*2+blueIndex] != 0 {
		t.Fatalf("unexpected BGR channels: red=(%f,%f) blue=(%f,%f)", imageData[redIndex], imageData[pixels*2+redIndex], imageData[blueIndex], imageData[pixels*2+blueIndex])
	}
	for _, value := range maskData {
		if value != 0 && value != 1 {
			t.Fatalf("non-binary mask value %f", value)
		}
	}
	if maskData[redIndex] != 1 || maskData[blueIndex] != 0 {
		t.Fatalf("mask red=%f blue=%f", maskData[redIndex], maskData[blueIndex])
	}
}

func TestResizeCoordinateRoundTripAndEdgeShapes(t *testing.T) {
	for _, size := range []image.Point{{X: 1000, Y: 100}, {X: 100, Y: 1000}, {X: 512, Y: 512}, {X: 1, Y: 1}} {
		transform, err := newResizeTransform(size.X, size.Y)
		if err != nil {
			t.Fatal(err)
		}
		if transform.resizedWidth > modelSize || transform.resizedHeight > modelSize || (transform.resizedWidth != modelSize && transform.resizedHeight != modelSize) {
			t.Fatalf("size=%v transform=%+v", size, transform)
		}
		point := image.Pt(size.X/2, size.Y/2)
		roundTrip := transform.modelToSource(transform.sourceToModel(point))
		if abs(roundTrip.X-point.X) > 2 || abs(roundTrip.Y-point.Y) > 2 {
			t.Fatalf("size=%v point=%v roundTrip=%v", size, point, roundTrip)
		}
	}
}

func TestPostprocessChangesOnlyMaskAndPreservesAlpha(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 4, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 4; x++ {
			source.SetNRGBA(x, y, color.NRGBA{R: 10, G: 20, B: 30, A: uint8(60 + x + y)})
		}
	}
	mask := image.NewGray(source.Bounds())
	mask.SetGray(2, 1, color.Gray{Y: 255})
	transform, _ := newResizeTransform(4, 3)
	pixels := modelSize * modelSize
	output := make([]float32, pixels*3)
	for index := 0; index < pixels; index++ {
		output[index] = 200
		output[pixels+index] = 150
		output[pixels*2+index] = 100
	}
	result, err := postprocess(source, mask, output, transform)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.NRGBAAt(0, 0); got != source.NRGBAAt(0, 0) {
		t.Fatalf("outside=%+v want=%+v", got, source.NRGBAAt(0, 0))
	}
	if got := result.NRGBAAt(2, 1); got != (color.NRGBA{R: 100, G: 150, B: 200, A: source.NRGBAAt(2, 1).A}) {
		t.Fatalf("inside=%+v", got)
	}
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
