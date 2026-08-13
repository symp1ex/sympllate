package imagebatch

import (
	"math"
	"sort"
	"strings"
	"unicode"

	"github.com/sympllate/translator/internal/ocr"
)

const materialSourceTextOverlap = .35

type renderCandidate struct {
	index       int
	paragraph   ocr.OCRParagraph
	translation TranslatedBlock
	geometry    SourceTextGeometry
}

type collisionResolution struct {
	reason     string
	diagnostic CollisionDiagnostic
}

// normalizeCandidateContinuations preserves both independently useful blocks
// while removing a phrase repeated at the end of one translated block and the
// beginning of the next. This is intentionally stricter than duplicate
// detection: source and target must both have a multi-token sequence overlap.
func normalizeCandidateContinuations(candidates []renderCandidate) map[int]bool {
	normalized := make(map[int]bool)
	for left := range candidates {
		for right := left + 1; right < len(candidates); right++ {
			first, second := &candidates[left], &candidates[right]
			if sourceGeometryOverlapRatio(first.geometry, second.geometry) == 0 && !baselineCompatible(first.geometry.Bounds, second.geometry.Bounds) {
				continue
			}
			if boxReadingOrder(second.geometry.Bounds, first.geometry.Bounds) {
				first, second = second, first
			}
			sourceOverlap := suffixPrefixTokenOverlap(first.paragraph.Text, second.paragraph.Text)
			if sourceOverlap < 2 {
				continue
			}
			firstSourceTokens, secondSourceTokens := collisionTokens(first.paragraph.Text), collisionTokens(second.paragraph.Text)
			if float64(sourceOverlap)/float64(max(1, min(len(firstSourceTokens), len(secondSourceTokens)))) < .35 {
				continue
			}
			targetOverlap := suffixPrefixTokenOverlap(first.translation.TranslatedText, second.translation.TranslatedText)
			if targetOverlap < 2 {
				continue
			}
			fields := strings.Fields(second.translation.TranslatedText)
			if targetOverlap >= len(fields) {
				continue
			}
			second.translation.TranslatedText = strings.Join(fields[targetOverlap:], " ")
			normalized[second.index] = true
		}
	}
	return normalized
}

func suffixPrefixTokenOverlap(left, right string) int {
	a, b := collisionTokens(left), collisionTokens(right)
	limit := min(len(a), len(b))
	for size := limit; size >= 1; size-- {
		match := true
		for index := 0; index < size; index++ {
			if a[len(a)-size+index] != b[index] {
				match = false
				break
			}
		}
		if match {
			return size
		}
	}
	return 0
}

func boxReadingOrder(left, right ocr.OCRBox) bool {
	lineHeight := max(1, min(left.Height, right.Height))
	if absInt((left.Y+left.Height/2)-(right.Y+right.Height/2)) <= lineHeight/2 {
		return left.X < right.X
	}
	return left.Y < right.Y
}

func sourceTextGeometry(paragraph ocr.OCRParagraph, transform CoordinateTransform, width, height int) SourceTextGeometry {
	geometry := SourceTextGeometry{ID: paragraph.ID, Bounds: ClampBox(TransformBox(paragraph.Box, transform), width, height)}
	for _, line := range paragraph.Lines {
		geometry.LineRegions = appendUniqueBox(geometry.LineRegions, ClampBox(TransformBox(line.Box, transform), width, height))
		for _, word := range line.Words {
			if !word.Accepted {
				continue
			}
			wordBox := ClampBox(TransformBox(word.Box, transform), width, height)
			lineBox := ClampBox(TransformBox(line.Box, transform), width, height)
			if boxIntersectionArea(wordBox, geometry.Bounds) == 0 || (lineBox.Width > 0 && boxIntersectionArea(wordBox, lineBox) == 0) {
				continue
			}
			geometry.Regions = appendUniqueBox(geometry.Regions, wordBox)
			if polygonValid(word.Polygon) {
				geometry.Polygons = append(geometry.Polygons, transformPolygon(word.Polygon, transform))
			}
		}
	}
	if len(geometry.Regions) > 0 {
		geometry.Level = "word"
	} else {
		for _, line := range paragraph.Lines {
			geometry.Regions = appendUniqueBox(geometry.Regions, ClampBox(TransformBox(line.Box, transform), width, height))
		}
		if len(geometry.Regions) > 0 {
			geometry.Level = "line"
		}
	}
	if len(geometry.Regions) == 0 && geometry.Bounds.Width > 0 && geometry.Bounds.Height > 0 {
		geometry.Regions = append(geometry.Regions, geometry.Bounds)
		geometry.Level = "paragraph"
	}
	if geometry.Bounds.Width <= 0 || geometry.Bounds.Height <= 0 {
		geometry.Bounds = unionOCRBoxes(geometry.Regions)
	}
	return geometry
}

