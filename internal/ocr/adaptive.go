package ocr

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

const (
	// These limits bound decoded working images and cumulative sequential OCR
	// work independently of the accepted frontend image limits.
	maxOCRUpscale            = 4
	maxOCRWorkingDimension   = 4096
	maxOCRWorkingPixels      = 12_000_000
	maxOCRTotalWorkingPixels = 48_000_000
	maxOCRTiles              = 8
	targetOCRTileSize        = 1600
	tileOverlapPixels        = 128
	usefulOCRScale           = 1.8
	lowOCRConfidence         = 65.0
	smallOCRWordHeight       = 14
)

type ocrPass struct {
	name          string
	crop          OCRBox
	width, height int
	psm           int
}

type ocrPlan struct {
	Full  ocrPass
	Tiles []ocrPass
}

func calculateOCRPlan(width, height int) ocrPlan {
	preferred := 2
	if maximum(width, height) <= 0 {
		preferred = 1
	} else if maximum(width, height) <= 900 {
		preferred = maxOCRUpscale
	} else if maximum(width, height) <= 1400 {
		preferred = 3
	}
	fullWidth, fullHeight := scaledDimensions(width, height, preferred, maxOCRWorkingPixels)
	plan := ocrPlan{Full: ocrPass{name: "full", crop: OCRBox{Width: width, Height: height}, width: fullWidth, height: fullHeight, psm: 3}}

	crops := calculateTileCrops(width, height)
	remaining := maxOCRTotalWorkingPixels - fullWidth*fullHeight
	if remaining <= 0 || len(crops) == 0 {
		return plan
	}
	perTileBudget := minimum(maxOCRWorkingPixels, remaining/len(crops))
	for index, crop := range crops {
		tileWidth, tileHeight := scaledDimensions(crop.Width, crop.Height, maxOCRUpscale, perTileBudget)
		if tileWidth <= 0 || tileHeight <= 0 || tileWidth*tileHeight > remaining {
			break
		}
		plan.Tiles = append(plan.Tiles, ocrPass{name: fmt.Sprintf("tile-%02d", index+1), crop: crop, width: tileWidth, height: tileHeight, psm: 11})
		remaining -= tileWidth * tileHeight
	}
	return plan
}

func scaledDimensions(width, height, preferredScale, pixelLimit int) (int, int) {
	if width <= 0 || height <= 0 || pixelLimit <= 0 {
		return 0, 0
	}
	factor := float64(minimum(preferredScale, maxOCRUpscale))
	factor = math.Min(factor, float64(maxOCRWorkingDimension)/float64(width))
	factor = math.Min(factor, float64(maxOCRWorkingDimension)/float64(height))
	factor = math.Min(factor, math.Sqrt(float64(pixelLimit)/float64(width*height)))
	if factor <= 0 {
		return 0, 0
	}
	resultWidth := maximum(1, int(math.Round(float64(width)*factor)))
	resultHeight := maximum(1, int(math.Round(float64(height)*factor)))
	for resultWidth > maxOCRWorkingDimension || resultHeight > maxOCRWorkingDimension || resultWidth*resultHeight > pixelLimit {
		if resultWidth >= resultHeight && resultWidth > 1 {
			resultWidth--
		} else if resultHeight > 1 {
			resultHeight--
		} else {
			break
		}
	}
	return resultWidth, resultHeight
}

func calculateTileCrops(width, height int) []OCRBox {
	if width <= 0 || height <= 0 {
		return nil
	}
	desired := maximum(2, ceilingDivision(width, targetOCRTileSize)*ceilingDivision(height, targetOCRTileSize))
	desired = minimum(desired, maxOCRTiles)
	columns, rows := tileGrid(width, height, desired)
	result := make([]OCRBox, 0, columns*rows)
	for row := 0; row < rows; row++ {
		for column := 0; column < columns; column++ {
			x0, x1 := column*width/columns, (column+1)*width/columns
			y0, y1 := row*height/rows, (row+1)*height/rows
			if column > 0 {
				x0 = maximum(0, x0-tileOverlapPixels)
			}
			if column+1 < columns {
				x1 = minimum(width, x1+tileOverlapPixels)
			}
			if row > 0 {
				y0 = maximum(0, y0-tileOverlapPixels)
			}
			if row+1 < rows {
				y1 = minimum(height, y1+tileOverlapPixels)
			}
			result = append(result, OCRBox{X: x0, Y: y0, Width: x1 - x0, Height: y1 - y0})
		}
	}
	return result
}

