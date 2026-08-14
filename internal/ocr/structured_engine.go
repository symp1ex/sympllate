package ocr

import (
	"context"

	"github.com/sympllate/translator/internal/translation"
)

// StructuredEngine is the OCR contract consumed by the image pipelines.
type StructuredEngine interface {
	Capability() translation.ImageCapability
	ValidateSource(string) error
	Recognize(context.Context, translation.ValidatedImage, string) (string, error)
	RecognizeStructured(context.Context, translation.ValidatedImage, string) (OCRPage, error)
	Close() error
}
