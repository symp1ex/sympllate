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
	"strings"
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
	neuralContextPadding      = 48
	neuralClusterGap          = 24
	maximumClusterDimension   = 1024
	maximumClusterArea        = 768 * 1024
	maximumRegionDimension    = 768
	textMaskLowConfidenceCode = "text_mask_low_confidence"
	textMaskUnsafeCode        = "text_mask_unsafe"
	textMaskOverlapCode       = "text_mask_overlap"
)

// cleanupMaskConfig groups the conservative, document-oriented mask
// tolerances. Ratios are relative to OCR text height so the same defaults work
// for small UI labels and high-resolution scans.
type cleanupMaskConfig struct {
	HorizontalPaddingMin        int     // Minimum word search padding on the x axis.
	HorizontalPaddingMax        int     // Maximum word search padding on the x axis.
	VerticalPaddingMin          int     // Minimum word search padding on the y axis.
	VerticalPaddingMax          int     // Maximum word search padding on the y axis.
	DilationMin                 int     // Minimum glyph-edge expansion, in pixels.
	DilationMax                 int     // Maximum glyph-edge expansion, in pixels.
	FringeBackgroundDelta       float64 // Minimum normalized RGB distance from the sampled background.
	FringeMaxDistance           int     // Maximum connected growth distance from strict text core.
	GraphicsBackgroundDelta     float64 // Minimum normalized RGB distance used to detect local rules.
	ProtectedPadding            int     // Safety margin around rejected graphics components.
	LineAspectRatio             float64 // Minimum length/thickness ratio for an obvious stroke.
	HorizontalLengthHeightRatio float64 // Minimum horizontal length relative to OCR text height.
	VerticalLengthHeightRatio   float64 // Minimum vertical length relative to OCR text height.
	MaxComponentHeightRatio     float64 // Largest removable component height relative to text height.
	MaxComponentAreaHeightRatio float64 // Largest removable area relative to squared text height.
	SuspiciousCleanupAreaRatio  float64 // Diagnostic threshold; does not expand or approve cleanup.
	MaxCleanupAreaRatio         float64 // Hard cap; exceeding it disables dilation or rejects cleanup.
	WordMaxCleanupAreaRatio     float64 // Word-local cap measured against the padded search region.
	WordMaxHoleAreaHeightRatio  float64 // Largest enclosed word-mask hole relative to squared text height.
}

var defaultCleanupMaskConfig = cleanupMaskConfig{
	HorizontalPaddingMin: 2, HorizontalPaddingMax: 5, VerticalPaddingMin: 1, VerticalPaddingMax: 4,
	DilationMin: 1, DilationMax: 3, FringeBackgroundDelta: 0.012, FringeMaxDistance: 3,
	GraphicsBackgroundDelta: 0.04, ProtectedPadding: 1,
	LineAspectRatio: 6, HorizontalLengthHeightRatio: 2.5, VerticalLengthHeightRatio: 0.8,
	MaxComponentHeightRatio: 1.35, MaxComponentAreaHeightRatio: 2,
	SuspiciousCleanupAreaRatio: 0.30, MaxCleanupAreaRatio: 0.45,
	WordMaxCleanupAreaRatio: 0.72, WordMaxHoleAreaHeightRatio: 0.45,
}

var (
	errTextMaskLowConfidence = errors.New("text mask confidence is too low")
	errTextMaskUnsafe        = errors.New("text mask is unsafe")
)

type CleanupStats struct {
	UniformRegions          int
	NeuralRegions           int
	NeuralClusters          int
	OCRRegionPixels         int
	CandidatePixels         int
	FinalCleanupPixels      int
	ProtectedGraphicsPixels int
	RejectedComponents      int
	SuspiciousBlocks        int
	CleanupPixelRatio       float64
	Preprocessing           time.Duration
	Inference               time.Duration
	Postprocessing          time.Duration
	Blocks                  []CleanupBlockStats
	Diagnostics             CleanupDiagnostics
}

type CleanupBlockStats struct {
	ID                   string
	OCRLevel             string
	Region               ocr.OCRBox
	RegionPixels         int
	CandidatePixels      int
	FinalCleanupPixels   int
	ProtectedPixels      int
	RejectedComponents   int
	CleanupPixelRatio    float64
	CleanupMode          CleanupMode
	ConservativeFallback bool
	Rejections           []CleanupComponentRejection
}

type CleanupComponentRejection struct {
	Box         ocr.OCRBox
	Width       int
	Height      int
	Area        int
	AspectRatio float64
	FillRatio   float64
	Reason      string
}

type CleanupDiagnostics struct {
	OCRRegions         *image.Gray
	CandidateMask      *image.Gray
	StrictTextCoreMask *image.Gray
	FringeRecoveryMask *image.Gray
	RejectedMask       *image.Gray
	ProtectedMask      *image.Gray
	FinalCleanupMask   *image.Gray
}

type maskBuildResult struct {
	mask                 *image.Gray
	regionMask           *image.Gray
	candidateMask        *image.Gray
	strictTextCoreMask   *image.Gray
	fringeRecoveryMask   *image.Gray
	rejectedMask         *image.Gray
	protectedMask        *image.Gray
	rejections           []CleanupComponentRejection
	regionBox            ocr.OCRBox
	ocrLevel             string
	conservativeFallback bool
}

type blockCleanup struct {
	block RenderBlock
	mask  *image.Gray
}

