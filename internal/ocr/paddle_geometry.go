package ocr

import (
	"image"
	"image/color"
	"math"
	"sort"
)

type paddlePoint struct{ X, Y float64 }
type paddleRegion struct {
	Polygon              [4]paddlePoint
	Box                  OCRBox
	DetectorConfidence   float64
	RecognizerConfidence float64
	Text, Recognizer     string
	Pass                 string
}

type detectorTransform struct {
	SourceWidth, SourceHeight     int
	InputWidth, InputHeight       int
	ScaleX, ScaleY                float64
	PaddingLeft, PaddingTop       float64
	OffsetX, OffsetY              float64
	OriginalWidth, OriginalHeight int
}

func (t detectorTransform) sourcePoint(point paddlePoint) paddlePoint {
	scaleX, scaleY := t.ScaleX, t.ScaleY
	if scaleX == 0 {
		scaleX = float64(t.InputWidth) / float64(t.SourceWidth)
	}
	if scaleY == 0 {
		scaleY = float64(t.InputHeight) / float64(t.SourceHeight)
	}
	x := (point.X-t.PaddingLeft)/scaleX + t.OffsetX
	y := (point.Y-t.PaddingTop)/scaleY + t.OffsetY
	width, height := t.OriginalWidth, t.OriginalHeight
	if width <= 0 {
		width = t.SourceWidth
	}
	if height <= 0 {
		height = t.SourceHeight
	}
	return paddlePoint{X: math.Max(0, math.Min(float64(width), x)), Y: math.Max(0, math.Min(float64(height), y))}
}

func sourceRegion(polygon [4]paddlePoint, transform detectorTransform) paddleRegion {
	for index := range polygon {
		polygon[index] = transform.sourcePoint(polygon[index])
	}
	width, height := transform.OriginalWidth, transform.OriginalHeight
	if width <= 0 {
		width = transform.SourceWidth
	}
	if height <= 0 {
		height = transform.SourceHeight
	}
	return paddleRegion{Polygon: polygon, Box: polygonBox(polygon, width, height)}
}

func publicPolygon(polygon [4]paddlePoint) OCRPolygon {
	var result OCRPolygon
	for index, point := range polygon {
		result[index] = OCRPoint{X: point.X, Y: point.Y}
	}
	return result
}

func polygonCenter(polygon [4]paddlePoint) paddlePoint {
	var center paddlePoint
	for _, point := range polygon {
		center.X += point.X
		center.Y += point.Y
	}
	center.X /= 4
	center.Y /= 4
	return center
}

func polygonArea(polygon [4]paddlePoint) float64 {
	area := 0.0
	for index, point := range polygon {
		next := polygon[(index+1)%len(polygon)]
		area += point.X*next.Y - next.X*point.Y
	}
	return math.Abs(area) / 2
}

// Convex clipping is sufficient for Paddle detector quads.
func polygonIntersectionArea(subject, clip [4]paddlePoint) float64 {
	points := append([]paddlePoint(nil), subject[:]...)
	clipSign := polygonSignedArea(clip)
	for edge := 0; edge < len(clip) && len(points) > 0; edge++ {
		a, b := clip[edge], clip[(edge+1)%len(clip)]
		input := points
		points = nil
		previous := input[len(input)-1]
		previousInside := cross(a, b, previous)*clipSign >= 0
		for _, current := range input {
			currentInside := cross(a, b, current)*clipSign >= 0
			if currentInside != previousInside {
				points = append(points, lineIntersection(previous, current, a, b))
			}
			if currentInside {
				points = append(points, current)
			}
			previous, previousInside = current, currentInside
		}
	}
	if len(points) < 3 {
		return 0
	}
	area := 0.0
	for index, point := range points {
		next := points[(index+1)%len(points)]
		area += point.X*next.Y - next.X*point.Y
	}
	return math.Abs(area) / 2
}

func polygonSignedArea(polygon [4]paddlePoint) float64 {
	area := 0.0
	for i, p := range polygon {
		n := polygon[(i+1)%4]
		area += p.X*n.Y - n.X*p.Y
	}
	if area < 0 {
		return -1
	}
	return 1
}
func cross(a, b, p paddlePoint) float64 { return (b.X-a.X)*(p.Y-a.Y) - (b.Y-a.Y)*(p.X-a.X) }
func lineIntersection(a, b, c, d paddlePoint) paddlePoint {
	abX, abY := b.X-a.X, b.Y-a.Y
	cdX, cdY := d.X-c.X, d.Y-c.Y
	denominator := abX*cdY - abY*cdX
	if math.Abs(denominator) < 1e-9 {
		return b
	}
	t := ((c.X-a.X)*cdY - (c.Y-a.Y)*cdX) / denominator
	return paddlePoint{X: a.X + t*abX, Y: a.Y + t*abY}
}

