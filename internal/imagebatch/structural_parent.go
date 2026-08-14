package imagebatch

import (
	"fmt"
	"image"
	"strings"
	"unicode"

	"github.com/sympllate/translator/internal/ocr"
)

const structuralEdgeSupport = 0.62

type structuralEdgeMap struct {
	width, height int
	horizontal    []uint32
	vertical      []uint32
}

func inferStructuralParents(source *image.NRGBA, paragraphs []ocr.OCRParagraph, geometries []SourceTextGeometry) []StructuralParent {
	parents := make([]StructuralParent, len(geometries))
	edges := newStructuralEdgeMap(source)
	for index, geometry := range geometries {
		base := geometry.Bounds
		lineHeight := max(1, base.Height/max(1, len(paragraphs[index].Lines)))
		if bounds, ok := edges.enclosingRectangle(base, lineHeight); ok {
			parentType := classifyStructuralRectangle(index, bounds, lineHeight, paragraphs, geometries)
			parent := newStructuralParent(parentType, bounds, "stable_border_rectangle", source.Bounds().Dx(), source.Bounds().Dy())
			if parentType == "table_cell" {
				parent.SourceCell = parent.ID
			}
			parents[index] = parent
			continue
		}
		if bounds, column, ok := inferDocumentColumn(base, geometries, source.Bounds().Dx(), source.Bounds().Dy()); ok {
			parent := newStructuralParent("document_column", bounds, "ocr_column_cluster", source.Bounds().Dx(), source.Bounds().Dy())
			parent.SourceColumn = column
			parents[index] = parent
			continue
		}
		padding := lineHeight * 3
		bounds := ClampBox(ExpandBox(base, CleanupPadding{Horizontal: padding, Vertical: padding}, source.Bounds().Dx(), source.Bounds().Dy()), source.Bounds().Dx(), source.Bounds().Dy())
		parents[index] = newStructuralParent("local_text_region", bounds, "source_text_cluster", source.Bounds().Dx(), source.Bounds().Dy())
	}
	return parents
}

func newStructuralParent(parentType string, bounds ocr.OCRBox, detection string, width, height int) StructuralParent {
	bounds = ClampBox(bounds, width, height)
	return StructuralParent{
		ID:   fmt.Sprintf("%s-%d-%d-%d-%d", parentType, bounds.X, bounds.Y, bounds.Width, bounds.Height),
		Type: parentType, Bounds: bounds, Detection: detection, MaximumLocalExpansion: bounds,
	}
}

func uniqueStructuralParents(parents []StructuralParent) []StructuralParent {
	seen := make(map[string]struct{}, len(parents))
	result := make([]StructuralParent, 0, len(parents))
	for _, parent := range parents {
		if parent.ID == "" {
			continue
		}
		if _, ok := seen[parent.ID]; ok {
			continue
		}
		seen[parent.ID] = struct{}{}
		result = append(result, parent)
	}
	return result
}

func newStructuralEdgeMap(source *image.NRGBA) structuralEdgeMap {
	width, height := source.Bounds().Dx(), source.Bounds().Dy()
	result := structuralEdgeMap{width: width, height: height, horizontal: make([]uint32, height*(width+1)), vertical: make([]uint32, width*(height+1))}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			center := structuralLuminance(source.NRGBAAt(x, y))
			up, down := center, center
			left, right := center, center
			if y > 0 {
				up = structuralLuminance(source.NRGBAAt(x, y-1))
			}
			if y+1 < height {
				down = structuralLuminance(source.NRGBAAt(x, y+1))
			}
			if x > 0 {
				left = structuralLuminance(source.NRGBAAt(x-1, y))
			}
			if x+1 < width {
				right = structuralLuminance(source.NRGBAAt(x+1, y))
			}
			horizontal := max(absInt(center-up), absInt(center-down)) >= 18
			vertical := max(absInt(center-left), absInt(center-right)) >= 18
			row := y*(width+1) + x
			result.horizontal[row+1] = result.horizontal[row]
			if horizontal {
				result.horizontal[row+1]++
			}
			column := x*(height+1) + y
			result.vertical[column+1] = result.vertical[column]
			if vertical {
				result.vertical[column+1]++
			}
		}
	}
	return result
}