type maskComponent struct {
	pixels []image.Point
	bounds image.Rectangle
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
	stats := CleanupStats{Diagnostics: newCleanupDiagnostics(source.Bounds())}
	regions := make([]maskedRegion, 0, len(document.Blocks))
	neuralBlocks := make([]blockCleanup, 0, len(document.Blocks))
	solid := make([]blockCleanup, 0, len(document.Blocks))
	claimed := image.NewGray(source.Bounds())
	filtered := document
	filtered.Blocks = make([]RenderBlock, 0, len(document.Blocks))
	filtered.SkippedBlocks = append([]SkippedRenderBlock(nil), document.SkippedBlocks...)
	filtered.Warnings = append([]RenderWarning(nil), document.Warnings...)
	maskConfig := defaultCleanupMaskConfig
	for _, block := range document.Blocks {
		if err := ctx.Err(); err != nil {
			return nil, filtered, stats, err
		}
		if block.CleanupMode != CleanupSolid && block.CleanupMode != CleanupNeural {
			return nil, filtered, stats, fmt.Errorf("block %s has unknown cleanup mode %q", block.ID, block.CleanupMode)
		}
		ocrCleanupUnsafe := block.CleanupSafetyKnown && !block.CleanupSafe
		built, err := buildSafeTextMask(ctx, source, block, maskConfig)
		mergeMask(stats.Diagnostics.OCRRegions, built.regionMask)
		mergeMask(stats.Diagnostics.CandidateMask, built.candidateMask)
		mergeMask(stats.Diagnostics.StrictTextCoreMask, built.strictTextCoreMask)
		mergeMask(stats.Diagnostics.FringeRecoveryMask, built.fringeRecoveryMask)
		mergeMask(stats.Diagnostics.RejectedMask, built.rejectedMask)
		mergeMask(stats.Diagnostics.ProtectedMask, built.protectedMask)
		stats.RejectedComponents += len(built.rejections)
		if err != nil {
			stats.Blocks = append(stats.Blocks, cleanupBlockStats(block, built, 0))
			code := textMaskRejectionCode(err)
			if code == "" {
				return nil, filtered, stats, fmt.Errorf("build text mask for block %s: %w", block.ID, err)
			}
			if code == textMaskLowConfidenceCode {
				if mask, ok := conservativeSolidRegionMask(source.Bounds(), block, document.SourceGeometries, claimed); ok {
					mergeMask(claimed, mask)
					mergeMask(stats.Diagnostics.FinalCleanupMask, mask)
					finalPixels := countMask(mask)
					stats.Blocks[len(stats.Blocks)-1].FinalCleanupPixels = finalPixels
					stats.Blocks[len(stats.Blocks)-1].ConservativeFallback = true
					stats.UniformRegions++
					filtered.Blocks = append(filtered.Blocks, block)
					blockIndex := len(filtered.Blocks) - 1
					filtered.Blocks[blockIndex].Warning = appendWarningCode(filtered.Blocks[blockIndex].Warning, "cleanup_fallback_used")
					filtered.Warnings = append(filtered.Warnings, RenderWarning{Code: "cleanup_fallback_used", BlockID: block.ID})
					filtered.CleanupDiagnostics.ConservativeFallbackBlocks++
					appendBlockFate(&filtered, block.ID, BlockFateEvent{Stage: "cleanup", Decision: "fallback", Reason: code, Strategy: "conservative_solid_region"})
					solid = append(solid, blockCleanup{block: block, mask: mask})
					continue
				}
			}
			if ocrCleanupUnsafe {
				code = "cleanup_unsafe"
			}
			block.Warning = appendWarningCode(block.Warning, "cleanup_failed_rendered_anyway")
			block.Status = "renderable_cleanup_fallback"
			filtered.Blocks = append(filtered.Blocks, block)
			filtered.Warnings = append(filtered.Warnings, RenderWarning{Code: code, BlockID: block.ID}, RenderWarning{Code: "cleanup_failed_rendered_anyway", BlockID: block.ID}, RenderWarning{Code: "original_text_may_remain", BlockID: block.ID})
			filtered.CleanupDiagnostics.FailedRenderedBlocks++
			appendBlockFate(&filtered, block.ID, BlockFateEvent{Stage: "cleanup", Decision: "rendered_anyway", Reason: code, Strategy: "draw_over_original"})
			continue
		}
		mask := subtractMask(built.mask, claimed)
		finalPixels := countMask(mask)
		if finalPixels == 0 {
			block.Warning = appendWarningCode(block.Warning, "cleanup_failed_rendered_anyway")
			block.Status = "renderable_cleanup_fallback"
			filtered.Blocks = append(filtered.Blocks, block)
			filtered.Warnings = append(filtered.Warnings, RenderWarning{Code: textMaskOverlapCode, BlockID: block.ID}, RenderWarning{Code: "cleanup_failed_rendered_anyway", BlockID: block.ID}, RenderWarning{Code: "original_text_may_remain", BlockID: block.ID})
			filtered.CleanupDiagnostics.FailedRenderedBlocks++
			appendBlockFate(&filtered, block.ID, BlockFateEvent{Stage: "cleanup", Decision: "rendered_anyway", Reason: textMaskOverlapCode, Strategy: "draw_over_original"})
			continue
		}
		mergeMask(claimed, mask)
		mergeMask(stats.Diagnostics.FinalCleanupMask, mask)
		blockStats := cleanupBlockStats(block, built, finalPixels)
		stats.Blocks = append(stats.Blocks, blockStats)
		if blockStats.CleanupPixelRatio > defaultCleanupMaskConfig.SuspiciousCleanupAreaRatio {
			stats.SuspiciousBlocks++
		}
		filtered.Blocks = append(filtered.Blocks, block)
		appendBlockFate(&filtered, block.ID, BlockFateEvent{Stage: "cleanup", Decision: "cleaned", Strategy: string(block.CleanupMode)})
		if block.CleanupMode == CleanupSolid {
			stats.UniformRegions++
			solid = append(solid, blockCleanup{block: block, mask: mask})
		} else {
			stats.NeuralRegions++
			regions = append(regions, splitMaskedRegion(mask)...)
			neuralBlocks = append(neuralBlocks, blockCleanup{block: block, mask: mask})
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
			if ctx.Err() != nil {
				return nil, filtered, stats, ctx.Err()
			}
			// Neural cleanup is cosmetic. Preserve the original crop and proceed
			// to Draw rather than losing every successfully translated block.
			for _, cleanup := range neuralBlocks {
				cleanupBox := cleanup.block.CleanupBox
				if !image.Rect(cleanupBox.X, cleanupBox.Y, cleanupBox.X+cleanupBox.Width, cleanupBox.Y+cleanupBox.Height).Overlaps(cluster.bounds) {
					continue
				}
				for index := range filtered.Blocks {
					if filtered.Blocks[index].ID != cleanup.block.ID || strings.Contains(filtered.Blocks[index].Warning, "cleanup_failed_rendered_anyway") {
						continue
					}
					filtered.Blocks[index].Warning = appendWarningCode(filtered.Blocks[index].Warning, "cleanup_failed_rendered_anyway")
					filtered.Blocks[index].Status = "renderable_cleanup_fallback"
					filtered.Warnings = append(filtered.Warnings, RenderWarning{Code: "cleanup_failed_rendered_anyway", BlockID: cleanup.block.ID}, RenderWarning{Code: "original_text_may_remain", BlockID: cleanup.block.ID})
					filtered.CleanupDiagnostics.FailedRenderedBlocks++
					appendBlockFate(&filtered, cleanup.block.ID, BlockFateEvent{Stage: "cleanup", Decision: "rendered_anyway", Reason: "inpaint_failed", Strategy: "draw_over_original"})
				}
			}
			continue
		}
		stats.Preprocessing += result.Timings.Preprocessing
		stats.Inference += result.Timings.Inference
		stats.Postprocessing += result.Timings.Postprocessing
		compositeMasked(target, result.Image, mask, cropBounds.Min)
	}

	for _, cleanup := range solid {
		if err := solidCleanup(ctx, target, cleanup.mask, cleanup.block.Background.NRGBA()); err != nil {
			return nil, filtered, stats, fmt.Errorf("solid cleanup block %s: %w", cleanup.block.ID, err)
		}
	}
	stats.OCRRegionPixels = countMask(stats.Diagnostics.OCRRegions)
	stats.CandidatePixels = countMask(stats.Diagnostics.CandidateMask)
	stats.FinalCleanupPixels = countMask(stats.Diagnostics.FinalCleanupMask)
	stats.ProtectedGraphicsPixels = countMask(stats.Diagnostics.ProtectedMask)
	if stats.OCRRegionPixels > 0 {
		stats.CleanupPixelRatio = float64(stats.FinalCleanupPixels) / float64(stats.OCRRegionPixels)
	}
	renderedIDs, layoutFallbackIDs, cleanupFallbackIDs := make(map[string]struct{}), make(map[string]struct{}), make(map[string]struct{})
	for _, block := range filtered.Blocks {
		renderedIDs[block.ID] = struct{}{}
		if isLocalityFallbackStrategy(block.FallbackReason) {
			layoutFallbackIDs[block.ID] = struct{}{}
		}
		if strings.Contains(block.Warning, "cleanup_fallback_used") || strings.Contains(block.Warning, "cleanup_failed_rendered_anyway") {
			cleanupFallbackIDs[block.ID] = struct{}{}
		}
	}
	for _, warning := range filtered.Warnings {
		if warning.BlockID != "" && (warning.Code == "cleanup_fallback_used" || warning.Code == "cleanup_failed_rendered_anyway") {
			cleanupFallbackIDs[warning.BlockID] = struct{}{}
		}
	}
	filtered.PipelineMetrics.RenderedBlocks = len(renderedIDs)
	filtered.PipelineMetrics.FallbackRenderedBlocks = len(layoutFallbackIDs)
	filtered.PipelineMetrics.CleanupFallbackBlocks = len(cleanupFallbackIDs)
	filtered.PipelineMetrics.NormalRenderedBlocks = len(renderedIDs) - len(layoutFallbackIDs)
	filtered.PipelineMetrics.HardFailedBlocks = max(0, filtered.PipelineMetrics.TranslatedUniqueBlocks-len(renderedIDs)-filtered.PipelineMetrics.DeduplicatedBlocks-filtered.PipelineMetrics.PassthroughBlocks)
	if filtered.PipelineMetrics.TranslatedUniqueBlocks > 0 {
		if err := validatePipelineMetrics(filtered.PipelineMetrics); err != nil {
			return nil, filtered, stats, err
		}
	}
	return target, filtered, stats, nil
}

