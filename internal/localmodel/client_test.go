package localmodel

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sympllate/translator/internal/language"
	"github.com/sympllate/translator/internal/translation"
)

func TestClientTranslateRequest(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		var body translateGemmaRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != ModelAlias || body.Stream || body.MaxTokens != 321 || body.Temperature != 0.25 || len(body.Messages) != 1 {
			t.Errorf("unexpected body: %+v", body)
		}
		if body.Messages[0].Role != "user" || len(body.Messages[0].Content) != 1 || body.Messages[0].Content[0] != (translateGemmaContent{Type: "text", SourceLangCode: "ru", TargetLangCode: "en", Text: "Привет"}) {
			t.Errorf("unexpected native content: %+v", body.Messages)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Translation: Hello"}}]}`))
	}))
	defer server.Close()
	client := NewClient(server.URL, "test-key", 321, 0.25, 100, time.Second)
	result, err := client.Translate(context.Background(), translation.TranslateRequest{Text: "Привет", Source: "ru", Target: "en"})
	if err != nil || result.Text != "Hello" {
		t.Fatalf("Translate() = %+v, %v", result, err)
	}
}

func TestParseChatResponse(t *testing.T) {
	t.Parallel()
	result, err := ParseChatResponse(http.StatusOK, []byte(`{"choices":[{"message":{"content":"Привет"}}]}`))
	if err != nil || result.Text != "Привет" {
		t.Fatalf("ParseChatResponse() = %+v, %v", result, err)
	}
	for _, test := range []struct {
		status int
		body   string
	}{
		{http.StatusInternalServerError, `{"error":{"message":"load failed"}}`},
		{http.StatusOK, `{`},
		{http.StatusOK, `{"choices":[]}`},
		{http.StatusOK, `{"choices":[{"message":{"content":" "}}]}`},
	} {
		if _, err := ParseChatResponse(test.status, []byte(test.body)); err == nil {
			t.Fatalf("ParseChatResponse(%d, %q) expected error", test.status, test.body)
		}
	}
}

func TestClientTranslateCancellation(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(100 * time.Millisecond):
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, "test-key", 10, 0, 100, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.Translate(ctx, translation.TranslateRequest{Text: "hello", Source: "en", Target: "ru"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Translate() error = %v", err)
	}
}

func TestClientTranslateTimeout(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(100 * time.Millisecond):
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, "test-key", 10, 0, 100, 10*time.Millisecond)
	_, err := client.Translate(context.Background(), translation.TranslateRequest{Text: "hello", Source: "en", Target: "ru"})
	if err == nil || !strings.Contains(err.Error(), "did not respond in time") {
		t.Fatalf("Translate() error = %v", err)
	}
}

func TestClientTranslateNormalizesVisibleParagraphs(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"one\\n\\ntwo"}}]}`))
	}))
	defer server.Close()
	client := NewClient(server.URL, "test-key", 100, 0, 100, time.Second)
	result, err := client.Translate(context.Background(), translation.TranslateRequest{Text: "first\n\nsecond", Source: "en", Target: "ru"})
	if err != nil || result.Text != "one\n\ntwo" {
		t.Fatalf("Translate() = %q, %v; want real paragraph breaks", result.Text, err)
	}
}

func TestClientReportsUnsupportedImageInput(t *testing.T) {
	t.Parallel()
	client := NewClient("http://127.0.0.1:1", "test-key", 10, 0, 100, time.Second)
	capability := client.ImageCapability()
	if capability.Supported || !strings.Contains(capability.Reason, "PaddleOCR") {
		t.Fatalf("ImageCapability() = %+v", capability)
	}
	_, err := client.TranslateImage(context.Background(), localImageRequest(t))
	if err == nil || !strings.Contains(err.Error(), "PaddleOCR") {
		t.Fatalf("TranslateImage() error = %v", err)
	}
}

func TestClientTranslateImageSendsOnlyOCRTextToLocalServer(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request translateGemmaRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		content := request.Messages[0].Content[0].Text
		if content != "recognized source" {
			t.Errorf("unexpected native OCR text: %s", content)
		}
		if strings.Contains(content, "iVBOR") || strings.Contains(content, "data:image") {
			t.Errorf("image data leaked to local server request: %s", content)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"translated"}}]}`))
	}))
	defer server.Close()
	extractor := fakeImageTextExtractor{text: "recognized source"}
	client := NewClientWithImageTextExtractor(server.URL, "test-key", 100, 0, 1000, time.Second, extractor, nil)
	result, err := client.TranslateImage(context.Background(), localImageRequest(t))
	if err != nil || result.Text != "translated" {
		t.Fatalf("TranslateImage() = %+v, %v", result, err)
	}
}

