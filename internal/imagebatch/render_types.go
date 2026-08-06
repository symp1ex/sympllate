package imagebatch

import (
	"image/color"

	"github.com/sympllate/translator/internal/ocr"
)

type RenderConfig struct {
	CleanupPaddingX       int
	CleanupPaddingY       int
	BackgroundSampleWidth int
	MinimumFontSize       float64
	MaximumFontSize       float64
	LineSpacing           float64
	HorizontalTextPadding int
	VerticalTextPadding   int
	JPEGQuality           int
	MaximumSamples        int
	MinimumSamples        int
	NonUniformThreshold   float64
}

func DefaultRenderConfig() RenderConfig {
	return RenderConfig{
		CleanupPaddingX: 4, CleanupPaddingY: 3, BackgroundSampleWidth: 4,
		MinimumFontSize: 10, MaximumFontSize: 48, LineSpacing: 1.15,
		HorizontalTextPadding: 2, VerticalTextPadding: 2, JPEGQuality: 92,
		MaximumSamples: 4096, MinimumSamples: 8, NonUniformThreshold: 625,
	}
}

func (c RenderConfig) validate() error {
	if c.CleanupPaddingX < 0 || c.CleanupPaddingY < 0 || c.HorizontalTextPadding < 0 || c.VerticalTextPadding < 0 {
		return errInvalidRenderConfig("padding cannot be negative")
	}
	if c.BackgroundSampleWidth <= 0 || c.MaximumSamples <= 0 || c.MinimumSamples <= 0 {
		return errInvalidRenderConfig("sampling limits must be positive")
	}
	if c.MinimumFontSize <= 0 || c.MaximumFontSize < c.MinimumFontSize || c.LineSpacing < 1 || c.JPEGQuality < 1 || c.JPEGQuality > 100 {
		return errInvalidRenderConfig("font or encoding limits are invalid")
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
	SchemaVersion int                  `json:"schemaVersion"`
	SourceFile    string               `json:"sourceFile"`
	ImageWidth    int                  `json:"imageWidth"`
	ImageHeight   int                  `json:"imageHeight"`
	Transform     CoordinateTransform  `json:"coordinateTransform"`
	Blocks        []RenderBlock        `json:"blocks"`
	SkippedBlocks []SkippedRenderBlock `json:"skippedBlocks"`
	Warnings      []RenderWarning      `json:"warnings"`
}

type RenderBlock struct {
	ID             string      `json:"id"`
	SourceText     string      `json:"sourceText"`
	TranslatedText string      `json:"translatedText"`
	SourceBox      ocr.OCRBox  `json:"sourceBox"`
	CleanupBox     ocr.OCRBox  `json:"cleanupBox"`
	TextBox        ocr.OCRBox  `json:"textBox"`
	Background     RenderColor `json:"background"`
	Foreground     RenderColor `json:"foreground"`
	FontSize       float64     `json:"fontSize"`
	LineSpacing    float64     `json:"lineSpacing"`
	Lines          []string    `json:"lines"`
	Alignment      string      `json:"alignment"`
	VerticalAlign  string      `json:"verticalAlign"`
	Status         string      `json:"status"`
	Warning        string      `json:"warning,omitempty"`
}

type SkippedRenderBlock struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
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
	Text          string
	Width         int
	Height        int
	MinFontSize   float64
	MaxFontSize   float64
	LineSpacing   float64
	HorizontalPad int
	VerticalPad   int
}

type TextFitResult struct {
	FontSize   float64
	Lines      []string
	TextWidth  int
	TextHeight int
	Fits       bool
	Overflow   bool
	Truncated  bool
}