func appendWarningCode(existing, code string) string {
	if existing == "" {
		return code
	}
	for _, current := range strings.Split(existing, ",") {
		if current == code {
			return existing
		}
	}
	return existing + "," + code
}

func conservativeSolidRegionMask(bounds image.Rectangle, block RenderBlock, geometries []SourceTextGeometry, claimed *image.Gray) (*image.Gray, bool) {
	if block.CleanupMode != CleanupSolid || block.SourceGeometry.Level != "word" || len(block.CleanupRegions) == 0 {
		return nil, false
	}
	mask := image.NewGray(bounds)
	for _, region := range block.CleanupRegions {
		if region.Level != "word" || region.Box.Width <= 0 || region.Box.Height <= 0 {
			return nil, false
		}
		for _, geometry := range geometries {
			if geometry.ID == block.ID {
				continue
			}
			if intersectsAny(region.Box, geometry.Regions) {
				return nil, false
			}
		}
		fillMaskRectangle(mask, ocrRectangle(region.Box).Intersect(bounds))
	}
	mask = subtractMask(mask, claimed)
	return mask, countMask(mask) > 0
}

func warningsWithoutBlock(warnings []RenderWarning, blockID string) []RenderWarning {
	result := warnings[:0]
	for _, warning := range warnings {
		if warning.BlockID != blockID {
			result = append(result, warning)
		}
	}
	return result
}

