package ocr

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
)

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

// buildPaddleParagraphs treats each Paddle detector region as a semantic line.
// It must not be joined horizontally with an adjacent table cell before
// paragraph reconstruction.
func buildPaddleParagraphs(words []OCRWord) []OCRParagraph {
	return buildPaddleParagraphsWithDiagnostics(words, nil)
}

func buildPaddleParagraphsWithDiagnostics(words []OCRWord, diagnostics *[]OCRParagraphMergeDiagnostic) []OCRParagraph {
	lines := make([][]OCRWord, 0, len(words))
	for _, word := range words {
		lines = append(lines, []OCRWord{word})
	}
	// Paddle detector regions are already semantic lines. First establish the
	// document's visual density; dense UI/screenshot areas stay at control/line
	// granularity while sparse prose can form paragraphs.
	groups := paddleSemanticGroups(lines, diagnostics)
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

func paddleSemanticGroups(lines [][]OCRWord, diagnostics *[]OCRParagraphMergeDiagnostic) [][][]OCRWord {
	result := make([][][]OCRWord, 0, len(lines))
	for _, group := range spatialParagraphsWithDiagnostics(lines, diagnostics) {
		parts := splitPaddleSemanticGroup(group, diagnostics)
		for _, part := range parts {
			if !paddleUIGroup(part, lines) {
				result = append(result, part)
				continue
			}
			// Dense screenshot/UI regions use detector-line/control units. Pairing
			// adjacent controls made their union box span menus, table cells and
			// dialog fields, so cleanup and layout could no longer stay local.
			for _, line := range part {
				result = append(result, [][]OCRWord{line})
			}
		}
	}
	return result
}

func splitPaddleSemanticGroup(group [][]OCRWord, diagnostics *[]OCRParagraphMergeDiagnostic) [][][]OCRWord {
	if len(group) < 2 {
		return [][][]OCRWord{group}
	}
	result := make([][][]OCRWord, 0, len(group))
	start := 0
	for index := 1; index < len(group); index++ {
		previous, current := group[index-1], group[index]
		previousBox, currentBox := unionWordBoxes(previous), unionWordBoxes(current)
		if !paddleSemanticBreak(joinWords(previous), joinWords(current), previousBox, currentBox) {
			continue
		}
		diagnostic := mergeDiagnostic(previousBox, currentBox, "semantic_boundary")
		diagnostic.PreviousText, diagnostic.CurrentText = joinWords(previous), joinWords(current)
		recordParagraphMergeDiagnostic(diagnostics, diagnostic)
		result = append(result, group[start:index])
		start = index
	}
	result = append(result, group[start:])
	return result
}

func paddleUIGroup(group [][]OCRWord, all [][]OCRWord) bool {
	if len(group) == 0 {
		return false
	}
	line := group[0]
	box := unionWordBoxes(line)
	text := strings.TrimSpace(joinWords(line))
	if box.Width <= 0 || box.Height <= 0 || text == "" {
		return true
	}
	nearby, aligned := 0, 0
	for _, other := range all {
		otherBox := unionWordBoxes(other)
		if otherBox == box && joinWords(other) == text {
			continue
		}
		height := maximum(box.Height, otherBox.Height)
		if verticalBandDistance(box, otherBox) <= height*3 && horizontalGap(box, otherBox) <= height*8 {
			nearby++
			if abs(box.X-otherBox.X) <= height || abs(box.X+box.Width-(otherBox.X+otherBox.Width)) <= height {
				aligned++
			}
		}
	}
	shortControl := len([]rune(text)) <= 28 && len(strings.Fields(text)) <= 4
	shortLines := 0
	heights := make([]float64, 0, len(group))
	for _, member := range group {
		memberText := strings.TrimSpace(joinWords(member))
		memberBox := unionWordBoxes(member)
		if memberBox.Height > 0 {
			heights = append(heights, float64(memberBox.Height))
		}
		if len([]rune(memberText)) <= 36 && len(strings.Fields(memberText)) <= 5 {
			shortLines++
		}
	}
	tallDenseColumn := false
	if len(group) >= 4 && len(heights) > 0 {
		groupBox := unionPaddleLineBoxes(group)
		tallDenseColumn = shortLines*4 >= len(group)*3 && float64(groupBox.Height) >= medianOCRMeasurements(heights)*3.5
	}
	return tallDenseColumn || (shortControl && nearby >= 3 && aligned < nearby/2+1)
}

func unionPaddleLineBoxes(lines [][]OCRWord) OCRBox {
	boxes := make([]OCRBox, 0, len(lines))
	for _, line := range lines {
		boxes = append(boxes, unionWordBoxes(line))
	}
	return unionBoxes(boxes)
}

func medianOCRMeasurements(values []float64) float64 {
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	middle := len(ordered) / 2
	if len(ordered)%2 != 0 {
		return ordered[middle]
	}
	return (ordered[middle-1] + ordered[middle]) / 2
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

func paddleSemanticBreak(previousText, currentText string, previous, current OCRBox) bool {
	height := maximum(previous.Height, current.Height)
	gap := current.Y - (previous.Y + previous.Height)
	previousTokens, currentTokens := len(strings.Fields(previousText)), len(strings.Fields(currentText))
	currentRunes, previousRunes := []rune(currentText), []rune(previousText)
	if len(currentRunes) == 0 || len(previousRunes) == 0 {
		return false
	}
	if paddleContinuationLine(previousRunes, currentRunes, previous, current, height, gap) {
		return false
	}
	startsList := strings.ContainsRune("•●○▪▫-", currentRunes[0])
	if startsList {
		return true
	}
	// Returning from an inset screenshot/control panel to the document margin is
	// a visual-context boundary even when both sides contain sentence-length
	// text. Requiring both a line-sized left shift and a real vertical gap keeps
	// ordinary ragged prose and hanging bullet indentation together.
	if current.X+height*3/2 < previous.X && gap > height/2 {
		return true
	}
	if gap > height*2/5 && previousTokens <= 5 && currentTokens >= 6 {
		return true
	}
	// A short field/tab caption immediately after a long instruction is a
	// screenshot/control boundary, not the final ragged line of the prose.
	if previousTokens >= 6 && currentTokens <= 4 && current.Width*2 < previous.Width && gap >= height/5 {
		return true
	}
	return false
}

func paddleContinuationLine(previousText, currentText []rune, previous, current OCRBox, height, gap int) bool {
	last := previousText[len(previousText)-1]
	first := currentText[0]
	if strings.ContainsRune(".!?:;", last) || !unicode.IsLower(first) {
		return false
	}
	minimumHeight := minimum(previous.Height, current.Height)
	if minimumHeight <= 0 || float64(height)/float64(minimumHeight) > 1.35 {
		return false
	}
	return gap <= height/3 && abs(previous.X-current.X) <= height
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
