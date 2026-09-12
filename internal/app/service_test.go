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

type explicitSourceTranslator struct{ fakeTranslator }

func (explicitSourceTranslator) RequiresExplicitSourceLanguage() bool { return true }

type fakeVisionTranslator struct {
	fakeTranslator
	imageResult   string
	imageErr      error
	imageRequests *[]translation.ImageTranslateRequest
}

func (f fakeVisionTranslator) TranslateImage(_ context.Context, request translation.ImageTranslateRequest) (translation.ImageTranslateResult, error) {
	if f.imageRequests != nil {
		*f.imageRequests = append(*f.imageRequests, request)
	}
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
	service := NewService(context.Background(), fakeTranslator{result: "Hello", requests: &requests}, testIdentifier(language.Detection{Language: "ru", Reliable: true}), "ru", "en", log.New(io.Discard, "", 0))
	result, err := service.Translate(context.Background(), translation.TranslateRequest{Text: "Привет", Source: "auto", Target: "en"})
	if err != nil || result.DetectedLanguage != "ru" || result.TargetLanguage != "en" || len(requests) != 1 || requests[0].Source != "ru" || requests[0].Target != "en" {
		t.Fatalf("Translate() = %+v, %v", result, err)
	}
}

func TestServiceTranslateKeepsAutoWhenUnreliableDetectionIsTooShort(t *testing.T) {
	t.Parallel()
	var requests []translation.TranslateRequest
	service := NewService(context.Background(), fakeTranslator{result: "Hello", detected: "de", requests: &requests}, testIdentifier(language.Detection{Language: "de", Reliable: false}), "ru", "en", log.New(io.Discard, "", 0))
	result, err := service.Translate(context.Background(), translation.TranslateRequest{Text: "test", Source: "auto", Target: "en"})
	if err != nil || result.DetectedLanguage != "" || len(requests) != 1 || requests[0].Source != "auto" || requests[0].Target != "en" {
		t.Fatalf("Translate() = %+v, %v; requests = %+v", result, err, requests)
	}
}

func TestServiceTranslateUsesUsableUnreliableAutoCandidate(t *testing.T) {
	t.Parallel()
	const source = `Метод LanguageIdentifier.ResolveSource должен сохранять путь /api/v1/translate и идентификатор HTTPRequest без изменений.`
	var requests []translation.TranslateRequest
	service := NewService(context.Background(), fakeTranslator{result: "Hello", requests: &requests}, testIdentifier(language.Detection{Language: "ru", Confidence: 0.55, Reliable: false}), "ru", "en", log.New(io.Discard, "", 0))
	result, err := service.Translate(context.Background(), translation.TranslateRequest{Text: source, Source: "auto", Target: "en"})
	if err != nil || result.DetectedLanguage != "" || result.TargetLanguage != "en" || len(requests) != 1 || requests[0].Source != "ru" || requests[0].Target != "en" {
		t.Fatalf("Translate() = %+v, %v; requests = %+v", result, err, requests)
	}
}

func TestServiceTranslateKeepsAutoWhenDetectionHasNoCandidate(t *testing.T) {
	t.Parallel()
	var requests []translation.TranslateRequest
	service := NewService(context.Background(), fakeTranslator{result: "translated", requests: &requests}, testIdentifier(language.Detection{}), "ru", "en", log.New(io.Discard, "", 0))
	result, err := service.Translate(context.Background(), translation.TranslateRequest{Text: "This input is long enough, but local detection found no candidate.", Source: "auto", Target: "ru"})
	if err != nil || result.DetectedLanguage != "" || result.TargetLanguage != "ru" || len(requests) != 1 || requests[0].Source != "auto" || requests[0].Target != "ru" {
		t.Fatalf("Translate() = %+v, %v; requests = %+v", result, err, requests)
	}
}

func TestServiceTranslateUsesExplicitSourceFallbackBeforeTargetSelection(t *testing.T) {
	t.Parallel()
	var requests []translation.TranslateRequest
	service := NewService(context.Background(), explicitSourceTranslator{fakeTranslator{result: "Hello", requests: &requests}}, testIdentifier(language.Detection{Language: "de", Reliable: false}), "ru", "en", log.New(io.Discard, "", 0))
	result, err := service.Translate(context.Background(), translation.TranslateRequest{Text: "коротко", Source: "auto", Target: "ru"})
	if err != nil || result.DetectedLanguage != "" || result.TargetLanguage != "en" {
		t.Fatalf("Translate() = %+v, %v", result, err)
	}
	if len(requests) != 1 || requests[0].Source != "ru" || requests[0].Target != "en" {
		t.Fatalf("translator requests = %+v", requests)
	}
}

func TestServiceTranslateDoesNotOverrideSuccessfulDetectionWithFallback(t *testing.T) {
	t.Parallel()
	var requests []translation.TranslateRequest
	service := NewService(context.Background(), explicitSourceTranslator{fakeTranslator{result: "Hello", requests: &requests}}, testIdentifier(language.Detection{Language: "de", Reliable: true}), "ru", "en", log.New(io.Discard, "", 0))
	result, err := service.Translate(context.Background(), translation.TranslateRequest{Text: "Привіт", Source: "auto", Target: "en"})
	if err != nil || result.DetectedLanguage != "de" || result.TargetLanguage != "en" {
		t.Fatalf("Translate() = %+v, %v", result, err)
	}
	if len(requests) != 1 || requests[0].Source != "de" || requests[0].Target != "en" {
		t.Fatalf("translator requests = %+v", requests)
	}
}

