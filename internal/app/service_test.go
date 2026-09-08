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
	result   string
	err      error
	detected string
	requests *[]translation.TranslateRequest
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

func (f fakeTranslator) Translate(_ context.Context, request translation.TranslateRequest) (translation.TranslateResult, error) {
	if f.requests != nil {
		*f.requests = append(*f.requests, request)
	}
	return translation.TranslateResult{Text: f.result, DetectedLanguage: f.detected}, f.err
}

type testClassifier struct {
	detection language.Detection
	calls     *int
	panics    bool
}

func (f testClassifier) Detect(string) language.Detection {
	if f.calls != nil {
		(*f.calls)++
	}
	if f.panics {
		panic("classifier failure")
	}
	return f.detection
}

func testIdentifier(detection language.Detection) *language.LanguageIdentifier {
	return language.NewLanguageIdentifier(testClassifier{detection: detection})
}

func TestServiceTranslateDetectsAutoLanguage(t *testing.T) {
	t.Parallel()
	var requests []translation.TranslateRequest
	service := NewService(context.Background(), fakeTranslator{result: "Hello", requests: &requests}, testIdentifier(language.Detection{Language: "ru", Reliable: true}), log.New(io.Discard, "", 0))
	result, err := service.Translate(context.Background(), translation.TranslateRequest{Text: "Привет", Source: "auto", Target: "en"})
	if err != nil || result.DetectedLanguage != "ru" || len(requests) != 1 || requests[0].Source != "ru" || requests[0].Target != "en" {
		t.Fatalf("Translate() = %+v, %v", result, err)
	}
}

func TestServiceTranslateKeepsAutoWhenDetectionIsUnreliable(t *testing.T) {
	t.Parallel()
	var requests []translation.TranslateRequest
	service := NewService(context.Background(), fakeTranslator{result: "Hello", detected: "de", requests: &requests}, testIdentifier(language.Detection{Language: "de", Reliable: false}), log.New(io.Discard, "", 0))
	result, err := service.Translate(context.Background(), translation.TranslateRequest{Text: "test", Source: "auto", Target: "en"})
	if err != nil || result.DetectedLanguage != "" || len(requests) != 1 || requests[0].Source != "auto" || requests[0].Target != "en" {
		t.Fatalf("Translate() = %+v, %v; requests = %+v", result, err, requests)
	}
}

func TestServiceTranslateExplicitSourceBypassesIdentification(t *testing.T) {
	t.Parallel()
	calls := 0
	var requests []translation.TranslateRequest
	identifier := language.NewLanguageIdentifier(testClassifier{detection: language.Detection{Language: "de", Reliable: true}, calls: &calls})
	service := NewService(context.Background(), fakeTranslator{result: "Bonjour", requests: &requests}, identifier, log.New(io.Discard, "", 0))
	result, err := service.Translate(context.Background(), translation.TranslateRequest{Text: "Bonjour", Source: "fr", Target: "en"})
	if err != nil || calls != 0 || result.DetectedLanguage != "" || len(requests) != 1 || requests[0].Source != "fr" || requests[0].Target != "en" {
		t.Fatalf("Translate() = %+v, %v; calls = %d, requests = %+v", result, err, calls, requests)
	}
}

func TestServiceReturnsTranslatorError(t *testing.T) {
	t.Parallel()
	want := errors.New("offline")
	service := NewService(context.Background(), fakeTranslator{err: want}, testIdentifier(language.Detection{}), log.New(io.Discard, "", 0))
	_, err := service.Translate(context.Background(), translation.TranslateRequest{Text: "x", Source: "en", Target: "ru"})
	if !errors.Is(err, want) {
		t.Fatalf("Translate() error = %v", err)
	}
}

func TestServiceRejectsWorkAfterClose(t *testing.T) {
	t.Parallel()
	service := NewService(context.Background(), fakeTranslator{result: "Hello"}, testIdentifier(language.Detection{}), log.New(io.Discard, "", 0))
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
	service := NewService(context.Background(), fakeVisionTranslator{}, testIdentifier(language.Detection{}), log.New(io.Discard, "", 0))
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
	service := NewService(context.Background(), fakeVisionTranslator{}, testIdentifier(language.Detection{}), log.New(io.Discard, "", 0))
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
