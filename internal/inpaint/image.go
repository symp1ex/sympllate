package inpaint

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"math"

	xdraw "golang.org/x/image/draw"
)

const imageScale = float32(0.00392)

type resizeTransform struct {
	sourceWidth, sourceHeight   int
	resizedWidth, resizedHeight int
	padLeft, padTop             int
}

func newResizeTransform(width, height int) (resizeTransform, error) {
	if width <= 0 || height <= 0 {
		return resizeTransform{}, errors.New("LaMa source image is empty")
	}
	scale := math.Min(float64(modelSize)/float64(width), float64(modelSize)/float64(height))
	resizedWidth := max(1, min(modelSize, int(math.Round(float64(width)*scale))))
	resizedHeight := max(1, min(modelSize, int(math.Round(float64(height)*scale))))
	return resizeTransform{
		sourceWidth: width, sourceHeight: height,
		resizedWidth: resizedWidth, resizedHeight: resizedHeight,
		padLeft: (modelSize - resizedWidth) / 2,
		padTop:  (modelSize - resizedHeight) / 2,
	}, nil
}

func (t resizeTransform) modelRect() image.Rectangle {
	return image.Rect(t.padLeft, t.padTop, t.padLeft+t.resizedWidth, t.padTop+t.resizedHeight)
}

func (t resizeTransform) sourceToModel(point image.Point) image.Point {
	x := t.padLeft + int(math.Round((float64(point.X)+0.5)*float64(t.resizedWidth)/float64(t.sourceWidth)-0.5))
	y := t.padTop + int(math.Round((float64(point.Y)+0.5)*float64(t.resizedHeight)/float64(t.sourceHeight)-0.5))
	return image.Pt(x, y)
}

func (t resizeTransform) modelToSource(point image.Point) image.Point {
	x := int(math.Round((float64(point.X-t.padLeft)+0.5)*float64(t.sourceWidth)/float64(t.resizedWidth) - 0.5))
	y := int(math.Round((float64(point.Y-t.padTop)+0.5)*float64(t.sourceHeight)/float64(t.resizedHeight) - 0.5))
	return image.Pt(min(t.sourceWidth-1, max(0, x)), min(t.sourceHeight-1, max(0, y)))
}

func preprocess(source *image.NRGBA, mask *image.Gray) ([]float32, []float32, resizeTransform, error) {
	if source == nil || mask == nil {
		return nil, nil, resizeTransform{}, errors.New("LaMa source and mask are required")
	}
	if source.Bounds() != mask.Bounds() {
		return nil, nil, resizeTransform{}, errors.New("LaMa source and mask bounds differ")
	}
	transform, err := newResizeTransform(source.Bounds().Dx(), source.Bounds().Dy())
	if err != nil {
		return nil, nil, resizeTransform{}, err
	}
	canvas := image.NewNRGBA(image.Rect(0, 0, modelSize, modelSize))
	xdraw.CatmullRom.Scale(canvas, transform.modelRect(), source, source.Bounds(), draw.Src, nil)
	fillImagePadding(canvas, transform.modelRect())
	maskCanvas := image.NewGray(canvas.Bounds())
	xdraw.NearestNeighbor.Scale(maskCanvas, transform.modelRect(), mask, mask.Bounds(), draw.Src, nil)

	pixels := modelSize * modelSize
	imageData := make([]float32, pixels*3)
	maskData := make([]float32, pixels)
	for y := 0; y < modelSize; y++ {
		for x := 0; x < modelSize; x++ {
			index := y*modelSize + x
			pixel := canvas.NRGBAAt(x, y)
			imageData[index] = float32(pixel.B) * imageScale
			imageData[pixels+index] = float32(pixel.G) * imageScale
			imageData[pixels*2+index] = float32(pixel.R) * imageScale
			if maskCanvas.GrayAt(x, y).Y != 0 {
				maskData[index] = 1
			}
		}
	}
	return imageData, maskData, transform, nil
}

func fillImagePadding(canvas *image.NRGBA, content image.Rectangle) {
	for y := 0; y < modelSize; y++ {
		for x := 0; x < modelSize; x++ {
			if image.Pt(x, y).In(content) {
				continue
			}
			sx := min(content.Max.X-1, max(content.Min.X, x))
			sy := min(content.Max.Y-1, max(content.Min.Y, y))
			canvas.SetNRGBA(x, y, canvas.NRGBAAt(sx, sy))
		}
	}
}

func postprocess(source *image.NRGBA, mask *image.Gray, output []float32, transform resizeTransform) (*image.NRGBA, error) {
	pixels := modelSize * modelSize
	if len(output) != pixels*3 {
		return nil, errors.New("LaMa output tensor has an unexpected size")
	}
	canvas := image.NewNRGBA(image.Rect(0, 0, modelSize, modelSize))
	for y := 0; y < modelSize; y++ {
		for x := 0; x < modelSize; x++ {
			index := y*modelSize + x
			canvas.SetNRGBA(x, y, color.NRGBA{
				R: floatToByte(output[pixels*2+index]),
				G: floatToByte(output[pixels+index]),
				B: floatToByte(output[index]),
				A: 255,
			})
		}
	}
	restored := image.NewNRGBA(image.Rect(0, 0, transform.sourceWidth, transform.sourceHeight))
	xdraw.CatmullRom.Scale(restored, restored.Bounds(), canvas, transform.modelRect(), draw.Src, nil)
	result := image.NewNRGBA(source.Bounds())
	draw.Draw(result, result.Bounds(), source, source.Bounds().Min, draw.Src)
	for y := source.Bounds().Min.Y; y < source.Bounds().Max.Y; y++ {
		for x := source.Bounds().Min.X; x < source.Bounds().Max.X; x++ {
			if mask.GrayAt(x, y).Y == 0 {
				continue
			}
			pixel := restored.NRGBAAt(x-source.Bounds().Min.X, y-source.Bounds().Min.Y)
			pixel.A = source.NRGBAAt(x, y).A
			result.SetNRGBA(x, y, pixel)
		}
	}
	return result, nil
}

func floatToByte(value float32) uint8 {
	if math.IsNaN(float64(value)) || value <= 0 {
		return 0
	}
	if value >= 255 {
		return 255
	}
	return uint8(value + 0.5)
}
