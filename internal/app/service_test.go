package app

import (
	"context"
	"errors"
	"io"
	"log"
	"testing"

	"github.com/sympllate/translator/internal/language"
	"github.com/sympllate/translator/internal/translation"
)

type fakeTranslator struct {
	result string
	err    error
}

func (f fakeTranslator) Translate(context.Context, translation.TranslateRequest) (translation.TranslateResult, error) {
	return translation.TranslateResult{Text: f.result}, f.err
}

func TestServiceTranslateDetectsAutoLanguage(t *testing.T) {
	t.Parallel()
	service := NewService(context.Background(), fakeTranslator{result: "Hello"}, language.SimpleDetector{}, log.New(io.Discard, "", 0))
	result, err := service.Translate(context.Background(), translation.TranslateRequest{Text: "Привет", Source: "auto", Target: "en"})
	if err != nil || result.DetectedLanguage != "ru" {
		t.Fatalf("Translate() = %+v, %v", result, err)
	}
}

func TestServiceReturnsTranslatorError(t *testing.T) {
	t.Parallel()
	want := errors.New("offline")
	service := NewService(context.Background(), fakeTranslator{err: want}, language.SimpleDetector{}, log.New(io.Discard, "", 0))
	_, err := service.Translate(context.Background(), translation.TranslateRequest{Text: "x", Source: "en", Target: "ru"})
	if !errors.Is(err, want) {
		t.Fatalf("Translate() error = %v", err)
	}
}

func TestServiceRejectsWorkAfterClose(t *testing.T) {
	t.Parallel()
	service := NewService(context.Background(), fakeTranslator{result: "Hello"}, language.SimpleDetector{}, log.New(io.Discard, "", 0))
	service.Close()
	if _, err := service.StartTranslate(translation.TranslateRequest{Text: "x", Source: "en", Target: "ru"}); err == nil {
		t.Fatal("StartTranslate() expected shutdown error")
	}
	if _, err := service.Translate(context.Background(), translation.TranslateRequest{Text: "x", Source: "en", Target: "ru"}); err == nil {
		t.Fatal("Translate() expected shutdown error")
	}
}
