package imagebatch

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"strings"

	"github.com/sympllate/translator/internal/ocr"
)

func renderDebugImage(ctx context.Context, imageData []byte, page ocr.OCRPage, outputPath string) error {
	source, _, err := image.Decode(bytes.NewReader(imageData))
	if err != nil {
		return fmt.Errorf("decode debug image: %w", err)
	}
	canvas := image.NewRGBA(source.Bounds())
	draw.Draw(canvas, canvas.Bounds(), source, source.Bounds().Min, draw.Src)
	paragraphColor := color.RGBA{R: 255, G: 32, B: 32, A: 255}
	lineColor := color.RGBA{R: 32, G: 128, B: 255, A: 255}
	for _, paragraph := range page.Paragraphs {
		if err := ctx.Err(); err != nil {
			return err
		}
		drawBox(canvas, paragraph.Box, paragraphColor, 2)
		for _, line := range paragraph.Lines {
			drawBox(canvas, line.Box, lineColor, 1)
		}
		drawLabel(canvas, paragraph.Box.X+2, paragraph.Box.Y+2, paragraph.ID, paragraphColor)
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, canvas); err != nil {
		return fmt.Errorf("encode debug image: %w", err)
	}
	return atomicWriteBytes(outputPath, encoded.Bytes())
}

func drawBox(target *image.RGBA, box ocr.OCRBox, value color.RGBA, thickness int) {
	for offset := 0; offset < thickness; offset++ {
		left, top := box.X+offset, box.Y+offset
		right, bottom := box.X+box.Width-1-offset, box.Y+box.Height-1-offset
		for x := left; x <= right; x++ {
			setRGBA(target, x, top, value)
			setRGBA(target, x, bottom, value)
		}
		for y := top; y <= bottom; y++ {
			setRGBA(target, left, y, value)
			setRGBA(target, right, y, value)
		}
	}
}

func drawLabel(target *image.RGBA, x, y int, value string, foreground color.RGBA) {
	value = strings.ToUpper(value)
	width := len(value)*4 + 2
	background := color.RGBA{A: 220}
	for py := y; py < y+7; py++ {
		for px := x; px < x+width; px++ {
			setRGBA(target, px, py, background)
		}
	}
	for index, character := range value {
		glyph, ok := debugGlyphs[character]
		if !ok {
			glyph = debugGlyphs['?']
		}
		for row, bits := range glyph {
			for column := 0; column < 3; column++ {
				if bits&(1<<(2-column)) != 0 {
					setRGBA(target, x+1+index*4+column, y+1+row, foreground)
				}
			}
		}
	}
}

func setRGBA(target *image.RGBA, x, y int, value color.RGBA) {
	point := image.Pt(x, y)
	if point.In(target.Bounds()) {
		target.SetRGBA(x, y, value)
	}
}

var debugGlyphs = map[rune][5]byte{
	'0': {7, 5, 5, 5, 7}, '1': {2, 6, 2, 2, 7}, '2': {7, 1, 7, 4, 7},
	'3': {7, 1, 7, 1, 7}, '4': {5, 5, 7, 1, 1}, '5': {7, 4, 7, 1, 7},
	'6': {7, 4, 7, 5, 7}, '7': {7, 1, 2, 2, 2}, '8': {7, 5, 7, 5, 7},
	'9': {7, 5, 7, 1, 7}, 'P': {6, 5, 6, 4, 4}, 'B': {6, 5, 6, 5, 6},
	'A': {2, 5, 7, 5, 5}, 'R': {6, 5, 6, 5, 5}, 'L': {4, 4, 4, 4, 7},
	'E': {7, 4, 6, 4, 7}, 'F': {7, 4, 6, 4, 4},
	'-': {0, 0, 7, 0, 0}, '?': {7, 1, 3, 0, 2},
}