func appendUniqueBox(boxes []ocr.OCRBox, box ocr.OCRBox) []ocr.OCRBox {
	if box.Width <= 0 || box.Height <= 0 {
		return boxes
	}
	for _, existing := range boxes {
		if existing == box {
			return boxes
		}
	}
	return append(boxes, box)
}

func unionOCRBoxes(boxes []ocr.OCRBox) ocr.OCRBox {
	if len(boxes) == 0 {
		return ocr.OCRBox{}
	}
	result := boxes[0]
	for _, box := range boxes[1:] {
		left, top := min(result.X, box.X), min(result.Y, box.Y)
		right, bottom := max(result.X+result.Width, box.X+box.Width), max(result.Y+result.Height, box.Y+box.Height)
		result = ocr.OCRBox{X: left, Y: top, Width: right - left, Height: bottom - top}
	}
	return result
}

func resolveSourceCollisions(candidates []renderCandidate) (map[int]collisionResolution, []CollisionDiagnostic) {
	ordered := append([]renderCandidate(nil), candidates...)
	sort.SliceStable(ordered, func(i, j int) bool { return betterRenderCandidate(ordered[i], ordered[j]) })
	rejected := make(map[int]collisionResolution)
	diagnostics := make([]CollisionDiagnostic, 0)
	for left := 0; left < len(ordered); left++ {
		if _, lost := rejected[ordered[left].index]; lost {
			continue
		}
		for right := left + 1; right < len(ordered); right++ {
			if _, lost := rejected[ordered[right].index]; lost {
				continue
			}
			diagnostic, destructive := classifySourceCollision(ordered[left], ordered[right])
			if diagnostic.CollisionClass == "no_intersection" {
				continue
			}
			if !destructive {
				diagnostics = append(diagnostics, diagnostic)
				continue
			}
			reason := "overlapping_ocr_box"
			switch diagnostic.CollisionClass {
			case "duplicate":
				reason = "duplicate_ocr_detection"
			case "contained_fragment":
				reason = "contained_ocr_fragment"
			}
			diagnostic.Decision = "kept_" + ordered[left].paragraph.ID
			diagnostics = append(diagnostics, diagnostic)
			loserDiagnostic := diagnostic
			loserDiagnostic.BlockID, loserDiagnostic.ConflictingBlockID = ordered[right].paragraph.ID, ordered[left].paragraph.ID
			loserDiagnostic.Decision = "skipped"
			rejected[ordered[right].index] = collisionResolution{reason: reason, diagnostic: loserDiagnostic}
		}
	}
	return rejected, diagnostics
}

