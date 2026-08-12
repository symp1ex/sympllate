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
		// Tile PSM 11 block/paragraph identifiers are local to the tile and
		// cannot safely replace the full-page Tesseract segmentation. Mark tile-
		// only words for spatial fallback; matched words inherit the full-pass
		// identifiers in mergeOCRWords.
		word.ID = ""
		word.Page, word.Block, word.Paragraph, word.Line, word.Word = 0, 0, 0, 0, 0
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
			existing := result[match]
			candidate.ID = existing.ID
			candidate.Page, candidate.Block, candidate.Paragraph = existing.Page, existing.Block, existing.Paragraph
			candidate.Line, candidate.Word = existing.Line, existing.Word
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
	structured := make([]OCRWord, 0, len(words))
	fallback := make([]OCRWord, 0)
	rejected := make([]OCRWord, 0)
	for _, word := range words {
		if word.Accepted {
			if hasOCRStructure(word) {
				structured = append(structured, word)
			} else {
				fallback = append(fallback, word)
			}
		} else {
			rejected = append(rejected, word)
		}
	}
	paragraphs := groupWords(structured)
	fallback = attachFallbackWords(paragraphs, fallback)
	paragraphs = append(paragraphs, buildSpatialParagraphs(fallback, nextOCRBlock(paragraphs))...)
	page := OCRPage{SchemaVersion: 1, Image: image, Words: make([]OCRWord, 0, len(words)), Paragraphs: paragraphs}
	for paragraphIndex := range page.Paragraphs {
		normalizeOCRParagraph(&page.Paragraphs[paragraphIndex])
		for _, line := range page.Paragraphs[paragraphIndex].Lines {
			page.Words = append(page.Words, line.Words...)
		}
	}
	sort.SliceStable(rejected, func(i, j int) bool { return lessSpatialWord(rejected[i], rejected[j]) })
	for index := range rejected {
		rejected[index].Page, rejected[index].Block, rejected[index].Paragraph, rejected[index].Line, rejected[index].Word = 1, 0, 0, 0, index+1
		rejected[index].ID = fmt.Sprintf("p1-b0-par0-l0-w%d", index+1)
		page.Words = append(page.Words, rejected[index])
	}
	return page
}

func hasOCRStructure(word OCRWord) bool {
	return word.Page > 0 && word.Block > 0 && word.Paragraph > 0 && word.Line > 0
}

func attachFallbackWords(paragraphs []OCRParagraph, words []OCRWord) []OCRWord {
	unassigned := make([]OCRWord, 0)
	for _, word := range words {
		bestParagraph, bestLine, bestScore := -1, -1, math.MaxInt
		for paragraphIndex := range paragraphs {
			for lineIndex := range paragraphs[paragraphIndex].Lines {
				box := paragraphs[paragraphIndex].Lines[lineIndex].Box
				if !sameSpatialLine(box, word.Box) {
					continue
				}
				score := abs((box.Y+box.Height/2)-(word.Box.Y+word.Box.Height/2)) + horizontalGap(box, word.Box)
				if score < bestScore {
					bestParagraph, bestLine, bestScore = paragraphIndex, lineIndex, score
				}
			}
		}
		if bestParagraph < 0 {
			unassigned = append(unassigned, word)
			continue
		}
		line := &paragraphs[bestParagraph].Lines[bestLine]
		word.Page, word.Block, word.Paragraph, word.Line = line.Page, line.Block, line.Paragraph, line.Line
		line.Words = append(line.Words, word)
	}
	return unassigned
}

func nextOCRBlock(paragraphs []OCRParagraph) int {
	block := 1
	for _, paragraph := range paragraphs {
		block = maximum(block, paragraph.Block+1)
	}
	return block
}

func buildSpatialParagraphs(words []OCRWord, firstBlock int) []OCRParagraph {
	groups := spatialParagraphs(spatialLines(words))
	result := make([]OCRParagraph, 0, len(groups))
	for paragraphIndex, lines := range groups {
		block := firstBlock + paragraphIndex
		paragraph := OCRParagraph{Page: 1, Block: block, Paragraph: 1}
		for lineIndex, words := range lines {
			paragraph.Lines = append(paragraph.Lines, OCRLine{Page: 1, Block: block, Paragraph: 1, Line: lineIndex + 1, Words: words})
		}
		result = append(result, paragraph)
	}
	return result
}

// buildPaddleParagraphs treats each DB detector quad as a text line. Unlike a
// Tesseract TSV word, a Paddle region must not be joined horizontally with an
// adjacent table cell before paragraph reconstruction.
func buildPaddleParagraphs(words []OCRWord) []OCRParagraph {
	return buildPaddleParagraphsWithDiagnostics(words, nil)
}

