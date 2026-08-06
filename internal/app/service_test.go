package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
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

type fakeVisionTranslator struct {
	fakeTranslator
	imageResult string
	imageErr    error
}

func (f fakeVisionTranslator) TranslateImage(context.Context, translation.ImageTranslateRequest) (translation.ImageTranslateResult, error) {
	return translation.ImageTranslateResult{Text: f.imageResult}, f.imageErr
}

func (fakeVisionTranslator) ImageCapability() translation.ImageCapability {
	return translation.ImageCapability{Supported: true}
}

func (fakeVisionTranslator) ProviderName() string { return "fake" }

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

func TestServiceRunsImageJobAndAllowsEmptyResult(t *testing.T) {
	t.Parallel()
	service := NewService(context.Background(), fakeVisionTranslator{}, language.SimpleDetector{}, log.New(io.Discard, "", 0))
	id, err := service.StartImageTranslate(validImageServiceRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	service.Wait()
	status, err := service.ImageJob(id)
	if err != nil || status.State != "done" || status.Result == nil || status.Result.Text != "" {
		t.Fatalf("ImageJob() = %+v, %v", status, err)
	}
}

func TestServiceRejectsImageWorkAfterClose(t *testing.T) {
	t.Parallel()
	service := NewService(context.Background(), fakeVisionTranslator{}, language.SimpleDetector{}, log.New(io.Discard, "", 0))
	service.Close()
	if _, err := service.StartImageTranslate(validImageServiceRequest(t)); err == nil {
		t.Fatal("StartImageTranslate() expected shutdown error")
	}
	if _, err := service.TranslateImage(context.Background(), validImageServiceRequest(t)); err == nil {
		t.Fatal("TranslateImage() expected shutdown error")
	}
}

func validImageServiceRequest(t *testing.T) translation.ImageTranslateRequest {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return translation.ImageTranslateRequest{
		DataBase64: base64.StdEncoding.EncodeToString(buffer.Bytes()), MediaType: "image/png", Source: "en", Target: "ru",
	}
}
