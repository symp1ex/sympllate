package imagebatch

import (
	"image/color"

	"github.com/sympllate/translator/internal/ocr"
)

type RenderConfig struct {
	MinimumFontSize       float64
	MaximumFontSize       float64
	LineSpacing           float64
	HorizontalTextPadding int
	VerticalTextPadding   int
	JPEGQuality           int
	Layout                LayoutConfig
}

// LayoutConfig groups the document-layout tolerances that are intentionally
// not exposed as user-facing settings. MinimumFontSize and MaximumFontSize
// remain the hard compatibility limits.
type LayoutConfig struct {
	MaximumUpscaleRatio  float64
	PreferredShrinkRatio float64
}

func DefaultRenderConfig() RenderConfig {
	return RenderConfig{
		MinimumFontSize: 7, MaximumFontSize: 48, LineSpacing: 1.15,
		HorizontalTextPadding: 2, VerticalTextPadding: 2, JPEGQuality: 92,
		Layout: LayoutConfig{MaximumUpscaleRatio: 1.05, PreferredShrinkRatio: 0.85},
	}
}

func (c RenderConfig) validate() error {
	if c.HorizontalTextPadding < 0 || c.VerticalTextPadding < 0 {
		return errInvalidRenderConfig("padding cannot be negative")
	}
	if c.MinimumFontSize <= 0 || c.MaximumFontSize < c.MinimumFontSize || c.LineSpacing < 1 || c.JPEGQuality < 1 || c.JPEGQuality > 100 {
		return errInvalidRenderConfig("font or encoding limits are invalid")
	}
	if c.Layout.MaximumUpscaleRatio < 1 || c.Layout.PreferredShrinkRatio <= 0 || c.Layout.PreferredShrinkRatio > 1 {
		return errInvalidRenderConfig("layout font-size ratios are invalid")
	}
	return nil
}

type renderConfigError string

func (e renderConfigError) Error() string { return string(e) }
func errInvalidRenderConfig(message string) error {
	return renderConfigError("invalid image render configuration: " + message)
}

type CoordinateTransform struct {
	SourceWidth  int     `json:"sourceWidth"`
	SourceHeight int     `json:"sourceHeight"`
	OCRWidth     int     `json:"ocrWidth"`
	OCRHeight    int     `json:"ocrHeight"`
	ScaleX       float64 `json:"scaleX"`
	ScaleY       float64 `json:"scaleY"`
}

type RenderColor struct {
	R uint8 `json:"r"`
	G uint8 `json:"g"`
	B uint8 `json:"b"`
	A uint8 `json:"a"`
}

func newRenderColor(value color.NRGBA) RenderColor {
	return RenderColor{R: value.R, G: value.G, B: value.B, A: value.A}
}

func (c RenderColor) NRGBA() color.NRGBA { return color.NRGBA{R: c.R, G: c.G, B: c.B, A: c.A} }

type RenderDocument struct {
	SchemaVersion      int                       `json:"schemaVersion"`
	SourceFile         string                    `json:"sourceFile"`
	ImageWidth         int                       `json:"imageWidth"`
	ImageHeight        int                       `json:"imageHeight"`
	Transform          CoordinateTransform       `json:"coordinateTransform"`
	OCRBackend         string                    `json:"ocrBackend,omitempty"`
	SourceGeometries   []SourceTextGeometry      `json:"sourceGeometries,omitempty"`
	Collisions         []CollisionDiagnostic     `json:"collisions,omitempty"`
	FillWordBoxes      bool                      `json:"fillWordBoxes,omitempty"`
	Blocks             []RenderBlock             `json:"blocks"`
	DeduplicatedBlocks []SkippedRenderBlock      `json:"deduplicatedBlocks,omitempty"`
	SkippedBlocks      []SkippedRenderBlock      `json:"skippedBlocks"`
	Warnings           []RenderWarning           `json:"warnings"`
	BlockFates         []BlockFate               `json:"blockFates,omitempty"`
	PipelineMetrics    PipelineMetrics           `json:"pipelineMetrics"`
	LayoutDiagnostics  LayoutDiagnostics         `json:"layoutDiagnostics"`
	CleanupDiagnostics CleanupDiagnosticsSummary `json:"cleanupDiagnostics"`
}

