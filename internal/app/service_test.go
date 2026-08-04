package app

import (
	"context"
	"errors"
	"io"
	"log"
	"testing"

	"github.com/sympllate/translator/internal/language"
	"github.com/sympllate/translator/internal/ollama"
)

type fakeTranslator struct {
	result string
	err    error
}

func (f fakeTranslator) Translate(context.Context, ollama.TranslateRequest) (ollama.TranslateResult, error) {
	return ollama.TranslateResult{Text: f.result}, f.err
}

func TestServiceTranslateDetectsAutoLanguage(t *testing.T) {
	t.Parallel()
	service := NewService(context.Background(), fakeTranslator{result: "Hello"}, language.SimpleDetector{}, log.New(io.Discard, "", 0))
	result, err := service.Translate(context.Background(), ollama.TranslateRequest{Text: "Привет", Source: "auto", Target: "en"})
	if err != nil || result.DetectedLanguage != "ru" {
		t.Fatalf("Translate() = %+v, %v", result, err)
	}
}

func TestServiceReturnsTranslatorError(t *testing.T) {
	t.Parallel()
	want := errors.New("offline")
	service := NewService(context.Background(), fakeTranslator{err: want}, language.SimpleDetector{}, log.New(io.Discard, "", 0))
	_, err := service.Translate(context.Background(), ollama.TranslateRequest{Text: "x", Source: "en", Target: "ru"})
	if !errors.Is(err, want) {
		t.Fatalf("Translate() error = %v", err)
	}
}
