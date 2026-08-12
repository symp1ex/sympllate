package ocr

import (
	"context"
	"fmt"
	"time"

	"github.com/sympllate/translator/internal/config"
	"github.com/sympllate/translator/internal/logger"
	"github.com/sympllate/translator/internal/translation"
)

// StructuredEngine is the common contract consumed by the image pipeline.
type StructuredEngine interface {
	Capability() translation.ImageCapability
	ValidateSource(string) error
	Recognize(context.Context, translation.ValidatedImage, string) (string, error)
	RecognizeStructured(context.Context, translation.ValidatedImage, string) (OCRPage, error)
	Close() error
}

type tesseractBackend struct{ *Engine }

func (t *tesseractBackend) Close() error { return nil }

func NewBackend(executableDir, backend string, timeout time.Duration, log logger.PrintLogger) (StructuredEngine, error) {
	switch backend {
	case config.OCRBackendTesseract:
		return &tesseractBackend{Engine: New(executableDir, timeout)}, nil
	case config.OCRBackendPaddleOCR:
		return NewPaddleEngine(executableDir, timeout, log)
	default:
		return nil, fmt.Errorf("unknown OCR backend %q", backend)
	}
}