func TestServiceTranslateExplicitSourceBypassesIdentification(t *testing.T) {
	t.Parallel()
	calls := 0
	var requests []translation.TranslateRequest
	identifier := language.NewLanguageIdentifier(testClassifier{detection: language.Detection{Language: "de", Reliable: true}, calls: &calls})
	service := NewService(context.Background(), explicitSourceTranslator{fakeTranslator{result: "Bonjour", requests: &requests}}, identifier, "ru", "en", log.New(io.Discard, "", 0))
	result, err := service.Translate(context.Background(), translation.TranslateRequest{Text: "Bonjour", Source: "fr", Target: "en"})
	if err != nil || calls != 0 || result.DetectedLanguage != "" || result.TargetLanguage != "en" || len(requests) != 1 || requests[0].Source != "fr" || requests[0].Target != "en" {
		t.Fatalf("Translate() = %+v, %v; calls = %d, requests = %+v", result, err, calls, requests)
	}
}

func TestServiceTranslateChangesCollidingDetectedTargetBeforeTranslation(t *testing.T) {
	t.Parallel()
	var requests []translation.TranslateRequest
	service := NewService(context.Background(), fakeTranslator{result: "Hello", requests: &requests}, testIdentifier(language.Detection{Language: "ru", Reliable: true}), "ru", "en", log.New(io.Discard, "", 0))
	result, err := service.Translate(context.Background(), translation.TranslateRequest{Text: "Привет", Source: "auto", Target: "ru"})
	if err != nil || result.DetectedLanguage != "ru" || result.TargetLanguage != "en" {
		t.Fatalf("Translate() = %+v, %v", result, err)
	}
	if len(requests) != 1 || requests[0].Source != "ru" || requests[0].Target != "en" {
		t.Fatalf("translator requests = %+v", requests)
	}
}

func TestServiceTranslateChangesCollidingExplicitTargetBeforeTranslation(t *testing.T) {
	t.Parallel()
	var requests []translation.TranslateRequest
	service := NewService(context.Background(), fakeTranslator{result: "Hello", requests: &requests}, testIdentifier(language.Detection{}), "ru", "en", log.New(io.Discard, "", 0))
	result, err := service.Translate(context.Background(), translation.TranslateRequest{Text: "Hello", Source: "en", Target: "en"})
	if err != nil || result.TargetLanguage != "ru" {
		t.Fatalf("Translate() = %+v, %v", result, err)
	}
	if len(requests) != 1 || requests[0].Source != "en" || requests[0].Target != "ru" {
		t.Fatalf("translator requests = %+v", requests)
	}
}

func TestServiceReturnsTranslatorError(t *testing.T) {
	t.Parallel()
	want := errors.New("offline")
	service := NewService(context.Background(), fakeTranslator{err: want}, testIdentifier(language.Detection{}), "ru", "en", log.New(io.Discard, "", 0))
	_, err := service.Translate(context.Background(), translation.TranslateRequest{Text: "x", Source: "en", Target: "ru"})
	if !errors.Is(err, want) {
		t.Fatalf("Translate() error = %v", err)
	}
}

func TestServiceRejectsWorkAfterClose(t *testing.T) {
	t.Parallel()
	service := NewService(context.Background(), fakeTranslator{result: "Hello"}, testIdentifier(language.Detection{}), "ru", "en", log.New(io.Discard, "", 0))
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
	var requests []translation.ImageTranslateRequest
	service := NewService(context.Background(), fakeVisionTranslator{imageRequests: &requests}, testIdentifier(language.Detection{}), "ru", "en", log.New(io.Discard, "", 0))
	id, err := service.StartImageTranslate(validImageServiceRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	service.Wait()
	status, err := service.ImageJob(id)
	if err != nil || status.State != "done" || status.Result == nil || status.Result.Text != "" || status.Result.TargetLanguage != "ru" {
		t.Fatalf("ImageJob() = %+v, %v", status, err)
	}
	if len(requests) != 1 || requests[0].DefaultLanguageFirst != "ru" || requests[0].DefaultLanguageSecond != "en" {
		t.Fatalf("image translator requests = %+v", requests)
	}
}

func TestServiceTranslateImagePassesDefaultPair(t *testing.T) {
	t.Parallel()
	var requests []translation.ImageTranslateRequest
	service := NewService(context.Background(), fakeVisionTranslator{imageResult: "translated", imageRequests: &requests}, testIdentifier(language.Detection{}), "ru", "en", log.New(io.Discard, "", 0))
	result, err := service.TranslateImage(context.Background(), validImageServiceRequest(t))
	if err != nil || result.Text != "translated" || result.TargetLanguage != "ru" {
		t.Fatalf("TranslateImage() = %+v, %v", result, err)
	}
	if len(requests) != 1 || requests[0].DefaultLanguageFirst != "ru" || requests[0].DefaultLanguageSecond != "en" {
		t.Fatalf("image translator requests = %+v", requests)
	}
}

func TestServiceRejectsImageWorkAfterClose(t *testing.T) {
	t.Parallel()
	service := NewService(context.Background(), fakeVisionTranslator{}, testIdentifier(language.Detection{}), "ru", "en", log.New(io.Discard, "", 0))
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
