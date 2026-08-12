package ocr

import (
	"context"
	"fmt"
	"image"
	"math"

	ort "github.com/yalue/onnxruntime_go"
)

func (e *PaddleEngine) detect(ctx context.Context, source image.Image, offset image.Point, original image.Point) ([]paddleRegion, detectorTransform, error) {
	if e.detectOverride != nil {
		return e.detectOverride(ctx, source, offset, original)
	}
	data, transform := detectorInput(source, e.detectorConfig)
	transform.OffsetX, transform.OffsetY = float64(offset.X), float64(offset.Y)
	transform.OriginalWidth, transform.OriginalHeight = original.X, original.Y
	input, err := ort.NewTensor(ort.NewShape(1, 3, int64(transform.InputHeight), int64(transform.InputWidth)), data)
	if err != nil {
		return nil, transform, fmt.Errorf("create PaddleOCR detector tensor: %w", err)
	}
	defer input.Destroy()
	e.detectorGate.Lock()
	defer e.detectorGate.Unlock()
	if e.detector == nil {
		return nil, transform, errorsNewClosedPaddle()
	}
	output, err := runSession(ctx, e.detector, input)
	if err != nil {
		return nil, transform, fmt.Errorf("PaddleOCR detector inference failed: %w", err)
	}
	defer output.Destroy()
	shape := output.GetShape()
	if len(shape) != 4 || shape[0] != 1 || shape[1] != 1 || shape[2] <= 0 || shape[3] <= 0 {
		return nil, transform, fmt.Errorf("PaddleOCR model output has unexpected shape %v", shape)
	}
	return detectorRegions(output.GetData(), int(shape[3]), int(shape[2]), transform, e.detectorConfig), transform, nil
}

func errorsNewClosedPaddle() error { return fmt.Errorf("PaddleOCR engine is closed") }

func detectorInput(source image.Image, config detectorConfig) ([]float32, detectorTransform) {
	bounds := source.Bounds()
	sourceWidth, sourceHeight := bounds.Dx(), bounds.Dy()
	scale := float64(config.ResizeLong) / float64(maximum(sourceWidth, sourceHeight))
	width := maximum(32, int(math.Round(float64(sourceWidth)*scale/32))*32)
	height := maximum(32, int(math.Round(float64(sourceHeight)*scale/32))*32)
	data := make([]float32, 3*width*height)
	for y := 0; y < height; y++ {
		sy := float64(bounds.Min.Y) + (float64(y)+.5)*float64(sourceHeight)/float64(height) - .5
		for x := 0; x < width; x++ {
			sx := float64(bounds.Min.X) + (float64(x)+.5)*float64(sourceWidth)/float64(width) - .5
			pixel := bilinearColor(source, sx, sy)
			offset := y*width + x
			channels := [3]float32{float32(pixel.B) / 255, float32(pixel.G) / 255, float32(pixel.R) / 255}
			for channel := 0; channel < 3; channel++ {
				data[channel*width*height+offset] = (channels[channel] - config.Mean[channel]) / config.Std[channel]
			}
		}
	}
	return data, detectorTransform{SourceWidth: sourceWidth, SourceHeight: sourceHeight, InputWidth: width, InputHeight: height, ScaleX: float64(width) / float64(sourceWidth), ScaleY: float64(height) / float64(sourceHeight)}
}

type detectorPixel struct{ x, y int }

func detectorRegions(probabilities []float32, width, height int, transform detectorTransform, config detectorConfig) []paddleRegion {
	visited := make([]bool, width*height)
	regions := make([]paddleRegion, 0)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			index := y*width + x
			if visited[index] || probabilities[index] < config.Threshold {
				continue
			}
			pixels := detectorComponent(probabilities, visited, width, height, x, y, config.Threshold)
			componentWidth, componentHeight := detectorComponentSize(pixels)
			if componentWidth < 3 || componentHeight < 3 {
				continue
			}
			score := detectorBoxScore(probabilities, width, height, pixels)
			if len(pixels) < 3 || score < config.BoxThreshold {
				continue
			}
			polygon := componentQuad(pixels, float64(transform.InputWidth)/float64(width), float64(transform.InputHeight)/float64(height), config.UnclipRatio)
			region := sourceRegion(polygon, transform)
			region.DetectorConfidence = float64(score)
			if region.Box.Width > 1 && region.Box.Height > 1 {
				regions = append(regions, region)
			}
			if len(regions) >= config.MaxCandidates && config.MaxCandidates > 0 {
				return regions
			}
		}
	}
	return regions
}