func classifySourceCollision(left, right renderCandidate) (CollisionDiagnostic, bool) {
	paragraphArea := boxIntersectionArea(left.geometry.Bounds, right.geometry.Bounds)
	diagnostic := CollisionDiagnostic{BlockID: left.paragraph.ID, ConflictingBlockID: right.paragraph.ID, CollisionClass: "no_intersection", Decision: "kept_both"}
	leftText, rightText := normalizedCollisionText(left.paragraph.Text), normalizedCollisionText(right.paragraph.Text)
	textSimilarity := collisionTextSimilarity(left.paragraph.Text, right.paragraph.Text)
	translationSimilarity := collisionTextSimilarity(left.translation.TranslatedText, right.translation.TranslatedText)
	if paragraphArea == 0 {
		// Paddle full-page and tiled passes can produce nearly adjacent quads for
		// the same physical line after projection. Only collapse these when both
		// source and translated text agree and their baselines/centres are close.
		if textSimilarity >= .94 && translationSimilarity >= .9 && baselineCompatible(left.geometry.Bounds, right.geometry.Bounds) && normalizedCenterDistance(left.geometry.Bounds, right.geometry.Bounds) <= 1.25 {
			diagnostic.CollisionClass = "duplicate"
			return diagnostic, true
		}
		return diagnostic, false
	}
	diagnostic.ParagraphIntersection = boxIntersection(left.geometry.Bounds, right.geometry.Bounds)
	diagnostic.ParagraphOverlapRatio = smallerBoxOverlapRatio(left.geometry.Bounds, right.geometry.Bounds)
	diagnostic.TextRegionOverlapRatio = sourceGeometryOverlapRatio(left.geometry, right.geometry)
	if diagnostic.TextRegionOverlapRatio == 0 {
		diagnostic.CollisionClass = "aabb_only"
		diagnostic.Decision = "kept_both"
		return diagnostic, false
	}
	paragraphIoU := boxIoU(left.geometry.Bounds, right.geometry.Bounds)
	if leftText == rightText && (diagnostic.TextRegionOverlapRatio >= .2 || paragraphIoU >= .15 || normalizedCenterDistance(left.geometry.Bounds, right.geometry.Bounds) <= .8) {
		diagnostic.CollisionClass = "duplicate"
		return diagnostic, true
	}
	shorterRatio := float64(min(len([]rune(leftText)), len([]rune(rightText)))) / float64(max(1, max(len([]rune(leftText)), len([]rune(rightText)))))
	containedText := strings.Contains(leftText, rightText) || strings.Contains(rightText, leftText)
	if containedText && meaningfulTextLength(left.paragraph.Text) >= 3 && meaningfulTextLength(right.paragraph.Text) >= 3 &&
		(shorterRatio >= .2 || diagnostic.ParagraphOverlapRatio >= .9) &&
		(diagnostic.TextRegionOverlapRatio >= .45 || diagnostic.ParagraphOverlapRatio >= .72) {
		diagnostic.CollisionClass = "contained_fragment"
		return diagnostic, true
	}
	if textSimilarity >= .86 && translationSimilarity >= .82 && (diagnostic.TextRegionOverlapRatio >= materialSourceTextOverlap || paragraphIoU >= .3) {
		diagnostic.CollisionClass = "duplicate"
		return diagnostic, true
	}
	// Geometry by itself is not enough evidence to discard translated text.
	// Independent labels, table cells, and crossing paragraph AABBs are common.
	if diagnostic.TextRegionOverlapRatio >= materialSourceTextOverlap {
		diagnostic.CollisionClass = "ambiguous_overlap"
		diagnostic.Decision = "kept_both"
		return diagnostic, false
	}
	diagnostic.CollisionClass = "neighboring_independent_text"
	diagnostic.Decision = "kept_both"
	return diagnostic, false
}