func buildPaddleParagraphsWithDiagnostics(words []OCRWord, diagnostics *[]OCRParagraphMergeDiagnostic) []OCRParagraph {
	lines := make([][]OCRWord, 0, len(words))
	for _, word := range words {
		lines = append(lines, []OCRWord{word})
	}
	groups := spatialParagraphsWithDiagnostics(lines, diagnostics)
	result := make([]OCRParagraph, 0, len(groups))
	for paragraphIndex, group := range groups {
		block := paragraphIndex + 1
		paragraph := OCRParagraph{Page: 1, Block: block, Paragraph: 1}
		for lineIndex, lineWords := range group {
			paragraph.Lines = append(paragraph.Lines, OCRLine{Page: 1, Block: block, Paragraph: 1, Line: lineIndex + 1, Words: lineWords})
		}
		normalizeOCRParagraph(&paragraph)
		result = append(result, paragraph)
	}
	return columnAwareParagraphOrder(result)
}

func columnAwareParagraphOrder(paragraphs []OCRParagraph) []OCRParagraph {
	if len(paragraphs) < 2 {
		return paragraphs
	}
	medianWidth := make([]int, 0, len(paragraphs))
	pageRight := 0
	for _, paragraph := range paragraphs {
		medianWidth = append(medianWidth, paragraph.Box.Width)
		pageRight = maximum(pageRight, paragraph.Box.X+paragraph.Box.Width)
	}
	sort.Ints(medianWidth)
	typical := medianWidth[len(medianWidth)/2]
	columnGap := maximum(typical, pageRight/8)
	sort.SliceStable(paragraphs, func(i, j int) bool {
		left, right := paragraphs[i].Box, paragraphs[j].Box
		if abs(left.X-right.X) >= columnGap {
			return left.X < right.X
		}
		if left.Y != right.Y {
			return left.Y < right.Y
		}
		return left.X < right.X
	})
	for index := range paragraphs {
		block := index + 1
		paragraphs[index].Block = block
		paragraphs[index].Paragraph = 1
		for lineIndex := range paragraphs[index].Lines {
			paragraphs[index].Lines[lineIndex].Block = block
			paragraphs[index].Lines[lineIndex].Paragraph = 1
			paragraphs[index].Lines[lineIndex].Line = lineIndex + 1
		}
		normalizeOCRParagraph(&paragraphs[index])
	}
	return paragraphs
}

func normalizeOCRParagraph(paragraph *OCRParagraph) {
	paragraph.ID = fmt.Sprintf("p%d-b%d-par%d", paragraph.Page, paragraph.Block, paragraph.Paragraph)
	for lineIndex := range paragraph.Lines {
		line := &paragraph.Lines[lineIndex]
		sort.SliceStable(line.Words, func(i, j int) bool { return line.Words[i].Box.X < line.Words[j].Box.X })
		line.ID = fmt.Sprintf("p%d-b%d-par%d-l%d", line.Page, line.Block, line.Paragraph, line.Line)
		for wordIndex := range line.Words {
			line.Words[wordIndex].Page, line.Words[wordIndex].Block = line.Page, line.Block
			line.Words[wordIndex].Paragraph, line.Words[wordIndex].Line = line.Paragraph, line.Line
			line.Words[wordIndex].Word = wordIndex + 1
			line.Words[wordIndex].ID = fmt.Sprintf("%s-w%d", line.ID, wordIndex+1)
		}
		line.Text, line.Confidence, line.Box = joinWords(line.Words), averageConfidence(line.Words), unionWordBoxes(line.Words)
	}
	paragraph.Text = joinLines(paragraph.Lines)
	paragraph.Confidence = averageLineConfidence(paragraph.Lines)
	paragraph.Box = unionLineBoxes(paragraph.Lines)
}