func structuralLuminance(value interface {
	RGBA() (uint32, uint32, uint32, uint32)
}) int {
	r, g, b, _ := value.RGBA()
	return int((299*r + 587*g + 114*b) / 1000 >> 8)
}

func (edges structuralEdgeMap) enclosingRectangle(base ocr.OCRBox, lineHeight int) (ocr.OCRBox, bool) {
	if base.Width <= 0 || base.Height <= 0 {
		return ocr.OCRBox{}, false
	}
	searchX := min(edges.width/3, max(lineHeight*12, base.Width/2))
	searchY := min(edges.height/3, max(lineHeight*12, base.Height*2))
	left := edges.findVertical(base.X-1, max(0, base.X-searchX), -1, base.Y, base.Y+base.Height)
	right := edges.findVertical(base.X+base.Width, min(edges.width-1, base.X+base.Width+searchX), 1, base.Y, base.Y+base.Height)
	top := edges.findHorizontal(base.Y-1, max(0, base.Y-searchY), -1, base.X, base.X+base.Width)
	bottom := edges.findHorizontal(base.Y+base.Height, min(edges.height-1, base.Y+base.Height+searchY), 1, base.X, base.X+base.Width)
	if left < 0 || right < 0 || top < 0 || bottom < 0 || right-left < base.Width || bottom-top < base.Height {
		return ocr.OCRBox{}, false
	}
	bounds := ocr.OCRBox{X: left, Y: top, Width: right - left + 1, Height: bottom - top + 1}
	areaRatio := float64(bounds.Width*bounds.Height) / float64(max(1, edges.width*edges.height))
	if areaRatio > .72 || bounds.Width*100 >= edges.width*94 || bounds.Height*100 >= edges.height*94 {
		return ocr.OCRBox{}, false
	}
	return bounds, true
}

func (edges structuralEdgeMap) findHorizontal(start, limit, step, x0, x1 int) int {
	x0, x1 = max(0, x0), min(edges.width, x1)
	for y := start; y >= 0 && y < edges.height; y += step {
		if (step < 0 && y < limit) || (step > 0 && y > limit) {
			break
		}
		row := y * (edges.width + 1)
		if float64(edges.horizontal[row+x1]-edges.horizontal[row+x0])/float64(max(1, x1-x0)) >= structuralEdgeSupport {
			return y
		}
	}
	return -1
}

func (edges structuralEdgeMap) findVertical(start, limit, step, y0, y1 int) int {
	y0, y1 = max(0, y0), min(edges.height, y1)
	for x := start; x >= 0 && x < edges.width; x += step {
		if (step < 0 && x < limit) || (step > 0 && x > limit) {
			break
		}
		column := x * (edges.height + 1)
		if float64(edges.vertical[column+y1]-edges.vertical[column+y0])/float64(max(1, y1-y0)) >= structuralEdgeSupport {
			return x
		}
	}
	return -1
}

func classifyStructuralRectangle(own int, bounds ocr.OCRBox, lineHeight int, paragraphs []ocr.OCRParagraph, geometries []SourceTextGeometry) string {
	text := strings.ToUpper(strings.TrimSpace(paragraphs[own].Text))
	if text == "DANGER" || text == "WARNING" || text == "NOTE" {
		return "warning_header"
	}
	for index, paragraph := range paragraphs {
		if index == own {
			continue
		}
		heading := strings.ToUpper(strings.TrimSpace(paragraph.Text))
		box := geometries[index].Bounds
		if (heading == "DANGER" || heading == "WARNING") && box.Y+box.Height <= geometries[own].Bounds.Y && geometries[own].Bounds.Y-(box.Y+box.Height) <= lineHeight*2 && overlapPixels(box.X, box.X+box.Width, bounds.X, bounds.X+bounds.Width) > 0 {
			return "warning_body"
		}
	}
	contained, adjacent := 0, 0
	base := geometries[own].Bounds
	for index, geometry := range geometries {
		if index == own {
			continue
		}
		if boxContains(bounds, geometry.Bounds) {
			contained++
		}
		if overlapPixels(base.Y, base.Y+base.Height, geometry.Bounds.Y, geometry.Bounds.Y+geometry.Bounds.Height) > 0 && !boxContains(bounds, geometry.Bounds) && horizontalBoxDistance(base, geometry.Bounds) <= lineHeight*4 {
			adjacent++
		}
	}
	if adjacent > 0 {
		return "table_cell"
	}
	if contained >= 3 {
		return "ui_panel"
	}
	if bounds.Height <= lineHeight*3 {
		return "control_row"
	}
	return "bounded_region"
}