func betterRenderCandidate(left, right renderCandidate) bool {
	// Semantic completeness and source coverage deliberately precede OCR
	// confidence. A high-confidence word inside a complete paragraph must not
	// suppress the paragraph.
	leftContent, rightContent := meaningfulTextLength(left.paragraph.Text), meaningfulTextLength(right.paragraph.Text)
	if leftContent != rightContent {
		return leftContent > rightContent
	}
	leftTokens, rightTokens := meaningfulTokenCount(left.paragraph.Text), meaningfulTokenCount(right.paragraph.Text)
	if leftTokens != rightTokens {
		return leftTokens > rightTokens
	}
	leftAccepted, rightAccepted := acceptedWordCount(left.paragraph), acceptedWordCount(right.paragraph)
	if (leftAccepted > 0) != (rightAccepted > 0) {
		return leftAccepted > 0
	}
	leftArea, rightArea := sourceCoverageArea(left.geometry), sourceCoverageArea(right.geometry)
	if leftArea != rightArea {
		return leftArea > rightArea
	}
	if geometryQuality(left.geometry) != geometryQuality(right.geometry) {
		return geometryQuality(left.geometry) > geometryQuality(right.geometry)
	}
	if left.paragraph.Confidence != right.paragraph.Confidence {
		return left.paragraph.Confidence > right.paragraph.Confidence
	}
	leftDetector, rightDetector := averageDetectorConfidence(left.paragraph), averageDetectorConfidence(right.paragraph)
	if leftDetector != rightDetector {
		return leftDetector > rightDetector
	}
	return left.paragraph.ID < right.paragraph.ID
}

func meaningfulTextLength(value string) int { return len([]rune(normalizedCollisionText(value))) }

func meaningfulTokenCount(value string) int {
	return len(strings.FieldsFunc(strings.ToLower(value), func(character rune) bool {
		return !unicode.IsLetter(character) && !unicode.IsDigit(character)
	}))
}

func sourceCoverageArea(geometry SourceTextGeometry) int {
	if len(geometry.Regions) > 0 {
		return regionArea(geometry.Regions)
	}
	return geometry.Bounds.Width * geometry.Bounds.Height
}

func collisionTextSimilarity(left, right string) float64 {
	a, b := collisionTokens(left), collisionTokens(right)
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	counts := make(map[string]int, len(a))
	for _, token := range a {
		counts[token]++
	}
	shared := 0
	for _, token := range b {
		if counts[token] > 0 {
			shared++
			counts[token]--
		}
	}
	tokenDice := 2 * float64(shared) / float64(len(a)+len(b))
	leftNormalized, rightNormalized := normalizedCollisionText(left), normalizedCollisionText(right)
	if leftNormalized == rightNormalized {
		return 1
	}
	if strings.Contains(leftNormalized, rightNormalized) || strings.Contains(rightNormalized, leftNormalized) {
		coverage := float64(min(len([]rune(leftNormalized)), len([]rune(rightNormalized)))) / float64(max(len([]rune(leftNormalized)), len([]rune(rightNormalized))))
		return math.Max(tokenDice, coverage)
	}
	return math.Max(tokenDice, collisionNGramSimilarity(leftNormalized, rightNormalized))
}

func collisionNGramSimilarity(left, right string) float64 {
	if len([]rune(left)) < 2 || len([]rune(right)) < 2 {
		return 0
	}
	grams := func(value string) []string {
		runes := []rune(value)
		result := make([]string, 0, len(runes)-1)
		for index := 1; index < len(runes); index++ {
			result = append(result, string(runes[index-1:index+1]))
		}
		return result
	}
	a, b := grams(left), grams(right)
	counts := make(map[string]int, len(a))
	for _, gram := range a {
		counts[gram]++
	}
	shared := 0
	for _, gram := range b {
		if counts[gram] > 0 {
			shared++
			counts[gram]--
		}
	}
	return 2 * float64(shared) / float64(len(a)+len(b))
}

func collisionTokens(value string) []string {
	return strings.FieldsFunc(strings.ToLower(value), func(character rune) bool {
		return !unicode.IsLetter(character) && !unicode.IsDigit(character)
	})
}

func boxIoU(left, right ocr.OCRBox) float64 {
	intersection := boxIntersectionArea(left, right)
	union := left.Width*left.Height + right.Width*right.Height - intersection
	return float64(intersection) / float64(max(1, union))
}