func tileGrid(width, height, desired int) (int, int) {
	bestColumns, bestRows := 1, desired
	bestScore := math.MaxFloat64
	for rows := 1; rows <= desired; rows++ {
		columns := ceilingDivision(desired, rows)
		if columns*rows > maxOCRTiles {
			continue
		}
		cellRatio := (float64(width) / float64(columns)) / (float64(height) / float64(rows))
		score := math.Abs(math.Log(cellRatio))
		if score < bestScore {
			bestScore, bestColumns, bestRows = score, columns, rows
		}
	}
	return bestColumns, bestRows
}

func shouldUseTiles(full ocrPass, words []OCRWord) bool {
	if float64(full.width)/float64(full.crop.Width) < usefulOCRScale || float64(full.height)/float64(full.crop.Height) < usefulOCRScale {
		return true
	}
	for _, word := range words {
		if strings.TrimSpace(word.Text) != "" && (word.Confidence < lowOCRConfidence || word.Box.Height < smallOCRWordHeight) {
			return true
		}
	}
	return false
}

func projectWords(words []OCRWord, pass ocrPass, imageWidth, imageHeight int) []OCRWord {
	result := make([]OCRWord, 0, len(words))
	for _, word := range words {
		word.Box = projectBox(word.Box, pass, imageWidth, imageHeight)
		word.Accepted = word.Accepted && hasRenderableProjectedGeometry(word.Box)
		result = append(result, word)
	}
	return result
}

func projectAcceptedWords(words []OCRWord, pass ocrPass, imageWidth, imageHeight int) []OCRWord {
	result := make([]OCRWord, 0, len(words))
	for _, word := range words {
		if !word.Accepted {
			continue
		}
		word.Box = projectBox(word.Box, pass, imageWidth, imageHeight)
		if !hasRenderableProjectedGeometry(word.Box) {
			continue
		}
		result = append(result, word)
	}
	return result
}

// A projected OCR box must cover at least one addressable source pixel on
// both axes. Larger thresholds would incorrectly discard narrow punctuation.
func hasRenderableProjectedGeometry(box OCRBox) bool {
	return box.Width > 0 && box.Height > 0
}

func projectBox(box OCRBox, pass ocrPass, imageWidth, imageHeight int) OCRBox {
	x0 := pass.crop.X + roundRatio(box.X, pass.crop.Width, pass.width)
	y0 := pass.crop.Y + roundRatio(box.Y, pass.crop.Height, pass.height)
	x1 := pass.crop.X + roundRatio(box.X+box.Width, pass.crop.Width, pass.width)
	y1 := pass.crop.Y + roundRatio(box.Y+box.Height, pass.crop.Height, pass.height)
	x0, y0 = clamp(x0, 0, imageWidth), clamp(y0, 0, imageHeight)
	x1, y1 = clamp(x1, x0, imageWidth), clamp(y1, y0, imageHeight)
	return OCRBox{X: x0, Y: y0, Width: x1 - x0, Height: y1 - y0}
}

func roundRatio(value, numerator, denominator int) int {
	if denominator <= 0 {
		return 0
	}
	return (value*numerator + denominator/2) / denominator
}

