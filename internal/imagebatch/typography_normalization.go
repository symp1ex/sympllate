package imagebatch

import (
	"math"

	"github.com/sympllate/translator/internal/ocr"
)

// normalizeLocalTypography removes isolated estimates that are inconsistent
// with nearby lines of the same observed geometry. It deliberately uses line
// boxes rather than paragraph height, so multi-line paragraphs do not look
// like headings merely because their union box is tall.
func normalizeLocalTypography(paragraphs []ocr.OCRParagraph, estimates []FontStyleEstimate, eligible []bool, transform CoordinateTransform) {
	for index, paragraph := range paragraphs {
		if !eligible[index] || estimates[index].FontSize <= 0 {
			continue
		}
		ownHeight := paragraphMedianLineHeight(paragraph, transform)
		if ownHeight <= 0 {
			continue
		}
		neighborSizes := make([]float64, 0, 4)
		neighborHeights := make([]float64, 0, 4)
		for other := range paragraphs {
			if other == index || !eligible[other] || estimates[other].FontSize <= 0 || !typographicNeighbors(paragraph, paragraphs[other], transform) {
				continue
			}
			height := paragraphMedianLineHeight(paragraphs[other], transform)
			if height <= 0 || math.Max(ownHeight, height)/math.Min(ownHeight, height) > 1.35 {
				continue
			}
			neighborSizes = append(neighborSizes, estimates[other].FontSize)
			neighborHeights = append(neighborHeights, height)
		}
		if len(neighborSizes) < 2 {
			continue
		}
		localSize, localHeight := median(neighborSizes), median(neighborHeights)
		if ownHeight > localHeight*1.35 { // retain genuinely larger heading geometry
			continue
		}
		current := estimates[index].FontSize
		if current <= localSize*1.55 && current >= localSize*.65 {
			continue
		}
		estimates[index].InitialFontSize = current
		estimates[index].FontSize = roundFontSize(localSize)
		estimates[index].Normalized = true
		estimates[index].NormalizationReason = "local_line_geometry_outlier"
	}
}

func paragraphMedianLineHeight(paragraph ocr.OCRParagraph, transform CoordinateTransform) float64 {
	heights := make([]float64, 0, len(paragraph.Lines))
	for _, line := range paragraph.Lines {
		box := TransformBox(line.Box, transform)
		if box.Height > 0 {
			heights = append(heights, float64(box.Height))
		}
	}
	if len(heights) == 0 {
		lines := max(1, sourceLineCount(paragraph))
		box := TransformBox(paragraph.Box, transform)
		if box.Height > 0 {
			return float64(box.Height) / float64(lines)
		}
		return 0
	}
	return median(heights)
}

func typographicNeighbors(left, right ocr.OCRParagraph, transform CoordinateTransform) bool {
	a, b := TransformBox(left.Box, transform), TransformBox(right.Box, transform)
	if a.Width <= 0 || a.Height <= 0 || b.Width <= 0 || b.Height <= 0 {
		return false
	}
	lineHeight := math.Max(paragraphMedianLineHeight(left, transform), paragraphMedianLineHeight(right, transform))
	verticalGap := max(0, max(a.Y-b.Y-b.Height, b.Y-a.Y-a.Height))
	horizontalGap := max(0, max(a.X-b.X-b.Width, b.X-a.X-a.Width))
	xOverlap := overlapPixels(a.X, a.X+a.Width, b.X, b.X+b.Width)
	sameColumn := xOverlap > 0 || absInt(a.X-b.X) <= int(lineHeight*3)
	sameRow := overlapPixels(a.Y, a.Y+a.Height, b.Y, b.Y+b.Height) > 0 && horizontalGap <= int(lineHeight*8)
	return sameRow || (sameColumn && verticalGap <= int(lineHeight*5))
}