func normalizedCenterDistance(left, right ocr.OCRBox) float64 {
	distance := math.Hypot(float64((left.X+left.Width/2)-(right.X+right.Width/2)), float64((left.Y+left.Height/2)-(right.Y+right.Height/2)))
	return distance / float64(max(1, min(max(left.Width, left.Height), max(right.Width, right.Height))))
}

func baselineCompatible(left, right ocr.OCRBox) bool {
	lineHeight := max(1, min(left.Height, right.Height))
	return absInt((left.Y+left.Height)-(right.Y+right.Height)) <= lineHeight/2+1
}

func acceptedWordCount(paragraph ocr.OCRParagraph) int {
	count := 0
	for _, line := range paragraph.Lines {
		for _, word := range line.Words {
			if word.Accepted {
				count++
			}
		}
	}
	return count
}

func averageDetectorConfidence(paragraph ocr.OCRParagraph) float64 {
	total, count := 0.0, 0
	for _, line := range paragraph.Lines {
		for _, word := range line.Words {
			if word.DetectorConfidence > 0 {
				total, count = total+word.DetectorConfidence, count+1
			}
		}
	}
	if count == 0 {
		return 0
	}
	return total / float64(count)
}

func geometryQuality(geometry SourceTextGeometry) int {
	switch geometry.Level {
	case "word":
		return 300 + len(geometry.Polygons)*2 + len(geometry.Regions)
	case "line":
		return 200 + len(geometry.Regions)
	default:
		return 100
	}
}

func normalizedCollisionText(value string) string {
	return strings.Map(func(character rune) rune {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			return unicode.ToLower(character)
		}
		return -1
	}, value)
}

func sourceGeometryOverlapRatio(left, right SourceTextGeometry) float64 {
	if len(left.Polygons) > 0 && len(right.Polygons) > 0 {
		intersection := 0.0
		for _, a := range left.Polygons {
			for _, b := range right.Polygons {
				intersection += convexPolygonIntersectionArea(a, b)
			}
		}
		leftArea, rightArea := 0.0, 0.0
		for _, polygon := range left.Polygons {
			leftArea += polygonArea(polygon[:])
		}
		for _, polygon := range right.Polygons {
			rightArea += polygonArea(polygon[:])
		}
		return roundMetric(math.Min(1, intersection/math.Max(1, math.Min(leftArea, rightArea))))
	}
	intersection := 0
	for _, a := range left.Regions {
		for _, b := range right.Regions {
			intersection += boxIntersectionArea(a, b)
		}
	}
	leftArea, rightArea := regionArea(left.Regions), regionArea(right.Regions)
	return roundMetric(math.Min(1, float64(intersection)/float64(max(1, min(leftArea, rightArea)))))
}

func regionArea(regions []ocr.OCRBox) int {
	area := 0
	for _, region := range regions {
		area += region.Width * region.Height
	}
	return area
}

func smallerBoxOverlapRatio(left, right ocr.OCRBox) float64 {
	return roundMetric(float64(boxIntersectionArea(left, right)) / float64(max(1, min(left.Width*left.Height, right.Width*right.Height))))
}

func boxIntersection(left, right ocr.OCRBox) ocr.OCRBox {
	x, y := max(left.X, right.X), max(left.Y, right.Y)
	rightEdge, bottom := min(left.X+left.Width, right.X+right.Width), min(left.Y+left.Height, right.Y+right.Height)
	if rightEdge <= x || bottom <= y {
		return ocr.OCRBox{}
	}
	return ocr.OCRBox{X: x, Y: y, Width: rightEdge - x, Height: bottom - y}
}

func protectedSourceRegions(geometries []SourceTextGeometry, own int, rejected map[int]collisionResolution) []ocr.OCRBox {
	regions := make([]ocr.OCRBox, 0)
	for index, geometry := range geometries {
		if index == own {
			continue
		}
		if _, ok := rejected[index]; ok {
			continue
		}
		regions = append(regions, geometry.Regions...)
	}
	return regions
}