type LayoutDiagnostics struct {
	TranslatedBlocks               int     `json:"translatedBlocks"`
	RenderableBlocks               int     `json:"renderableBlocks"`
	SkippedBlocks                  int     `json:"skippedBlocks"`
	FullPageFallbackBlocks         int     `json:"fullPageFallbackBlocks"`
	CrossedColumnPlacements        int     `json:"crossedColumnPlacements"`
	CrossedParentPlacements        int     `json:"crossedParentPlacements"`
	MaximumAnchorDisplacementLines float64 `json:"maximumAnchorDisplacementLineHeights"`
	MaximumExpansionRatio          float64 `json:"maximumExpansionRatio"`
	MaximumWidthExpansionRatio     float64 `json:"maximumWidthExpansionRatio"`
	MaximumHeightExpansionRatio    float64 `json:"maximumHeightExpansionRatio"`
	MediumOrHigherVisualRiskBlocks int     `json:"mediumOrHigherVisualRiskBlocks"`
}

// PipelineMetrics makes the translated -> rendered invariant explicit in
// job/debug JSON. Deduplicated blocks are accounted for separately because
// suppressing a duplicate is a successful semantic outcome, not a render
// failure.
type PipelineMetrics struct {
	OCRRegions             int `json:"ocrRegions"`
	SemanticSourceBlocks   int `json:"semanticSourceBlocks"`
	NonSemanticOCRNoise    int `json:"nonSemanticOCRNoise"`
	TranslatedUniqueBlocks int `json:"translatedUniqueBlocks"`
	PassthroughBlocks      int `json:"passthroughBlocks"`
	NormalRenderedBlocks   int `json:"normalRenderedBlocks"`
	CleanupFallbackBlocks  int `json:"cleanupFallbackBlocks"`
	OCRUniqueBlocks        int `json:"ocrUniqueBlocks"`
	TranslatedBlocks       int `json:"translatedBlocks"`
	RenderCandidates       int `json:"renderCandidates"`
	RenderedBlocks         int `json:"renderedBlocks"`
	DeduplicatedBlocks     int `json:"deduplicatedBlocks"`
	FallbackRenderedBlocks int `json:"fallbackRenderedBlocks"`
	HardFailedBlocks       int `json:"hardFailedBlocks"`
}

type BlockFate struct {
	ID          string           `json:"id"`
	Events      []BlockFateEvent `json:"events"`
	DuplicateOf string           `json:"duplicateOf,omitempty"`
}

type BlockFateEvent struct {
	Stage    string `json:"stage"`
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
	Strategy string `json:"strategy,omitempty"`
}

type CleanupDiagnosticsSummary struct {
	SafeBlocks                 int `json:"safeBlocks"`
	UnsafeBlocks               int `json:"unsafeBlocks"`
	ConservativeFallbackBlocks int `json:"conservativeFallbackBlocks,omitempty"`
	FailedRenderedBlocks       int `json:"failedRenderedBlocks,omitempty"`
}

