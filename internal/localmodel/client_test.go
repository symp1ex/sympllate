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
		var body chatRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != ModelAlias || body.Stream || body.MaxTokens != 321 || body.Temperature != 0.25 || len(body.Messages) != 1 {
			t.Errorf("unexpected body: %+v", body)
		}
		if !strings.Contains(body.Messages[0].Content, `"Привет"`) {
			t.Errorf("prompt does not safely encode text: %s", body.Messages[0].Content)
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
		var request chatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		content := request.Messages[0].Content
		if !strings.Contains(content, `"recognized source"`) {
			t.Errorf("OCR text missing from prompt: %s", content)
		}
		if strings.Contains(content, "iVBOR") || strings.Contains(content, "data:image") {
			t.Errorf("image data leaked to local server request: %s", content)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"translated"}}]}`))
	}))
	defer server.Close()
	extractor := fakeImageTextExtractor{text: "recognized source"}
	client := NewClientWithImageTextExtractor(server.URL, "test-key", 100, 0, 1000, time.Second, extractor)
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
	client := NewClientWithImageTextExtractor(server.URL, "test-key", 100, 0, 1000, time.Second, fakeImageTextExtractor{text: "source"})
	result, err := client.TranslateImage(context.Background(), localImageRequest(t))
	if err != nil || result.Text != "one\ntwo\n\nthree C:\\\\react folder\\nsys" {
		t.Fatalf("TranslateImage() = %q, %v", result.Text, err)
	}
}

func TestClientTranslateImageAllowsEmptyOCRResult(t *testing.T) {
	t.Parallel()
	client := NewClientWithImageTextExtractor("http://127.0.0.1:1", "test-key", 100, 0, 1000, time.Second, fakeImageTextExtractor{})
	result, err := client.TranslateImage(context.Background(), localImageRequest(t))
	if err != nil || result.Text != "" {
		t.Fatalf("TranslateImage() = %+v, %v", result, err)
	}
}

type fakeImageTextExtractor struct {
	text string
	err  error
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