func polygonBox(polygon [4]paddlePoint, width, height int) OCRBox {
	minX, minY, maxX, maxY := polygon[0].X, polygon[0].Y, polygon[0].X, polygon[0].Y
	for _, p := range polygon[1:] {
		minX = math.Min(minX, p.X)
		minY = math.Min(minY, p.Y)
		maxX = math.Max(maxX, p.X)
		maxY = math.Max(maxY, p.Y)
	}
	x0, y0 := clamp(int(math.Floor(minX)), 0, width), clamp(int(math.Floor(minY)), 0, height)
	x1, y1 := clamp(int(math.Ceil(maxX)), x0, width), clamp(int(math.Ceil(maxY)), y0, height)
	return OCRBox{X: x0, Y: y0, Width: x1 - x0, Height: y1 - y0}
}

func sortPaddleRegions(regions []paddleRegion) {
	sort.SliceStable(regions, func(i, j int) bool {
		a, b := regions[i].Box, regions[j].Box
		ah, bh := maximum(1, a.Height), maximum(1, b.Height)
		if abs((a.Y+ah/2)-(b.Y+bh/2)) <= maximum(ah, bh)/2 {
			if a.X != b.X {
				return a.X < b.X
			}
		}
		if a.Y != b.Y {
			return a.Y < b.Y
		}
		if a.X != b.X {
			return a.X < b.X
		}
		return a.Width*a.Height < b.Width*b.Height
	})
}

func rectifyRegion(source image.Image, region paddleRegion) *image.NRGBA {
	p := region.Polygon
	w := maximum(1, int(math.Round(math.Max(distance(p[0], p[1]), distance(p[3], p[2])))))
	h := maximum(1, int(math.Round(math.Max(distance(p[0], p[3]), distance(p[1], p[2])))))
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		v := (float64(y) + .5) / float64(h)
		for x := 0; x < w; x++ {
			u := (float64(x) + .5) / float64(w)
			q := bilinearQuad(p, u, v)
			out.Set(x, y, bilinearColor(source, q.X, q.Y))
		}
	}
	if h > w*3/2 {
		return rotateCrop(out)
	}
	return out
}

func distance(a, b paddlePoint) float64 { return math.Hypot(a.X-b.X, a.Y-b.Y) }
func bilinearQuad(p [4]paddlePoint, u, v float64) paddlePoint {
	return paddlePoint{X: (1-u)*(1-v)*p[0].X + u*(1-v)*p[1].X + u*v*p[2].X + (1-u)*v*p[3].X, Y: (1-u)*(1-v)*p[0].Y + u*(1-v)*p[1].Y + u*v*p[2].Y + (1-u)*v*p[3].Y}
}
func bilinearColor(src image.Image, x, y float64) color.NRGBA {
	b := src.Bounds()
	x = math.Max(float64(b.Min.X), math.Min(float64(b.Max.X-1), x))
	y = math.Max(float64(b.Min.Y), math.Min(float64(b.Max.Y-1), y))
	x0, y0 := int(math.Floor(x)), int(math.Floor(y))
	x1, y1 := minimum(x0+1, b.Max.X-1), minimum(y0+1, b.Max.Y-1)
	dx, dy := x-float64(x0), y-float64(y0)
	cs := []color.NRGBA{color.NRGBAModel.Convert(src.At(x0, y0)).(color.NRGBA), color.NRGBAModel.Convert(src.At(x1, y0)).(color.NRGBA), color.NRGBAModel.Convert(src.At(x1, y1)).(color.NRGBA), color.NRGBAModel.Convert(src.At(x0, y1)).(color.NRGBA)}
	weights := []float64{(1 - dx) * (1 - dy), dx * (1 - dy), dx * dy, (1 - dx) * dy}
	var r, g, bv, a float64
	for i, c := range cs {
		r += float64(c.R) * weights[i]
		g += float64(c.G) * weights[i]
		bv += float64(c.B) * weights[i]
		a += float64(c.A) * weights[i]
	}
	return color.NRGBA{R: uint8(r + .5), G: uint8(g + .5), B: uint8(bv + .5), A: uint8(a + .5)}
}
func rotateCrop(src *image.NRGBA) *image.NRGBA {
	b := src.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dy(), b.Dx()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			out.Set(b.Dy()-1-y, x, src.At(x, y))
		}
	}
	return out
}