func newCleanupDiagnostics(bounds image.Rectangle) CleanupDiagnostics {
	return CleanupDiagnostics{
		OCRRegions: image.NewGray(bounds), CandidateMask: image.NewGray(bounds),
		StrictTextCoreMask: image.NewGray(bounds), FringeRecoveryMask: image.NewGray(bounds),
		RejectedMask: image.NewGray(bounds), ProtectedMask: image.NewGray(bounds), FinalCleanupMask: image.NewGray(bounds),
	}
}

func cleanupBlockStats(block RenderBlock, built maskBuildResult, finalPixels int) CleanupBlockStats {
	regionPixels := countMask(built.regionMask)
	ratio := 0.0
	if regionPixels > 0 {
		ratio = float64(finalPixels) / float64(regionPixels)
	}
	return CleanupBlockStats{
		ID: block.ID, OCRLevel: built.ocrLevel, Region: built.regionBox, RegionPixels: regionPixels,
		CandidatePixels: countMask(built.candidateMask), FinalCleanupPixels: finalPixels,
		ProtectedPixels: countMask(built.protectedMask), RejectedComponents: len(built.rejections),
		CleanupPixelRatio: ratio, CleanupMode: block.CleanupMode,
		ConservativeFallback: built.conservativeFallback, Rejections: built.rejections,
	}
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

func solidCleanup(ctx context.Context, target *image.NRGBA, mask *image.Gray, value color.NRGBA) error {
	bounds := target.Bounds().Intersect(mask.Bounds())
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		if (y-bounds.Min.Y)&31 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if mask.GrayAt(x, y).Y != 0 {
				target.SetNRGBA(x, y, value)
			}
		}
	}
	return nil
}

func buildTextMask(ctx context.Context, source *image.NRGBA, block RenderBlock) (*image.Gray, error) {
	built, err := buildSafeTextMask(ctx, source, block, defaultCleanupMaskConfig)
	return built.mask, err
}