type RenderBlock struct {
	ID                            string             `json:"id"`
	SourceText                    string             `json:"sourceText"`
	TranslatedText                string             `json:"translatedText"`
	SourceBox                     ocr.OCRBox         `json:"sourceBox"`
	SourcePolygon                 ocr.OCRPolygon     `json:"sourcePolygon,omitempty"`
	SourceGeometry                SourceTextGeometry `json:"sourceGeometry,omitempty"`
	CleanupBox                    ocr.OCRBox         `json:"cleanupBox"`
	CleanupRegions                []CleanupRegion    `json:"cleanupRegions,omitempty"`
	TextBox                       ocr.OCRBox         `json:"textBox"`
	SourceWords                   []SourceWordLayout `json:"sourceWords"`
	SourceLines                   []SourceLineLayout `json:"sourceLines"`
	SourceLineHeights             []int              `json:"sourceLineHeights"`
	SourceLineWidths              []int              `json:"sourceLineWidths"`
	SourceLineGaps                []int              `json:"sourceLineGaps"`
	FontEstimate                  FontStyleEstimate  `json:"fontEstimate"`
	Background                    RenderColor        `json:"background"`
	Foreground                    RenderColor        `json:"foreground"`
	CleanupMode                   CleanupMode        `json:"cleanupMode"`
	CleanupSafe                   bool               `json:"cleanupSafe"`
	CleanupSafetyKnown            bool               `json:"cleanupSafetyKnown,omitempty"`
	FontSize                      float64            `json:"fontSize"`
	PreferredFontSize             float64            `json:"preferredFontSize"`
	MinimumFontSize               float64            `json:"minimumFontSize"`
	MaximumFontSize               float64            `json:"maximumFontSize"`
	LineSpacing                   float64            `json:"lineSpacing"`
	Lines                         []string           `json:"lines"`
	LineLayouts                   []RenderLineLayout `json:"lineLayouts"`
	SourceLineCount               int                `json:"sourceLineCount"`
	LineHeight                    int                `json:"lineHeight"`
	LineStep                      int                `json:"lineStep"`
	Ascent                        int                `json:"ascent"`
	TextWidth                     int                `json:"textWidth"`
	TextHeight                    int                `json:"textHeight"`
	TranslatedLineCount           int                `json:"translatedLineCount"`
	Alignment                     string             `json:"alignment"`
	VerticalAlign                 string             `json:"verticalAlign"`
	BoxExpanded                   bool               `json:"boxExpanded"`
	FontReduced                   bool               `json:"fontReduced"`
	EmergencyShrink               bool               `json:"emergencyShrink"`
	LayoutScore                   float64            `json:"layoutScore"`
	FontReductionRatio            float64            `json:"fontReductionRatio"`
	ExpansionRatio                float64            `json:"expansionRatio"`
	AnchorDisplacement            float64            `json:"anchorDisplacement"`
	AnchorDisplacementLineHeights float64            `json:"anchorDisplacementLineHeights"`
	WidthExpansionRatio           float64            `json:"widthExpansionRatio"`
	HeightExpansionRatio          float64            `json:"heightExpansionRatio"`
	IndependentSourceOverlapRatio float64            `json:"independentSourceOverlapRatio"`
	TranslatedRegionOverlapRatio  float64            `json:"translatedRegionOverlapRatio"`
	CrossedColumn                 bool               `json:"crossedColumn"`
	CrossedParentRegion           bool               `json:"crossedParentRegion"`
	VisualRiskSeverity            string             `json:"visualRiskSeverity"`
	LineStepRatio                 float64            `json:"lineStepRatio"`
	FallbackReason                string             `json:"fallbackReason,omitempty"`
	Status                        string             `json:"status"`
	Warning                       string             `json:"warning,omitempty"`
}

type SourceWordLayout struct {
	Text string     `json:"text"`
	Box  ocr.OCRBox `json:"box"`
}

type SourceLineLayout struct {
	ID     string     `json:"id"`
	Text   string     `json:"text"`
	Box    ocr.OCRBox `json:"box"`
	Width  int        `json:"width"`
	Height int        `json:"height"`
}

// FontStyleEstimate keeps typography inference independent from the currently
// bundled face. Style remains regular until a reliable image-weight signal or
// a bundled bold face is available; font size and line step are still inferred
// from source geometry.
type FontStyleEstimate struct {
	Style               string                   `json:"style"`
	FontSize            float64                  `json:"fontSize"`
	InitialFontSize     float64                  `json:"initialFontSize,omitempty"`
	Normalized          bool                     `json:"normalized,omitempty"`
	NormalizationReason string                   `json:"normalizationReason,omitempty"`
	LineStep            float64                  `json:"lineStep"`
	Confidence          float64                  `json:"confidence"`
	IndividualEstimates []IndividualFontEstimate `json:"individualEstimates"`
}

type IndividualFontEstimate struct {
	LineID           string  `json:"lineId"`
	Text             string  `json:"text"`
	SourceInkHeight  int     `json:"sourceInkHeight"`
	SourceLineWidth  int     `json:"sourceLineWidth"`
	MedianWordHeight float64 `json:"medianWordHeight"`
	EstimatedSize    float64 `json:"estimatedSize"`
	NormalizedError  float64 `json:"normalizedError"`
	WidthWeight      float64 `json:"widthWeight"`
}