func inferDocumentColumn(base ocr.OCRBox, geometries []SourceTextGeometry, width, height int) (ocr.OCRBox, int, bool) {
	middle := width / 2
	left, right := false, false
	for _, geometry := range geometries {
		if geometry.Bounds.X+geometry.Bounds.Width < middle {
			left = true
		}
		if geometry.Bounds.X > middle {
			right = true
		}
	}
	if !left || !right {
		return ocr.OCRBox{}, 0, false
	}
	if base.X+base.Width <= middle {
		return ocr.OCRBox{Width: middle, Height: height}, 1, true
	}
	if base.X >= middle {
		return ocr.OCRBox{X: middle, Width: width - middle, Height: height}, 2, true
	}
	return ocr.OCRBox{}, 0, false
}

func boxContains(outer, inner ocr.OCRBox) bool {
	return inner.X >= outer.X && inner.Y >= outer.Y && inner.X+inner.Width <= outer.X+outer.Width && inner.Y+inner.Height <= outer.Y+outer.Height
}

func horizontalBoxDistance(left, right ocr.OCRBox) int {
	return max(0, max(left.X-right.X-right.Width, right.X-left.X-left.Width))
}

func clampBoxToBounds(box, bounds ocr.OCRBox) ocr.OCRBox {
	x0, y0 := max(box.X, bounds.X), max(box.Y, bounds.Y)
	x1, y1 := min(box.X+box.Width, bounds.X+bounds.Width), min(box.Y+box.Height, bounds.Y+bounds.Height)
	if x1 <= x0 || y1 <= y0 {
		return ocr.OCRBox{}
	}
	return ocr.OCRBox{X: x0, Y: y0, Width: x1 - x0, Height: y1 - y0}
}

func containmentViolationPixels(box, bounds ocr.OCRBox) int {
	inside := boxIntersectionArea(box, bounds)
	return max(0, box.Width*box.Height-inside)
}

func overlapsContainerBorder(box ocr.OCRBox, parent StructuralParent) bool {
	if parent.Detection != "stable_border_rectangle" {
		return false
	}
	bounds := parent.Bounds
	if containmentViolationPixels(box, bounds) > 0 {
		return true
	}
	margin := max(1, min(bounds.Width, bounds.Height)/100)
	return box.X < bounds.X+margin || box.Y < bounds.Y+margin || box.X+box.Width > bounds.X+bounds.Width-margin || box.Y+box.Height > bounds.Y+bounds.Height-margin
}

func contextualFontOutlier(estimate FontStyleEstimate) bool {
	return estimate.Normalized && estimate.InitialFontSize > 0 && (estimate.InitialFontSize > estimate.FontSize*1.55 || estimate.InitialFontSize < estimate.FontSize*.65)
}

func detachedPaddleContinuation(paragraphs []ocr.OCRParagraph, own int) bool {
	if own <= 0 || len(paragraphs[own].Lines) != 1 {
		return false
	}
	current := strings.TrimSpace(paragraphs[own].Text)
	previous := strings.TrimSpace(paragraphs[own-1].Text)
	if current == "" || previous == "" {
		return false
	}
	currentRunes, previousRunes := []rune(current), []rune(previous)
	first, last := currentRunes[0], previousRunes[len(previousRunes)-1]
	if !unicode.IsLower(first) || strings.ContainsRune(".!?:;", last) {
		return false
	}
	a, b := paragraphs[own-1].Box, paragraphs[own].Box
	height := max(a.Height/max(1, len(paragraphs[own-1].Lines)), b.Height)
	return b.Y-(a.Y+a.Height) <= height/3 && absInt(a.X-b.X) <= height
}
