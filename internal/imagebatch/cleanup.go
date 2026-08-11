package imagebatch

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"sort"
	"time"

	"github.com/sympllate/translator/internal/ocr"
)

const (
	cleanupPaddingHorizontal  = 4
	cleanupPaddingVertical    = 3
	backgroundSampleWidth     = 16
	minimumBackgroundSamples  = 16
	maximumBackgroundSamples  = 4096
	uniformVarianceThreshold  = 25.0
	textMaskDilationRadius    = 1
	neuralContextPadding      = 48
	neuralClusterGap          = 24
	maximumClusterDimension   = 1024
	maximumClusterArea        = 768 * 1024
	maximumRegionDimension    = 768
	textMaskLowConfidenceCode = "text_mask_low_confidence"
	textMaskUnsafeCode        = "text_mask_unsafe"
)

var (
	errTextMaskLowConfidence = errors.New("text mask confidence is too low")
	errTextMaskUnsafe        = errors.New("text mask is unsafe")
)

type CleanupStats struct {
	UniformRegions int
	NeuralRegions  int
	NeuralClusters int
	Preprocessing  time.Duration
	Inference      time.Duration
	Postprocessing time.Duration
}

type maskedRegion struct {
	mask   *image.Gray
	bounds image.Rectangle
}

type regionCluster struct {
	indices []int
	bounds  image.Rectangle
}

func cleanupModeFor(sample BackgroundSample) CleanupMode {
	if sample.Variance <= uniformVarianceThreshold {
		return CleanupSolid
	}
	return CleanupNeural
}