func detectorComponentSize(pixels []detectorPixel) (int, int) {
	minX, minY, maxX, maxY := pixels[0].x, pixels[0].y, pixels[0].x, pixels[0].y
	for _, pixel := range pixels[1:] {
		minX, minY = minimum(minX, pixel.x), minimum(minY, pixel.y)
		maxX, maxY = maximum(maxX, pixel.x), maximum(maxY, pixel.y)
	}
	return maxX - minX + 1, maxY - minY + 1
}

func detectorComponent(values []float32, visited []bool, width, height, startX, startY int, threshold float32) []detectorPixel {
	queue := []detectorPixel{{startX, startY}}
	visited[startY*width+startX] = true
	pixels := make([]detectorPixel, 0, 64)
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		pixels = append(pixels, p)
		for _, d := range [][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
			x, y := p.x+d[0], p.y+d[1]
			if x < 0 || x >= width || y < 0 || y >= height {
				continue
			}
			index := y*width + x
			if !visited[index] && values[index] >= threshold {
				visited[index] = true
				queue = append(queue, detectorPixel{x, y})
			}
		}
	}
	return pixels
}

func detectorBoxScore(values []float32, width, height int, pixels []detectorPixel) float32 {
	minX, minY, maxX, maxY := pixels[0].x, pixels[0].y, pixels[0].x, pixels[0].y
	for _, pixel := range pixels[1:] {
		minX, minY = minimum(minX, pixel.x), minimum(minY, pixel.y)
		maxX, maxY = maximum(maxX, pixel.x), maximum(maxY, pixel.y)
	}
	var total float64
	count := 0
	for y := clamp(minY, 0, height-1); y <= clamp(maxY, 0, height-1); y++ {
		for x := clamp(minX, 0, width-1); x <= clamp(maxX, 0, width-1); x++ {
			total += float64(values[y*width+x])
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return float32(total / float64(count))
}

// componentQuad approximates DB contours with a deterministic PCA-oriented box,
// then applies the YAML unclip ratio around its center.
func componentQuad(pixels []detectorPixel, scaleX, scaleY, unclip float64) [4]paddlePoint {
	var cx, cy float64
	for _, p := range pixels {
		cx += float64(p.x) + .5
		cy += float64(p.y) + .5
	}
	cx /= float64(len(pixels))
	cy /= float64(len(pixels))
	var xx, xy, yy float64
	for _, p := range pixels {
		x, y := float64(p.x)+.5-cx, float64(p.y)+.5-cy
		xx += x * x
		xy += x * y
		yy += y * y
	}
	angle := .5 * math.Atan2(2*xy, xx-yy)
	cosine, sine := math.Cos(angle), math.Sin(angle)
	minU, minV, maxU, maxV := math.MaxFloat64, math.MaxFloat64, -math.MaxFloat64, -math.MaxFloat64
	for _, p := range pixels {
		x, y := float64(p.x)+.5-cx, float64(p.y)+.5-cy
		u, v := x*cosine+y*sine, -x*sine+y*cosine
		minU = math.Min(minU, u)
		maxU = math.Max(maxU, u)
		minV = math.Min(minV, v)
		maxV = math.Max(maxV, v)
	}
	boxWidth, boxHeight := maxU-minU+1, maxV-minV+1
	// DBPostProcess expands by area*unclip_ratio/perimeter, not by scaling
	// each side by unclip_ratio. Scaling severely over-crops long text lines.
	padding := boxWidth * boxHeight * unclip / maximumFloat(1, 2*(boxWidth+boxHeight))
	minU -= padding
	maxU += padding
	minV -= padding
	maxV += padding
	uv := [4][2]float64{{minU, minV}, {maxU, minV}, {maxU, maxV}, {minU, maxV}}
	var result [4]paddlePoint
	for i, q := range uv {
		x := cx + q[0]*cosine - q[1]*sine
		y := cy + q[0]*sine + q[1]*cosine
		result[i] = paddlePoint{X: x * scaleX, Y: y * scaleY}
	}
	return result
}

func maximumFloat(left, right float64) float64 {
	if left > right {
		return left
	}
	return right
}