func spatialLines(words []OCRWord) [][]OCRWord {
	sort.SliceStable(words, func(i, j int) bool { return lessSpatialWord(words[i], words[j]) })
	lines := make([][]OCRWord, 0)
	for _, word := range words {
		best := -1
		for index := len(lines) - 1; index >= 0; index-- {
			box := unionWordBoxes(lines[index])
			if sameSpatialLine(box, word.Box) {
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

func sameSpatialLine(left, right OCRBox) bool {
	if !verticalAffinity(left, right) {
		return false
	}
	return horizontalGap(left, right) <= maximum(left.Height, right.Height)*6
}

func horizontalGap(left, right OCRBox) int {
	if left.X+left.Width < right.X {
		return right.X - (left.X + left.Width)
	}
	if right.X+right.Width < left.X {
		return left.X - (right.X + right.Width)
	}
	return 0
}

func spatialParagraphs(lines [][]OCRWord) [][][]OCRWord {
	return spatialParagraphsWithDiagnostics(lines, nil)
}

func spatialParagraphsWithDiagnostics(lines [][]OCRWord, diagnostics *[]OCRParagraphMergeDiagnostic) [][][]OCRWord {
	result := make([][][]OCRWord, 0)
	for _, line := range lines {
		best, bestScore := -1, math.MaxFloat64
		for index := range result {
			previous := result[index][len(result[index])-1]
			previousBox, currentBox := unionWordBoxes(previous), unionWordBoxes(line)
			score, ok := spatialParagraphAffinity(previousBox, currentBox)
			if !ok {
				continue
			}
			if score < bestScore && paragraphMergeSafe(result[index], line, lines, diagnostics) {
				best, bestScore = index, score
			}
		}
		if best < 0 {
			result = append(result, [][]OCRWord{line})
			continue
		}
		result[best] = append(result[best], line)
	}
	return result
}

func spatialParagraphAffinity(previous, current OCRBox) (float64, bool) {
	return spatialParagraphPairAffinity(previous, current)
}

func spatialParagraphPairAffinity(previous, current OCRBox) (float64, bool) {
	height := maximum(previous.Height, current.Height)
	minimumHeight := minimum(previous.Height, current.Height)
	if height <= 0 || minimumHeight <= 0 || float64(height)/float64(minimumHeight) > 1.45 {
		return 0, false
	}
	gap := current.Y - (previous.Y + previous.Height)
	if gap < -minimumHeight/2 || float64(gap)/float64(height) > 0.85 {
		return 0, false
	}
	overlap := overlapLength(previous.X, previous.X+previous.Width, current.X, current.X+current.Width)
	overlapRatio := float64(overlap) / float64(maximum(1, minimum(previous.Width, current.Width)))
	leftDifference := abs(previous.X - current.X)
	widthRatio := float64(minimum(previous.Width, current.Width)) / float64(maximum(1, maximum(previous.Width, current.Width)))
	shortInsideLong := overlapRatio >= .85 && widthRatio <= .55
	if shortInsideLong && leftDifference > maximum(height*2, minimum(previous.Width, current.Width)/3) {
		return 0, false
	}
	if leftDifference > maximum(height*3, maximum(previous.Width, current.Width)/4) {
		return 0, false
	}
	if overlapRatio < 0.25 && leftDifference > height {
		return 0, false
	}
	return float64(maximum(0, gap))/float64(height) + float64(leftDifference)/float64(height*4) - overlapRatio*0.25, true
}

func paragraphMergeSafe(paragraph [][]OCRWord, line []OCRWord, allLines [][]OCRWord, diagnostics *[]OCRParagraphMergeDiagnostic) bool {
	if len(paragraph) == 0 || len(line) == 0 {
		return true
	}
	member := make(map[string]struct{}, len(paragraph)+1)
	boxes := make([]OCRBox, 0, len(paragraph)+1)
	for _, existing := range paragraph {
		box := unionWordBoxes(existing)
		boxes = append(boxes, box)
		member[ocrLineKey(box, joinWords(existing))] = struct{}{}
	}
	current := unionWordBoxes(line)
	boxes = append(boxes, current)
	member[ocrLineKey(current, joinWords(line))] = struct{}{}
	if len(paragraph) >= 2 && narrowLineEntersCrowdedNeighborhood(boxes[:len(boxes)-1], current, allLines, member) {
		diagnostic := mergeDiagnostic(boxes[len(boxes)-2], current, "layout_neighbor_conflict")
		diagnostic.PreviousText = joinWords(paragraph[len(paragraph)-1])
		diagnostic.CurrentText = joinWords(line)
		recordParagraphMergeDiagnostic(diagnostics, diagnostic)
		return false
	}
	tentative := unionBoxes(boxes)
	for _, other := range allLines {
		otherBox := unionWordBoxes(other)
		if _, ok := member[ocrLineKey(otherBox, joinWords(other))]; ok {
			continue
		}
		if paragraphCandidateNeighbor(boxes, otherBox) {
			continue
		}
		intrudes, ratio := foreignRegionIntrudes(tentative, boxes, otherBox)
		if intrudes {
			diagnostic := mergeDiagnostic(boxes[len(boxes)-2], current, "foreign_region_intrusion")
			diagnostic.PreviousText = joinWords(paragraph[len(paragraph)-1])
			diagnostic.CurrentText = joinWords(line)
			diagnostic.TentativeBox = tentative
			diagnostic.ForeignBox = otherBox
			diagnostic.ForeignText = joinWords(other)
			diagnostic.IntersectionRatio = ratio
			recordParagraphMergeDiagnostic(diagnostics, diagnostic)
			return false
		}
	}
	return true
}

func narrowLineEntersCrowdedNeighborhood(previous []OCRBox, current OCRBox, allLines [][]OCRWord, member map[string]struct{}) bool {
	if len(previous) == 0 || current.Width <= 0 || current.Height <= 0 {
		return false
	}
	widths := make([]int, 0, len(previous))
	for _, box := range previous {
		widths = append(widths, box.Width)
	}
	sort.Ints(widths)
	typicalWidth := widths[len(widths)/2]
	if typicalWidth <= 0 || float64(current.Width)/float64(typicalWidth) >= .35 {
		return false
	}
	for _, other := range allLines {
		otherBox := unionWordBoxes(other)
		if _, ok := member[ocrLineKey(otherBox, joinWords(other))]; ok {
			continue
		}
		if horizontalGap(current, otherBox) <= maximum(current.Height, otherBox.Height)*6 && verticalBandDistance(current, otherBox) <= maximum(current.Height, otherBox.Height) && abs(current.X-otherBox.X) > maximum(current.Height, otherBox.Height)*2 {
			return true
		}
	}
	return false
}

func verticalBandDistance(left, right OCRBox) int {
	if left.Y+left.Height < right.Y {
		return right.Y - (left.Y + left.Height)
	}
	if right.Y+right.Height < left.Y {
		return left.Y - (right.Y + right.Height)
	}
	return 0
}

func paragraphCandidateNeighbor(members []OCRBox, candidate OCRBox) bool {
	for _, member := range members {
		if _, ok := spatialParagraphPairAffinity(member, candidate); ok {
			return true
		}
	}
	return false
}

func foreignRegionIntrudes(tentative OCRBox, members []OCRBox, foreign OCRBox) (bool, float64) {
	if tentative.Width <= 0 || tentative.Height <= 0 || foreign.Width <= 0 || foreign.Height <= 0 {
		return false, 0
	}
	intersection := ocrBoxIntersectionArea(tentative, foreign)
	if intersection == 0 {
		return false, 0
	}
	foreignArea := foreign.Width * foreign.Height
	ratio := 0.0
	if foreignArea > 0 {
		ratio = float64(intersection) / float64(foreignArea)
	}
	if foreignArea <= 0 || ratio < .35 {
		return false, ratio
	}
	for _, member := range members {
		if ocrBoxIntersectionArea(member, foreign) > 0 {
			return false, ratio
		}
	}
	foreignCenterY := foreign.Y + foreign.Height/2
	if foreignCenterY < tentative.Y || foreignCenterY > tentative.Y+tentative.Height {
		return false, ratio
	}
	return true, ratio
}

func ocrBoxIntersectionArea(left, right OCRBox) int {
	width := overlapLength(left.X, left.X+left.Width, right.X, right.X+right.Width)
	height := overlapLength(left.Y, left.Y+left.Height, right.Y, right.Y+right.Height)
	return width * height
}

func mergeDiagnostic(previous, current OCRBox, reason string) OCRParagraphMergeDiagnostic {
	height := maximum(previous.Height, current.Height)
	minimumHeight := minimum(previous.Height, current.Height)
	heightRatio := 0.0
	if minimumHeight > 0 {
		heightRatio = float64(height) / float64(minimumHeight)
	}
	return OCRParagraphMergeDiagnostic{
		PreviousBox:     previous,
		CurrentBox:      current,
		Reason:          reason,
		LeftEdgeDelta:   abs(previous.X - current.X),
		VerticalGap:     current.Y - (previous.Y + previous.Height),
		LineHeightRatio: heightRatio,
	}
}

func recordParagraphMergeDiagnostic(diagnostics *[]OCRParagraphMergeDiagnostic, diagnostic OCRParagraphMergeDiagnostic) {
	if diagnostics == nil {
		return
	}
	*diagnostics = append(*diagnostics, diagnostic)
}

func ocrLineKey(box OCRBox, text string) string {
	return fmt.Sprintf("%d:%d:%d:%d:%s", box.X, box.Y, box.Width, box.Height, text)
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