func safeTranslationBase(geometry SourceTextGeometry, protected []ocr.OCRBox) ocr.OCRBox {
	if !intersectsAny(geometry.Bounds, protected) {
		return geometry.Bounds
	}
	candidates := geometry.LineRegions
	if len(candidates) == 0 {
		candidates = geometry.Regions
	}
	best := ocr.OCRBox{}
	for _, candidate := range candidates {
		if intersectsAny(candidate, protected) {
			continue
		}
		if candidate.Width*candidate.Height > best.Width*best.Height {
			best = candidate
		}
	}
	if best.Width > 0 && best.Height > 0 {
		return best
	}
	return geometry.Bounds
}

func renderedLineRegions(lines []RenderLineLayout, lineHeight, ascent int) []ocr.OCRBox {
	regions := make([]ocr.OCRBox, 0, len(lines))
	for _, line := range lines {
		if line.Width <= 0 {
			continue
		}
		regions = append(regions, ocr.OCRBox{X: line.X, Y: line.BaselineY - ascent, Width: line.Width, Height: lineHeight})
	}
	return regions
}

func intersectsRegionSets(left, right []ocr.OCRBox) bool {
	for _, candidate := range left {
		if intersectsAny(candidate, right) {
			return true
		}
	}
	return false
}

func polygonValid(polygon ocr.OCRPolygon) bool { return polygonArea(polygon[:]) > .5 }

func transformPolygon(polygon ocr.OCRPolygon, transform CoordinateTransform) ocr.OCRPolygon {
	var result ocr.OCRPolygon
	for index, point := range polygon {
		result[index] = ocr.OCRPoint{X: point.X * transform.ScaleX, Y: point.Y * transform.ScaleY}
	}
	return result
}

func convexPolygonIntersectionArea(subject, clip ocr.OCRPolygon) float64 {
	output := append([]ocr.OCRPoint(nil), subject[:]...)
	orientation := polygonSignedArea(clip[:])
	for index, edgeStart := range clip {
		edgeEnd := clip[(index+1)%len(clip)]
		input := output
		output = nil
		if len(input) == 0 {
			break
		}
		previous := input[len(input)-1]
		for _, current := range input {
			currentInside := polygonInside(current, edgeStart, edgeEnd, orientation)
			previousInside := polygonInside(previous, edgeStart, edgeEnd, orientation)
			if currentInside {
				if !previousInside {
					output = append(output, lineIntersection(previous, current, edgeStart, edgeEnd))
				}
				output = append(output, current)
			} else if previousInside {
				output = append(output, lineIntersection(previous, current, edgeStart, edgeEnd))
			}
			previous = current
		}
	}
	return polygonArea(output)
}

func polygonInside(point, edgeStart, edgeEnd ocr.OCRPoint, orientation float64) bool {
	cross := (edgeEnd.X-edgeStart.X)*(point.Y-edgeStart.Y) - (edgeEnd.Y-edgeStart.Y)*(point.X-edgeStart.X)
	if orientation >= 0 {
		return cross >= -1e-7
	}
	return cross <= 1e-7
}

func lineIntersection(a, b, c, d ocr.OCRPoint) ocr.OCRPoint {
	dx1, dy1, dx2, dy2 := b.X-a.X, b.Y-a.Y, d.X-c.X, d.Y-c.Y
	denominator := dx1*dy2 - dy1*dx2
	if math.Abs(denominator) < 1e-9 {
		return b
	}
	t := ((c.X-a.X)*dy2 - (c.Y-a.Y)*dx2) / denominator
	return ocr.OCRPoint{X: a.X + t*dx1, Y: a.Y + t*dy1}
}

func polygonSignedArea(points []ocr.OCRPoint) float64 {
	area := 0.0
	for index, point := range points {
		next := points[(index+1)%len(points)]
		area += point.X*next.Y - next.X*point.Y
	}
	return area / 2
}

func polygonArea(points []ocr.OCRPoint) float64 {
	if len(points) < 3 {
		return 0
	}
	return math.Abs(polygonSignedArea(points))
}