// CleanupRegion is a spatial prior for source glyph detection. Its box limits
// where cleanup candidates may be searched; it is never itself an erase mask.
type CleanupRegion struct {
	Level      string     `json:"level"`
	Box        ocr.OCRBox `json:"box"`
	TextHeight int        `json:"textHeight"`
}

type RenderLineLayout struct {
	Text      string `json:"text"`
	X         int    `json:"x"`
	BaselineY int    `json:"baselineY"`
	Width     int    `json:"width"`
}

type CleanupMode string

const (
	CleanupSolid  CleanupMode = "solid"
	CleanupNeural CleanupMode = "neural"
)

type SkippedRenderBlock struct {
	ID                     string         `json:"id"`
	Stage                  string         `json:"stage,omitempty"`
	Reason                 string         `json:"reason"`
	SourceText             string         `json:"sourceText,omitempty"`
	OCRConfidence          float64        `json:"ocrConfidence,omitempty"`
	SourcePolygon          ocr.OCRPolygon `json:"sourcePolygon,omitempty"`
	SourceBox              ocr.OCRBox     `json:"sourceBox,omitempty"`
	TranslationText        string         `json:"translationText,omitempty"`
	ConflictingBlockID     string         `json:"conflictingBlockId,omitempty"`
	DuplicateOf            string         `json:"duplicateOf,omitempty"`
	ParagraphOverlapRatio  float64        `json:"paragraphOverlapRatio,omitempty"`
	TextRegionOverlapRatio float64        `json:"textRegionOverlapRatio,omitempty"`
	CollisionClass         string         `json:"collisionClass,omitempty"`
	Decision               string         `json:"decision,omitempty"`
}

// SourceTextGeometry describes OCR ink independently from the paragraph union
// rectangle and from the translated-text layout box. Bounds is only a broad-
// phase index; Regions and Polygons are the protected source-text footprint.
type SourceTextGeometry struct {
	ID          string           `json:"id,omitempty"`
	Bounds      ocr.OCRBox       `json:"bounds"`
	Regions     []ocr.OCRBox     `json:"regions"`
	LineRegions []ocr.OCRBox     `json:"lineRegions,omitempty"`
	Polygons    []ocr.OCRPolygon `json:"polygons,omitempty"`
	Level       string           `json:"level"`
}

type CollisionDiagnostic struct {
	BlockID                string     `json:"blockId"`
	ConflictingBlockID     string     `json:"conflictingBlockId"`
	ParagraphIntersection  ocr.OCRBox `json:"paragraphIntersection,omitempty"`
	ParagraphOverlapRatio  float64    `json:"paragraphOverlapRatio"`
	TextRegionOverlapRatio float64    `json:"textRegionOverlapRatio"`
	CollisionClass         string     `json:"collisionClass"`
	Decision               string     `json:"decision"`
}

type RenderWarning struct {
	Code    string `json:"code"`
	BlockID string `json:"blockId,omitempty"`
}

type BackgroundSample struct {
	Color    color.NRGBA
	Variance float64
	Count    int
	Fallback bool
}

type CleanupPadding struct {
	Horizontal int
	Vertical   int
}

type TextFitRequest struct {
	Text              string
	Width             int
	Height            int
	MinFontSize       float64
	MaxFontSize       float64
	PreferredFontSize float64
	LineSpacing       float64
	SourceLineStep    float64
	HorizontalPad     int
	VerticalPad       int
}

type TextFitResult struct {
	FontSize           float64
	PreferredFontSize  float64
	MinimumFontSize    float64
	Lines              []string
	LineWidths         []int
	TextWidth          int
	TextHeight         int
	LineHeight         int
	LineStep           int
	Ascent             int
	Descent            int
	Fits               bool
	Overflow           bool
	BoxExpanded        bool
	FontReduced        bool
	EmergencyShrink    bool
	Score              float64
	FontReductionRatio float64
	ExpansionRatio     float64
	AnchorDisplacement float64
	LineStepRatio      float64
	FallbackReason     string
}