func (r *Renderer) Clean(ctx context.Context, source *image.NRGBA, document RenderDocument) (*image.NRGBA, RenderDocument, CleanupStats, error) {
	if err := ctx.Err(); err != nil {
		return nil, document, CleanupStats{}, err
	}
	if source == nil {
		return nil, document, CleanupStats{}, errors.New("cleanup source image is nil")
	}
	target := cloneNRGBA(source)
	stats := CleanupStats{}
	regions := make([]maskedRegion, 0, len(document.Blocks))
	filtered := document
	filtered.Blocks = make([]RenderBlock, 0, len(document.Blocks))
	filtered.SkippedBlocks = append([]SkippedRenderBlock(nil), document.SkippedBlocks...)
	filtered.Warnings = append([]RenderWarning(nil), document.Warnings...)
	for _, block := range document.Blocks {
		if err := ctx.Err(); err != nil {
			return nil, filtered, stats, err
		}
		switch block.CleanupMode {
		case CleanupSolid:
			stats.UniformRegions++
			filtered.Blocks = append(filtered.Blocks, block)
		case CleanupNeural:
			mask, err := buildTextMask(ctx, source, block)
			if err != nil {
				code := textMaskRejectionCode(err)
				if code == "" {
					return nil, filtered, stats, fmt.Errorf("build text mask for block %s: %w", block.ID, err)
				}
				filtered.SkippedBlocks = append(filtered.SkippedBlocks, SkippedRenderBlock{ID: block.ID, Reason: code})
				filtered.Warnings = append(filtered.Warnings, RenderWarning{Code: code, BlockID: block.ID})
				continue
			}
			regions = append(regions, splitMaskedRegion(mask)...)
			stats.NeuralRegions++
			filtered.Blocks = append(filtered.Blocks, block)
		default:
			return nil, filtered, stats, fmt.Errorf("block %s has unknown cleanup mode %q", block.ID, block.CleanupMode)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, filtered, stats, err
	}

	clusters := clusterMaskedRegions(regions, source.Bounds())
	stats.NeuralClusters = len(clusters)
	for _, cluster := range clusters {
		if err := ctx.Err(); err != nil {
			return nil, filtered, stats, err
		}
		cropBounds := expandRectangle(cluster.bounds, neuralContextPadding, source.Bounds())
		crop := cropNRGBA(source, cropBounds)
		mask := clusterMask(regions, cluster, cropBounds)
		result, err := r.inpainter.Inpaint(ctx, crop, mask)
		if err != nil {
			return nil, filtered, stats, fmt.Errorf("LaMa cleanup crop %v: %w", cropBounds, err)
		}
		stats.Preprocessing += result.Timings.Preprocessing
		stats.Inference += result.Timings.Inference
		stats.Postprocessing += result.Timings.Postprocessing
		compositeMasked(target, result.Image, mask, cropBounds.Min)
	}

	for _, block := range filtered.Blocks {
		if block.CleanupMode != CleanupSolid {
			continue
		}
		if err := solidCleanup(ctx, target, block.CleanupBox, block.Background.NRGBA()); err != nil {
			return nil, filtered, stats, fmt.Errorf("solid cleanup block %s: %w", block.ID, err)
		}
	}
	return target, filtered, stats, nil
}

func textMaskRejectionCode(err error) string {
	switch {
	case errors.Is(err, errTextMaskLowConfidence):
		return textMaskLowConfidenceCode
	case errors.Is(err, errTextMaskUnsafe):
		return textMaskUnsafeCode
	default:
		return ""
	}
}

func solidCleanup(ctx context.Context, target *image.NRGBA, box ocr.OCRBox, value color.NRGBA) error {
	clipped := ClampBox(box, target.Bounds().Dx(), target.Bounds().Dy())
	for y := clipped.Y; y < clipped.Y+clipped.Height; y++ {
		if y&31 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		for x := clipped.X; x < clipped.X+clipped.Width; x++ {
			target.SetNRGBA(x+target.Bounds().Min.X, y+target.Bounds().Min.Y, value)
		}
	}
	return nil
}

func buildTextMask(ctx context.Context, source *image.NRGBA, block RenderBlock) (*image.Gray, error) {
	box := ClampBox(block.SourceBox, source.Bounds().Dx(), source.Bounds().Dy())
	if box.Width <= 0 || box.Height <= 0 {
		return nil, errors.New("source box is outside the image")
	}
	maskBounds := expandRectangle(ocrRectangle(box), textMaskDilationRadius, source.Bounds())
	seed := image.NewGray(maskBounds)
	background := block.Background.NRGBA()
	foreground := block.Foreground.NRGBA()
	candidates := 0
	for y := box.Y; y < box.Y+box.Height; y++ {
		if (y-box.Y)&31 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		for x := box.X; x < box.X+box.Width; x++ {
			pixel := source.NRGBAAt(x+source.Bounds().Min.X, y+source.Bounds().Min.Y)
			if isTextPixel(pixel, foreground, background) {
				seed.SetGray(x+source.Bounds().Min.X, y+source.Bounds().Min.Y, color.Gray{Y: 255})
				candidates++
			}
		}
	}
	area := box.Width * box.Height
	minimum := max(2, area/1000)
	if candidates < minimum {
		return nil, fmt.Errorf("%w: selected %d of %d pixels", errTextMaskLowConfidence, candidates, area)
	}
	if candidates*10 > area*7 {
		return nil, fmt.Errorf("%w: selected %d of %d pixels", errTextMaskUnsafe, candidates, area)
	}
	return dilateMask(seed, textMaskDilationRadius), nil
}

func isTextPixel(pixel, foreground, background color.NRGBA) bool {
	if pixel.A == 0 {
		return false
	}
	backgroundDistance := colorDistanceSquared(pixel, background)
	foregroundDistance := colorDistanceSquared(pixel, foreground)
	luminanceDifference := math.Abs(Luminance(pixel) - Luminance(background))
	return luminanceDifference >= 0.045 && foregroundDistance*5 <= backgroundDistance*6
}

func colorDistanceSquared(left, right color.NRGBA) float64 {
	dr := float64(int(left.R) - int(right.R))
	dg := float64(int(left.G) - int(right.G))
	db := float64(int(left.B) - int(right.B))
	return dr*dr + dg*dg + db*db
}

func dilateMask(source *image.Gray, radius int) *image.Gray {
	if radius <= 0 {
		result := image.NewGray(source.Bounds())
		copy(result.Pix, source.Pix)
		return result
	}
	result := image.NewGray(source.Bounds())
	for y := source.Bounds().Min.Y; y < source.Bounds().Max.Y; y++ {
		for x := source.Bounds().Min.X; x < source.Bounds().Max.X; x++ {
			if source.GrayAt(x, y).Y == 0 {
				continue
			}
			for offsetY := -radius; offsetY <= radius; offsetY++ {
				for offsetX := -radius; offsetX <= radius; offsetX++ {
					point := image.Pt(x+offsetX, y+offsetY)
					if point.In(result.Bounds()) {
						result.SetGray(point.X, point.Y, color.Gray{Y: 255})
					}
				}
			}
		}
	}
	return result
}

func nonZeroBounds(mask *image.Gray) image.Rectangle {
	return nonZeroBoundsWithin(mask, mask.Bounds())
}

func nonZeroBoundsWithin(mask *image.Gray, bounds image.Rectangle) image.Rectangle {
	result := image.Rectangle{}
	found := false
	bounds = bounds.Intersect(mask.Bounds())
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if mask.GrayAt(x, y).Y == 0 {
				continue
			}
			if !found {
				result = image.Rect(x, y, x+1, y+1)
				found = true
			} else {
				result = result.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return result
}

func splitMaskedRegion(mask *image.Gray) []maskedRegion {
	bounds := nonZeroBounds(mask)
	if bounds.Empty() {
		return nil
	}
	regions := make([]maskedRegion, 0, (bounds.Dx()/maximumRegionDimension+1)*(bounds.Dy()/maximumRegionDimension+1))
	for y := bounds.Min.Y; y < bounds.Max.Y; y += maximumRegionDimension {
		for x := bounds.Min.X; x < bounds.Max.X; x += maximumRegionDimension {
			tile := image.Rect(x, y, min(x+maximumRegionDimension, bounds.Max.X), min(y+maximumRegionDimension, bounds.Max.Y))
			nonZero := nonZeroBoundsWithin(mask, tile)
			if !nonZero.Empty() {
				regions = append(regions, maskedRegion{mask: mask, bounds: nonZero})
			}
		}
	}
	return regions
}

func clusterMaskedRegions(regions []maskedRegion, imageBounds image.Rectangle) []regionCluster {
	order := make([]int, len(regions))
	for index := range regions {
		order[index] = index
	}
	sort.SliceStable(order, func(i, j int) bool {
		left, right := regions[order[i]].bounds, regions[order[j]].bounds
		if left.Min.Y != right.Min.Y {
			return left.Min.Y < right.Min.Y
		}
		return left.Min.X < right.Min.X
	})
	clusters := make([]regionCluster, 0, len(regions))
	for _, regionIndex := range order {
		regionBounds := regions[regionIndex].bounds
		merged := false
		for clusterIndex := range clusters {
			candidate := clusters[clusterIndex].bounds.Union(regionBounds)
			crop := expandRectangle(candidate, neuralContextPadding, imageBounds)
			if rectangleDistance(clusters[clusterIndex].bounds, regionBounds) > neuralContextPadding*2+neuralClusterGap ||
				crop.Dx() > maximumClusterDimension || crop.Dy() > maximumClusterDimension || crop.Dx()*crop.Dy() > maximumClusterArea {
				continue
			}
			clusters[clusterIndex].indices = append(clusters[clusterIndex].indices, regionIndex)
			clusters[clusterIndex].bounds = candidate
			merged = true
			break
		}
		if !merged {
			clusters = append(clusters, regionCluster{indices: []int{regionIndex}, bounds: regionBounds})
		}
	}
	return clusters
}

func rectangleDistance(left, right image.Rectangle) int {
	dx := max(0, max(left.Min.X-right.Max.X, right.Min.X-left.Max.X))
	dy := max(0, max(left.Min.Y-right.Max.Y, right.Min.Y-left.Max.Y))
	return int(math.Ceil(math.Hypot(float64(dx), float64(dy))))
}

func expandRectangle(value image.Rectangle, padding int, bounds image.Rectangle) image.Rectangle {
	return image.Rect(value.Min.X-padding, value.Min.Y-padding, value.Max.X+padding, value.Max.Y+padding).Intersect(bounds)
}

func ocrRectangle(box ocr.OCRBox) image.Rectangle {
	return image.Rect(box.X, box.Y, box.X+box.Width, box.Y+box.Height)
}

func cropNRGBA(source *image.NRGBA, bounds image.Rectangle) *image.NRGBA {
	result := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(result, result.Bounds(), source, bounds.Min, draw.Src)
	return result
}

func clusterMask(regions []maskedRegion, cluster regionCluster, cropBounds image.Rectangle) *image.Gray {
	result := image.NewGray(image.Rect(0, 0, cropBounds.Dx(), cropBounds.Dy()))
	for _, regionIndex := range cluster.indices {
		mask := regions[regionIndex].mask
		bounds := mask.Bounds().Intersect(cropBounds)
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				if mask.GrayAt(x, y).Y != 0 {
					result.SetGray(x-cropBounds.Min.X, y-cropBounds.Min.Y, color.Gray{Y: 255})
				}
			}
		}
	}
	return result
}

func compositeMasked(target, restored *image.NRGBA, mask *image.Gray, offset image.Point) {
	for y := mask.Bounds().Min.Y; y < mask.Bounds().Max.Y; y++ {
		for x := mask.Bounds().Min.X; x < mask.Bounds().Max.X; x++ {
			if mask.GrayAt(x, y).Y != 0 {
				target.SetNRGBA(offset.X+x, offset.Y+y, restored.NRGBAAt(x, y))
			}
		}
	}
}