func buildSafeTextMask(ctx context.Context, source *image.NRGBA, block RenderBlock, config cleanupMaskConfig) (maskBuildResult, error) {
	result := maskBuildResult{}
	regions := block.CleanupRegions
	if len(regions) == 0 {
		fallback := block.SourceBox
		if fallback.Width <= 0 || fallback.Height <= 0 {
			fallback = block.CleanupBox
		}
		regions = []CleanupRegion{{Level: "paragraph", Box: fallback, TextHeight: fallback.Height}}
	}
	valid := make([]CleanupRegion, 0, len(regions))
	maskBounds := image.Rectangle{}
	for _, region := range regions {
		region.Box = ClampBox(region.Box, source.Bounds().Dx(), source.Bounds().Dy())
		if region.Box.Width <= 0 || region.Box.Height <= 0 {
			continue
		}
		valid = append(valid, region)
		search := ExpandBox(region.Box, cleanupSearchPadding(region, config), source.Bounds().Dx(), source.Bounds().Dy())
		searchBounds := ocrRectangle(search).Intersect(source.Bounds())
		if maskBounds.Empty() {
			maskBounds = searchBounds
		} else {
			maskBounds = maskBounds.Union(searchBounds)
		}
	}
	if len(valid) == 0 {
		return result, errors.New("source cleanup regions are outside the image")
	}
	result.regionMask = image.NewGray(maskBounds)
	result.candidateMask = image.NewGray(maskBounds)
	result.strictTextCoreMask = image.NewGray(maskBounds)
	result.fringeRecoveryMask = image.NewGray(maskBounds)
	result.rejectedMask = image.NewGray(maskBounds)
	result.protectedMask = image.NewGray(maskBounds)
	finalMask := image.NewGray(maskBounds)
	background := block.Background.NRGBA()
	foreground := block.Foreground.NRGBA()
	for _, region := range valid {
		box := region.Box
		if result.ocrLevel == "" {
			result.ocrLevel = region.Level
		} else if result.ocrLevel != region.Level {
			result.ocrLevel = "mixed"
		}
		result.regionBox = unionOCRBox(result.regionBox, box)
		fillMaskRectangle(result.regionMask, ocrRectangle(box))
		search := ExpandBox(box, cleanupSearchPadding(region, config), source.Bounds().Dx(), source.Bounds().Dy())
		searchBounds := ocrRectangle(search).Intersect(source.Bounds())
		allowed := image.NewGray(searchBounds)
		fillMaskRectangle(allowed, searchBounds)
		candidate := image.NewGray(searchBounds)
		graphicsCandidate := image.NewGray(searchBounds)
		for y := searchBounds.Min.Y; y < searchBounds.Max.Y; y++ {
			if (y-searchBounds.Min.Y)&31 == 0 {
				if err := ctx.Err(); err != nil {
					return maskBuildResult{}, err
				}
			}
			for x := searchBounds.Min.X; x < searchBounds.Max.X; x++ {
				pixel := source.NRGBAAt(x, y)
				if isTextPixel(pixel, foreground, background) {
					candidate.SetGray(x, y, color.Gray{Y: 255})
					result.candidateMask.SetGray(x, y, color.Gray{Y: 255})
				}
				if candidate.GrayAt(x, y).Y != 0 || (region.Level == "word" && colorDelta(pixel, background) >= config.GraphicsBackgroundDelta) {
					graphicsCandidate.SetGray(x, y, color.Gray{Y: 255})
				}
			}
		}
		textHeight := cleanupTextHeight(region)
		lineProtection := detectProtectedLinePixels(graphicsCandidate, textHeight, config)
		mergeMask(result.rejectedMask, lineProtection)
		for _, component := range connectedComponents(lineProtection) {
			reason := "graphics_line_network"
			if component.bounds.Dx() >= component.bounds.Dy()*2 {
				reason = "horizontal_graphics"
			} else if component.bounds.Dy() >= component.bounds.Dx()*2 {
				reason = "vertical_graphics"
			}
			result.rejections = append(result.rejections, componentRejection(component, reason))
		}
		componentCandidates := subtractMask(candidate, lineProtection)
		strictCore := image.NewGray(searchBounds)
		localProtected := image.NewGray(searchBounds)
		mergeMask(localProtected, lineProtection)
		for _, component := range connectedComponents(componentCandidates) {
			reason := rejectComponent(component, ocrRectangle(box), searchBounds, region.Level, textHeight, config)
			if reason == "" {
				setComponent(strictCore, component)
				continue
			}
			setComponent(result.rejectedMask, component)
			setComponent(localProtected, component)
			result.rejections = append(result.rejections, componentRejection(component, reason))
		}
		protectedPadding := config.ProtectedPadding
		if region.Level == "word" {
			protectedPadding = 0
		}
		localProtected = dilateMask(localProtected, protectedPadding)
		mergeMask(result.protectedMask, localProtected)
		mergeMask(result.strictTextCoreMask, strictCore)

		fringe, err := recoverTextFringe(ctx, source, strictCore, allowed, localProtected, foreground, background, cleanupFringeDistance(region, config), config.FringeBackgroundDelta)
		if err != nil {
			return maskBuildResult{}, err
		}
		mergeMask(result.fringeRecoveryMask, fringe)
		grown := image.NewGray(searchBounds)
		mergeMask(grown, strictCore)
		mergeMask(grown, fringe)
		mask := dilateMaskConstrained(grown, cleanupDilationRadius(region, config), allowed, localProtected)
		if region.Level == "word" {
			mask = fillEnclosedWordMaskHoles(mask, allowed, localProtected, textHeight, config.WordMaxHoleAreaHeightRatio)
		}
		areaLimit := cleanupAreaLimit(region, config)
		searchArea := max(1, searchBounds.Dx()*searchBounds.Dy())
		finalPixels := countMask(mask)
		if float64(finalPixels)/float64(searchArea) > areaLimit {
			result.conservativeFallback = true
			mask = subtractMask(grown, localProtected)
			if region.Level == "word" {
				mask = fillEnclosedWordMaskHoles(mask, allowed, localProtected, textHeight, config.WordMaxHoleAreaHeightRatio)
			}
			finalPixels = countMask(mask)
		}
		if finalPixels > 0 && float64(finalPixels)/float64(searchArea) > areaLimit {
			return result, fmt.Errorf("%w: selected %d of %d local pixels", errTextMaskUnsafe, finalPixels, searchArea)
		}
		mergeMask(finalMask, mask)
	}
	regionArea := countMask(result.regionMask)
	candidates := countMask(result.candidateMask)
	if candidates < max(2, regionArea/1000) {
		return result, fmt.Errorf("%w: selected %d of %d pixels", errTextMaskLowConfidence, candidates, regionArea)
	}
	finalPixels := countMask(finalMask)
	if finalPixels == 0 {
		if float64(candidates)/float64(regionArea) > config.MaxCleanupAreaRatio {
			return result, fmt.Errorf("%w: no text-like components survived validation", errTextMaskUnsafe)
		}
		return result, fmt.Errorf("%w: no text-like components survived validation", errTextMaskLowConfidence)
	}
	result.mask = finalMask
	return result, nil
}

func cleanupTextHeight(region CleanupRegion) int {
	if region.TextHeight > 0 {
		return region.TextHeight
	}
	return max(1, region.Box.Height)
}

func cleanupSearchPadding(region CleanupRegion, config cleanupMaskConfig) CleanupPadding {
	textHeight := cleanupTextHeight(region)
	switch region.Level {
	case "word":
		return CleanupPadding{
			Horizontal: clampInt(int(math.Round(float64(textHeight)*0.10)), config.HorizontalPaddingMin, config.HorizontalPaddingMax),
			Vertical:   clampInt(int(math.Round(float64(textHeight)*0.08)), config.VerticalPaddingMin, config.VerticalPaddingMax),
		}
	case "line":
		return CleanupPadding{
			Horizontal: clampInt(int(math.Round(float64(textHeight)*0.06)), 1, min(3, config.HorizontalPaddingMax)),
			Vertical:   clampInt(int(math.Round(float64(textHeight)*0.05)), 1, min(2, config.VerticalPaddingMax)),
		}
	default:
		return CleanupPadding{Horizontal: 1, Vertical: 1}
	}
}