func mergeOCRWords(base, additions []OCRWord) []OCRWord {
	result := append([]OCRWord(nil), base...)
	for _, candidate := range additions {
		match := -1
		for index, existing := range result {
			if !existing.Accepted {
				continue
			}
			overlap := overlapOverSmaller(existing.Box, candidate.Box)
			sameText := normalizedOCRText(existing.Text) == normalizedOCRText(candidate.Text)
			if sameText && overlap >= 0.35 || !sameText && overlap >= 0.70 {
				match = index
				break
			}
		}
		if match < 0 {
			result = append(result, candidate)
			continue
		}
		if candidate.Confidence > result[match].Confidence {
			result[match] = candidate
		}
	}
	return result
}

func overlapOverSmaller(left, right OCRBox) float64 {
	x0, y0 := maximum(left.X, right.X), maximum(left.Y, right.Y)
	x1, y1 := minimum(left.X+left.Width, right.X+right.Width), minimum(left.Y+left.Height, right.Y+right.Height)
	if x1 <= x0 || y1 <= y0 {
		return 0
	}
	smaller := minimum(left.Width*left.Height, right.Width*right.Height)
	if smaller <= 0 {
		return 0
	}
	return float64((x1-x0)*(y1-y0)) / float64(smaller)
}

func normalizedOCRText(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func rebuildOCRPage(words []OCRWord, image OCRImageInfo) OCRPage {
	accepted := make([]OCRWord, 0, len(words))
	rejected := make([]OCRWord, 0)
	for _, word := range words {
		if word.Accepted {
			accepted = append(accepted, word)
		} else {
			rejected = append(rejected, word)
		}
	}
	lines := spatialLines(accepted)
	paragraphGroups := spatialParagraphs(lines)
	page := OCRPage{SchemaVersion: 1, Image: image, Words: make([]OCRWord, 0, len(words)), Paragraphs: make([]OCRParagraph, 0, len(paragraphGroups))}
	for paragraphIndex, paragraphLines := range paragraphGroups {
		block := paragraphIndex + 1
		paragraph := OCRParagraph{ID: fmt.Sprintf("p1-b%d-par1", block), Page: 1, Block: block, Paragraph: 1}
		for lineIndex, lineWords := range paragraphLines {
			lineNumber := lineIndex + 1
			line := OCRLine{ID: fmt.Sprintf("p1-b%d-par1-l%d", block, lineNumber), Page: 1, Block: block, Paragraph: 1, Line: lineNumber}
			for wordIndex := range lineWords {
				word := lineWords[wordIndex]
				word.Page, word.Block, word.Paragraph, word.Line, word.Word = 1, block, 1, lineNumber, wordIndex+1
				word.ID = fmt.Sprintf("p1-b%d-par1-l%d-w%d", block, lineNumber, wordIndex+1)
				line.Words = append(line.Words, word)
				page.Words = append(page.Words, word)
			}
			line.Text, line.Confidence, line.Box = joinWords(line.Words), averageConfidence(line.Words), unionWordBoxes(line.Words)
			paragraph.Lines = append(paragraph.Lines, line)
		}
		paragraph.Text = joinLines(paragraph.Lines)
		paragraph.Confidence = averageLineConfidence(paragraph.Lines)
		paragraph.Box = unionLineBoxes(paragraph.Lines)
		page.Paragraphs = append(page.Paragraphs, paragraph)
	}
	sort.SliceStable(rejected, func(i, j int) bool { return lessSpatialWord(rejected[i], rejected[j]) })
	for index := range rejected {
		rejected[index].Page, rejected[index].Block, rejected[index].Paragraph, rejected[index].Line, rejected[index].Word = 1, 0, 0, 0, index+1
		rejected[index].ID = fmt.Sprintf("p1-b0-par0-l0-w%d", index+1)
		page.Words = append(page.Words, rejected[index])
	}
	return page
}

func spatialLines(words []OCRWord) [][]OCRWord {
	sort.SliceStable(words, func(i, j int) bool { return lessSpatialWord(words[i], words[j]) })
	lines := make([][]OCRWord, 0)
	for _, word := range words {
		best := -1
		for index := len(lines) - 1; index >= 0; index-- {
			box := unionWordBoxes(lines[index])
			if verticalAffinity(box, word.Box) {
				best = index
				break
			}
			if word.Box.Y > box.Y+box.Height*2 {
				break
			}
		}
		if best < 0 {
			lines = append(lines, []OCRWord{word})
		} else {
			lines[best] = append(lines[best], word)
		}
	}
	for index := range lines {
		sort.SliceStable(lines[index], func(i, j int) bool {
			if lines[index][i].Box.X != lines[index][j].Box.X {
				return lines[index][i].Box.X < lines[index][j].Box.X
			}
			return normalizedOCRText(lines[index][i].Text) < normalizedOCRText(lines[index][j].Text)
		})
	}
	sort.SliceStable(lines, func(i, j int) bool {
		left, right := unionWordBoxes(lines[i]), unionWordBoxes(lines[j])
		if left.Y != right.Y {
			return left.Y < right.Y
		}
		return left.X < right.X
	})
	return lines
}

func spatialParagraphs(lines [][]OCRWord) [][][]OCRWord {
	result := make([][][]OCRWord, 0)
	for _, line := range lines {
		if len(result) == 0 {
			result = append(result, [][]OCRWord{line})
			continue
		}
		previous := result[len(result)-1][len(result[len(result)-1])-1]
		previousBox, currentBox := unionWordBoxes(previous), unionWordBoxes(line)
		gap := currentBox.Y - (previousBox.Y + previousBox.Height)
		horizontalOverlap := overlapLength(previousBox.X, previousBox.X+previousBox.Width, currentBox.X, currentBox.X+currentBox.Width)
		aligned := horizontalOverlap > 0 || abs(previousBox.X-currentBox.X) <= maximum(previousBox.Height, currentBox.Height)*2
		if aligned && gap <= maximum(previousBox.Height, currentBox.Height)*2 {
			result[len(result)-1] = append(result[len(result)-1], line)
		} else {
			result = append(result, [][]OCRWord{line})
		}
	}
	return result
}

func verticalAffinity(left, right OCRBox) bool {
	overlap := overlapLength(left.Y, left.Y+left.Height, right.Y, right.Y+right.Height)
	minimumHeight := minimum(left.Height, right.Height)
	if minimumHeight > 0 && float64(overlap)/float64(minimumHeight) >= 0.40 {
		return true
	}
	leftCenter, rightCenter := left.Y+left.Height/2, right.Y+right.Height/2
	return abs(leftCenter-rightCenter) <= maximum(left.Height, right.Height)/2
}

func lessSpatialWord(left, right OCRWord) bool {
	leftCenter, rightCenter := left.Box.Y+left.Box.Height/2, right.Box.Y+right.Box.Height/2
	if leftCenter != rightCenter {
		return leftCenter < rightCenter
	}
	if left.Box.X != right.Box.X {
		return left.Box.X < right.Box.X
	}
	if normalizedOCRText(left.Text) != normalizedOCRText(right.Text) {
		return normalizedOCRText(left.Text) < normalizedOCRText(right.Text)
	}
	return left.Confidence > right.Confidence
}

func plainText(page OCRPage) string {
	paragraphs := make([]string, 0, len(page.Paragraphs))
	for _, paragraph := range page.Paragraphs {
		if text := strings.TrimSpace(paragraph.Text); text != "" {
			paragraphs = append(paragraphs, text)
		}
	}
	return strings.Join(paragraphs, "\n\n")
}

func overlapLength(a0, a1, b0, b1 int) int   { return maximum(0, minimum(a1, b1)-maximum(a0, b0)) }
func ceilingDivision(value, divisor int) int { return (value + divisor - 1) / divisor }
func clamp(value, low, high int) int         { return minimum(maximum(value, low), high) }
func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
func minimum(left, right int) int {
	if left < right {
		return left
	}
	return right
}
func maximum(left, right int) int {
	if left > right {
		return left
	}
	return right
}