func TestClientTranslateImageNormalizesImageResultOnly(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"one\\r\\ntwo\\n\\nthree C:\\\\react folder\\nsys"}}]}`))
	}))
	defer server.Close()
	client := NewClientWithImageTextExtractor(server.URL, "test-key", 100, 0, 1000, time.Second, fakeImageTextExtractor{text: "source"}, nil)
	result, err := client.TranslateImage(context.Background(), localImageRequest(t))
	if err != nil || result.Text != "one\ntwo\n\nthree C:\\\\react folder\\nsys" {
		t.Fatalf("TranslateImage() = %q, %v", result.Text, err)
	}
}

func TestClientTranslateImageAllowsEmptyOCRResult(t *testing.T) {
	t.Parallel()
	client := NewClientWithImageTextExtractor("http://127.0.0.1:1", "test-key", 100, 0, 1000, time.Second, fakeImageTextExtractor{}, nil)
	result, err := client.TranslateImage(context.Background(), localImageRequest(t))
	if err != nil || result.Text != "" {
		t.Fatalf("TranslateImage() = %+v, %v", result, err)
	}
}

type fakeImageTextExtractor struct {
	text string
	err  error
}

type localClassifier struct {
	detection language.Detection
	calls     *int
}

func (f localClassifier) Detect(string) language.Detection {
	if f.calls != nil {
		(*f.calls)++
	}
	return f.detection
}

type trackingImageTextExtractor struct {
	text    string
	sources []string
}

func (*trackingImageTextExtractor) Capability() translation.ImageCapability {
	return translation.ImageCapability{Supported: true}
}

func (f *trackingImageTextExtractor) Recognize(_ context.Context, _ translation.ValidatedImage, source string) (string, error) {
	f.sources = append(f.sources, source)
	return f.text, nil
}

func TestClientTranslateImageResolvesReliableOCRSource(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request translateGemmaRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		content := request.Messages[0].Content[0]
		if content.SourceLangCode != "de" || content.TargetLangCode != "ru" || content.Text != "erkannter Text" {
			t.Errorf("translation content = %+v", content)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"translated"}}]}`))
	}))
	defer server.Close()
	extractor := &trackingImageTextExtractor{text: "erkannter Text"}
	identifier := language.NewLanguageIdentifier(localClassifier{detection: language.Detection{Language: "de", Reliable: true}})
	client := NewClientWithImageTextExtractor(server.URL, "test-key", 100, 0, 1000, time.Second, extractor, identifier)

	request := localImageRequest(t)
	request.Source = "auto"
	result, err := client.TranslateImage(context.Background(), request)
	if err != nil || result.Text != "translated" || result.DetectedLanguage != "de" {
		t.Fatalf("TranslateImage() = %+v, %v", result, err)
	}
	if len(extractor.sources) != 1 || extractor.sources[0] != "auto" {
		t.Fatalf("OCR sources = %+v", extractor.sources)
	}
}

func TestClientTranslateImageKeepsAutoForUnreliableOCRSource(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request chatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if len(request.Messages) != 1 || !strings.Contains(request.Messages[0].Content, "from auto to ru") {
			t.Errorf("generic translation request = %+v", request)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"translated"}}]}`))
	}))
	defer server.Close()
	extractor := &trackingImageTextExtractor{text: "ambiguous text"}
	identifier := language.NewLanguageIdentifier(localClassifier{detection: language.Detection{Language: "de", Reliable: false}})
	client := NewClientWithImageTextExtractor(server.URL, "test-key", 100, 0, 1000, time.Second, extractor, identifier)
	client.profile = "generic"

	request := localImageRequest(t)
	request.Source = "auto"
	result, err := client.TranslateImage(context.Background(), request)
	if err != nil || result.Text != "translated" || result.DetectedLanguage != "" {
		t.Fatalf("TranslateImage() = %+v, %v", result, err)
	}
	if len(extractor.sources) != 1 || extractor.sources[0] != "auto" {
		t.Fatalf("OCR sources = %+v", extractor.sources)
	}
}

func TestClientTranslateImageExplicitSourceBypassesIdentification(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request translateGemmaRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if source := request.Messages[0].Content[0].SourceLangCode; source != "en" {
			t.Errorf("translation source = %q, want en", source)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"translated"}}]}`))
	}))
	defer server.Close()
	calls := 0
	extractor := &trackingImageTextExtractor{text: "recognized text"}
	identifier := language.NewLanguageIdentifier(localClassifier{detection: language.Detection{Language: "de", Reliable: true}, calls: &calls})
	client := NewClientWithImageTextExtractor(server.URL, "test-key", 100, 0, 1000, time.Second, extractor, identifier)

	result, err := client.TranslateImage(context.Background(), localImageRequest(t))
	if err != nil || result.Text != "translated" || result.DetectedLanguage != "" || calls != 0 {
		t.Fatalf("TranslateImage() = %+v, %v; classifier calls = %d", result, err, calls)
	}
	if len(extractor.sources) != 1 || extractor.sources[0] != "en" {
		t.Fatalf("OCR sources = %+v", extractor.sources)
	}
}

func (fakeImageTextExtractor) Capability() translation.ImageCapability {
	return translation.ImageCapability{Supported: true}
}

func (f fakeImageTextExtractor) Recognize(context.Context, translation.ValidatedImage, string) (string, error) {
	return f.text, f.err
}

func localImageRequest(t *testing.T) translation.ImageTranslateRequest {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return translation.ImageTranslateRequest{
		DataBase64: base64.StdEncoding.EncodeToString(buffer.Bytes()), MediaType: "image/png", Source: "en", Target: "ru",
	}
}