func cleanupDilationRadius(region CleanupRegion, config cleanupMaskConfig) int {
	textHeight := cleanupTextHeight(region)
	radius := config.DilationMin
	switch region.Level {
	case "word":
		if textHeight > 12 {
			radius = 2
		}
		if textHeight > 40 {
			radius = 3
		}
	case "line":
		if textHeight > 24 {
			radius = 2
		}
		radius = min(radius, 2)
	default:
		radius = config.DilationMin
	}
	return clampInt(radius, config.DilationMin, config.DilationMax)
}

func cleanupFringeDistance(region CleanupRegion, config cleanupMaskConfig) int {
	textHeight := cleanupTextHeight(region)
	switch region.Level {
	case "word":
		return clampInt(int(math.Ceil(float64(textHeight)*0.08)), 2, config.FringeMaxDistance)
	case "line":
		if textHeight > 24 {
			return min(2, config.FringeMaxDistance)
		}
		return 1
	default:
		return 0
	}
}

func cleanupAreaLimit(region CleanupRegion, config cleanupMaskConfig) float64 {
	switch region.Level {
	case "word":
		return config.WordMaxCleanupAreaRatio
	case "line":
		return min(config.WordMaxCleanupAreaRatio, config.MaxCleanupAreaRatio+0.05)
	default:
		return config.MaxCleanupAreaRatio
	}
}

func fillEnclosedWordMaskHoles(mask, allowed, protected *image.Gray, textHeight int, maximumAreaHeightRatio float64) *image.Gray {
	result := image.NewGray(mask.Bounds())
	copy(result.Pix, mask.Pix)
	open := image.NewGray(mask.Bounds())
	for y := mask.Bounds().Min.Y; y < mask.Bounds().Max.Y; y++ {
		for x := mask.Bounds().Min.X; x < mask.Bounds().Max.X; x++ {
			if allowed.GrayAt(x, y).Y != 0 && mask.GrayAt(x, y).Y == 0 && protected.GrayAt(x, y).Y == 0 {
				open.SetGray(x, y, color.Gray{Y: 255})
			}
		}
	}
	protectedMargin := dilateMask(protected, 1)
	maximumArea := max(1, int(math.Ceil(float64(textHeight*textHeight)*maximumAreaHeightRatio)))
	for _, component := range connectedComponents(open) {
		if component.bounds.Min.X == mask.Bounds().Min.X || component.bounds.Min.Y == mask.Bounds().Min.Y ||
			component.bounds.Max.X == mask.Bounds().Max.X || component.bounds.Max.Y == mask.Bounds().Max.Y {
			continue
		}
		if len(component.pixels) > maximumArea || component.bounds.Dx() > textHeight || component.bounds.Dy() > textHeight {
			continue
		}
		if componentTouchesMask(component, protectedMargin) {
			continue
		}
		setComponent(result, component)
	}
	return result
}

func componentTouchesMask(component maskComponent, mask *image.Gray) bool {
	for _, point := range component.pixels {
		if mask.GrayAt(point.X, point.Y).Y != 0 {
			return true
		}
	}
	return false
}

func recoverTextFringe(ctx context.Context, source *image.NRGBA, strictCore, allowed, protected *image.Gray, foreground, background color.NRGBA, maxDistance int, backgroundDelta float64) (*image.Gray, error) {
	bounds := strictCore.Bounds()
	recovered := image.NewGray(bounds)
	if maxDistance <= 0 {
		return recovered, nil
	}
	width := bounds.Dx()
	distances := make([]int, width*bounds.Dy())
	for index := range distances {
		distances[index] = -1
	}
	queue := make([]image.Point, 0, countMask(strictCore))
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if strictCore.GrayAt(x, y).Y == 0 {
				continue
			}
			distances[(y-bounds.Min.Y)*width+x-bounds.Min.X] = 0
			queue = append(queue, image.Pt(x, y))
		}
	}
	neighbors := [...]image.Point{
		{X: -1, Y: -1}, {Y: -1}, {X: 1, Y: -1}, {X: -1},
		{X: 1}, {X: -1, Y: 1}, {Y: 1}, {X: 1, Y: 1},
	}
	for head := 0; head < len(queue); head++ {
		if head&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		point := queue[head]
		distance := distances[(point.Y-bounds.Min.Y)*width+point.X-bounds.Min.X]
		if distance >= maxDistance {
			continue
		}
		for _, offset := range neighbors {
			next := point.Add(offset)
			if !next.In(bounds) || allowed.GrayAt(next.X, next.Y).Y == 0 || protected.GrayAt(next.X, next.Y).Y != 0 {
				continue
			}
			index := (next.Y-bounds.Min.Y)*width + next.X - bounds.Min.X
			if distances[index] >= 0 {
				continue
			}
			if !isPossibleTextFringe(source.NRGBAAt(next.X, next.Y), foreground, background, backgroundDelta) {
				continue
			}
			distances[index] = distance + 1
			recovered.SetGray(next.X, next.Y, color.Gray{Y: 255})
			queue = append(queue, next)
		}
	}
	return recovered, nil
}

func isPossibleTextFringe(pixel, foreground, background color.NRGBA, minimumBackgroundDelta float64) bool {
	if pixel.A == 0 || colorDelta(pixel, background) < minimumBackgroundDelta {
		return false
	}
	directionR := float64(int(foreground.R) - int(background.R))
	directionG := float64(int(foreground.G) - int(background.G))
	directionB := float64(int(foreground.B) - int(background.B))
	deltaR := float64(int(pixel.R) - int(background.R))
	deltaG := float64(int(pixel.G) - int(background.G))
	deltaB := float64(int(pixel.B) - int(background.B))
	directionSquared := directionR*directionR + directionG*directionG + directionB*directionB
	if directionSquared == 0 {
		return false
	}
	dot := deltaR*directionR + deltaG*directionG + deltaB*directionB
	projection := dot / directionSquared
	if projection < 0.015 || projection > 1.20 {
		return false
	}
	deltaSquared := deltaR*deltaR + deltaG*deltaG + deltaB*deltaB
	return dot*dot >= deltaSquared*directionSquared*0.35
}

func colorDelta(left, right color.NRGBA) float64 {
	if left.A == 0 {
		return 0
	}
	return math.Sqrt(colorDistanceSquared(left, right)) / (math.Sqrt(3) * 255)
}

func clampInt(value, minimum, maximum int) int {
	if maximum < minimum {
		return minimum
	}
	return min(max(value, minimum), maximum)
}

func detectProtectedLinePixels(candidate *image.Gray, textHeight int, config cleanupMaskConfig) *image.Gray {
	bounds := candidate.Bounds()
	provisional := image.NewGray(bounds)
	minimumHorizontal := int(math.Ceil(float64(max(1, textHeight)) * config.HorizontalLengthHeightRatio))
	minimumVertical := int(math.Ceil(float64(max(1, textHeight)) * config.VerticalLengthHeightRatio))
	minimumAspectLength := int(math.Ceil(config.LineAspectRatio))
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for start := bounds.Min.X; start < bounds.Max.X; {
			if candidate.GrayAt(start, y).Y == 0 {
				start++
				continue
			}
			end := start + 1
			for end < bounds.Max.X && candidate.GrayAt(end, y).Y != 0 {
				end++
			}
			length := end - start
			touchesEdges := start == bounds.Min.X && end == bounds.Max.X
			if length >= minimumAspectLength && (length >= minimumHorizontal || touchesEdges) {
				fillMaskRectangle(provisional, image.Rect(start, y, end, y+1))
			}
			start = end
		}
	}
	for x := bounds.Min.X; x < bounds.Max.X; x++ {
		for start := bounds.Min.Y; start < bounds.Max.Y; {
			if candidate.GrayAt(x, start).Y == 0 {
				start++
				continue
			}
			end := start + 1
			for end < bounds.Max.Y && candidate.GrayAt(x, end).Y != 0 {
				end++
			}
			length := end - start
			touchesEdges := start == bounds.Min.Y && end == bounds.Max.Y
			if length >= minimumVertical && length >= minimumAspectLength && touchesEdges {
				fillMaskRectangle(provisional, image.Rect(x, start, x+1, end))
			}
			start = end
		}
	}
	protected := image.NewGray(bounds)
	for _, component := range connectedComponents(provisional) {
		width, height := component.bounds.Dx(), component.bounds.Dy()
		aspect := float64(max(width, height)) / float64(max(1, min(width, height)))
		fill := float64(len(component.pixels)) / float64(max(1, width*height))
		lineLike := aspect >= config.LineAspectRatio
		networkLike := max(width, height) >= max(6, textHeight) && fill <= 0.35
		if lineLike || networkLike {
			setComponent(protected, component)
		}
	}
	return protected
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

func connectedComponents(mask *image.Gray) []maskComponent {
	bounds := mask.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	visited := make([]bool, width*height)
	components := make([]maskComponent, 0)
	neighbors := [...]image.Point{
		{X: -1, Y: -1}, {Y: -1}, {X: 1, Y: -1}, {X: -1},
		{X: 1}, {X: -1, Y: 1}, {Y: 1}, {X: 1, Y: 1},
	}
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			index := (y-bounds.Min.Y)*width + x - bounds.Min.X
			if visited[index] || mask.GrayAt(x, y).Y == 0 {
				continue
			}
			visited[index] = true
			queue := []image.Point{{X: x, Y: y}}
			component := maskComponent{bounds: image.Rect(x, y, x+1, y+1)}
			for head := 0; head < len(queue); head++ {
				point := queue[head]
				component.pixels = append(component.pixels, point)
				component.bounds = component.bounds.Union(image.Rect(point.X, point.Y, point.X+1, point.Y+1))
				for _, offset := range neighbors {
					next := point.Add(offset)
					if !next.In(bounds) {
						continue
					}
					nextIndex := (next.Y-bounds.Min.Y)*width + next.X - bounds.Min.X
					if visited[nextIndex] || mask.GrayAt(next.X, next.Y).Y == 0 {
						continue
					}
					visited[nextIndex] = true
					queue = append(queue, next)
				}
			}
			components = append(components, component)
		}
	}
	return components
}

func rejectComponent(component maskComponent, ocrBounds, searchBounds image.Rectangle, level string, textHeight int, config cleanupMaskConfig) string {
	if component.bounds.Intersect(ocrBounds).Empty() {
		return "outside_ocr_region"
	}
	width, height := component.bounds.Dx(), component.bounds.Dy()
	textHeight = max(1, textHeight)
	thinLimit := max(2, int(math.Ceil(float64(textHeight)*0.20)))
	touchesHorizontalEdges := component.bounds.Min.X == searchBounds.Min.X && component.bounds.Max.X == searchBounds.Max.X
	touchesVerticalEdges := component.bounds.Min.Y == searchBounds.Min.Y && component.bounds.Max.Y == searchBounds.Max.Y
	horizontalLength := width >= int(math.Ceil(float64(textHeight)*config.HorizontalLengthHeightRatio)) || touchesHorizontalEdges
	verticalLength := height >= int(math.Ceil(float64(textHeight)*config.VerticalLengthHeightRatio)) && touchesVerticalEdges
	if height <= thinLimit && float64(width)/float64(max(1, height)) >= config.LineAspectRatio && horizontalLength {
		return "horizontal_graphics"
	}
	if width <= thinLimit && float64(height)/float64(max(1, width)) >= config.LineAspectRatio && verticalLength {
		return "vertical_graphics"
	}
	insideOCR := component.bounds.Intersect(ocrBounds)
	insideArea := max(0, insideOCR.Dx()) * max(0, insideOCR.Dy())
	wordTextPrior := level == "word" && insideArea*2 >= width*height &&
		!touchesHorizontalEdges && !touchesVerticalEdges &&
		float64(height) <= float64(textHeight)*1.5
	if wordTextPrior {
		return ""
	}
	if float64(height) > float64(textHeight)*config.MaxComponentHeightRatio ||
		float64(len(component.pixels)) > float64(textHeight*textHeight)*config.MaxComponentAreaHeightRatio {
		return "component_too_large"
	}
	fill := float64(len(component.pixels)) / float64(max(1, width*height))
	complexWidthRatio := 3
	if level == "paragraph" {
		complexWidthRatio = 2
	}
	if width > textHeight*complexWidthRatio && fill < 0.20 {
		return "complex_graphics"
	}
	if (touchesHorizontalEdges || touchesVerticalEdges) && max(width, height) > textHeight && fill < 0.35 {
		return "component_extends_outside_ocr"
	}
	return ""
}

func componentRejection(component maskComponent, reason string) CleanupComponentRejection {
	width, height := component.bounds.Dx(), component.bounds.Dy()
	aspect := float64(max(width, height)) / float64(max(1, min(width, height)))
	fill := float64(len(component.pixels)) / float64(max(1, width*height))
	return CleanupComponentRejection{
		Box:   ocr.OCRBox{X: component.bounds.Min.X, Y: component.bounds.Min.Y, Width: width, Height: height},
		Width: width, Height: height, Area: len(component.pixels), AspectRatio: aspect, FillRatio: fill, Reason: reason,
	}
}

func setComponent(mask *image.Gray, component maskComponent) {
	for _, point := range component.pixels {
		mask.SetGray(point.X, point.Y, color.Gray{Y: 255})
	}
}

func unionOCRBox(left, right ocr.OCRBox) ocr.OCRBox {
	if left.Width <= 0 || left.Height <= 0 {
		return right
	}
	rectangle := ocrRectangle(left).Union(ocrRectangle(right))
	return ocr.OCRBox{X: rectangle.Min.X, Y: rectangle.Min.Y, Width: rectangle.Dx(), Height: rectangle.Dy()}
}

func fillMaskRectangle(mask *image.Gray, rectangle image.Rectangle) {
	rectangle = rectangle.Intersect(mask.Bounds())
	for y := rectangle.Min.Y; y < rectangle.Max.Y; y++ {
		for x := rectangle.Min.X; x < rectangle.Max.X; x++ {
			mask.SetGray(x, y, color.Gray{Y: 255})
		}
	}
}

func mergeMask(target, source *image.Gray) {
	if target == nil || source == nil {
		return
	}
	bounds := target.Bounds().Intersect(nonZeroBounds(source))
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if source.GrayAt(x, y).Y != 0 {
				target.SetGray(x, y, color.Gray{Y: 255})
			}
		}
	}
}

func subtractMask(source, excluded *image.Gray) *image.Gray {
	result := image.NewGray(source.Bounds())
	bounds := source.Bounds().Intersect(excluded.Bounds())
	sourceBounds := nonZeroBounds(source)
	for y := sourceBounds.Min.Y; y < sourceBounds.Max.Y; y++ {
		for x := sourceBounds.Min.X; x < sourceBounds.Max.X; x++ {
			if source.GrayAt(x, y).Y == 0 || (image.Pt(x, y).In(bounds) && excluded.GrayAt(x, y).Y != 0) {
				continue
			}
			result.SetGray(x, y, color.Gray{Y: 255})
		}
	}
	return result
}

func countMask(mask *image.Gray) int {
	if mask == nil {
		return 0
	}
	count := 0
	bounds := nonZeroBounds(mask)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if mask.GrayAt(x, y).Y != 0 {
				count++
			}
		}
	}
	return count
}

func dilateMaskConstrained(source *image.Gray, radius int, allowed, protected *image.Gray) *image.Gray {
	dilated := dilateMask(source, radius)
	result := image.NewGray(dilated.Bounds())
	bounds := nonZeroBounds(dilated)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if dilated.GrayAt(x, y).Y == 0 || allowed.GrayAt(x, y).Y == 0 || protected.GrayAt(x, y).Y != 0 {
				continue
			}
			result.SetGray(x, y, color.Gray{Y: 255})
		}
	}
	return result
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
	bounds := nonZeroBounds(source)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
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
