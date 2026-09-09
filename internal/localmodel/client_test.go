package localmodel

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sympllate/translator/internal/config"
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

func TestRenderTranslateGemmaCanonicalPrompt(t *testing.T) {
	t.Parallel()
	want := "<start_of_turn>user\nYou are a professional Russian (ru) to English (en) translator. Your goal is to accurately convey the meaning and nuances of the original Russian text while adhering to English grammar, vocabulary, and cultural sensitivities.\nProduce only the English translation, without any additional explanations or commentary. Please translate the following Russian text into English:\n\n\nSource text<end_of_turn>\n<start_of_turn>model\n"
	got, err := renderTranslateGemmaCanonicalPrompt("ru", "en", "  Source text\n")
	if err != nil || got != want {
		t.Fatalf("renderTranslateGemmaCanonicalPrompt() = %q, %v; want %q", got, err, want)
	}
	for _, forbidden := range []string{"<bos>", "SYMPLLATE_SOURCE_BEGIN", "SYMPLLATE_SOURCE_END"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("canonical prompt contains %q: %q", forbidden, got)
		}
	}
}

func TestRawTranslateGemmaRequest(t *testing.T) {
	t.Parallel()
	wantPrompt, err := renderTranslateGemmaCanonicalPrompt("ru", "en", "Source text")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/completion" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer raw-key" {
			t.Errorf("Authorization = %q", got)
		}
		var body rawCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if body.Prompt != wantPrompt || body.NPredict != 321 || body.Temperature != 0.25 || body.Stream {
			t.Errorf("unexpected raw request: %+v", body)
		}
		_, _ = io.WriteString(w, `{"content":"Translation: Hello","tokens_evaluated":76}`)
	}))
	defer server.Close()
	client := NewClient(server.URL, "raw-key", 321, 0.25, 100, time.Second)
	client.profile = config.ProfileTranslateGemmaRaw
	result, err := client.Translate(t.Context(), translation.TranslateRequest{Text: "Source text", Source: "ru", Target: "en"})
	if err != nil || result.Text != "Hello" {
		t.Fatalf("Translate() = %+v, %v", result, err)
	}
}

func TestRawProfileCompleteUsesRawEndpoint(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/completion" {
			t.Errorf("Complete endpoint = %q", r.URL.Path)
		}
		var body rawCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if body.Prompt != "batch prompt" {
			t.Errorf("Complete prompt = %q", body.Prompt)
		}
		_, _ = io.WriteString(w, `{"content":"result"}`)
	}))
	defer server.Close()
	client := NewClient(server.URL, "key", 100, 0, 100, time.Second)
	client.profile = config.ProfileTranslateGemmaRaw
	result, err := client.Complete(t.Context(), "batch prompt")
	if err != nil || result != "result" {
		t.Fatalf("Complete() = %q, %v", result, err)
	}
}

func TestParseRawCompletionResponse(t *testing.T) {
	t.Parallel()
	result, err := ParseRawCompletionResponse(http.StatusOK, []byte(`{"content":"Translation: Hello"}`))
	if err != nil || result.Text != "Hello" {
		t.Fatalf("ParseRawCompletionResponse() = %+v, %v", result, err)
	}
	for _, test := range []struct {
		status int
		body   string
	}{
		{http.StatusInternalServerError, `{"error":{"code":500,"message":"generation failed","type":"server_error"}}`},
		{http.StatusBadGateway, `{`},
		{http.StatusOK, `{`},
		{http.StatusOK, `{"content":" "}`},
	} {
		if _, err := ParseRawCompletionResponse(test.status, []byte(test.body)); err == nil {
			t.Fatalf("ParseRawCompletionResponse(%d, %q) expected error", test.status, test.body)
		}
	}
}

func TestParseRawCompletionResponsePreservesContextOverflow(t *testing.T) {
	t.Parallel()
	_, err := ParseRawCompletionResponse(http.StatusBadRequest, []byte(`{"error":{"message":"request (2517 tokens) exceeds the available context size (2048 tokens)"}}`))
	var overflow *ContextOverflowError
	if !errors.As(err, &overflow) || overflow.RequestedTokens != 2517 || overflow.AvailableTokens != 2048 {
		t.Fatalf("ParseRawCompletionResponse() error = %#v", err)
	}
}

func TestRawCompletionCancellationAndTimeout(t *testing.T) {
	for _, test := range []struct {
		name    string
		timeout time.Duration
		cancel  bool
	}{
		{name: "cancellation", timeout: time.Minute, cancel: true},
		{name: "timeout", timeout: 10 * time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-r.Context().Done():
				case <-time.After(100 * time.Millisecond):
				}
			}))
			defer server.Close()
			client := NewClient(server.URL, "key", 10, 0, 100, test.timeout)
			client.profile = config.ProfileTranslateGemmaRaw
			ctx, cancel := context.WithCancel(context.Background())
			if test.cancel {
				cancel()
			} else {
				defer cancel()
			}
			_, err := client.Complete(ctx, "prompt")
			if test.cancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("Complete() error = %v, want cancellation", err)
			}
			if !test.cancel && (err == nil || !strings.Contains(err.Error(), "did not respond in time")) {
				t.Fatalf("Complete() error = %v, want timeout", err)
			}
		})
	}
}

func TestRawCompletionHTTPFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "non-2xx", status: http.StatusBadRequest, body: `{}`},
		{name: "server error payload", status: http.StatusInternalServerError, body: `{"error":{"code":500,"message":"generation failed","type":"server_error"}}`},
		{name: "malformed JSON", status: http.StatusOK, body: `{`},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/completion" {
					t.Errorf("endpoint = %q", r.URL.Path)
				}
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			client := NewClient(server.URL, "key", 10, 0, 100, time.Second)
			client.profile = config.ProfileTranslateGemmaRaw
			if _, err := client.Complete(t.Context(), "prompt"); err == nil {
				t.Fatal("Complete() expected error")
			}
		})
	}
}

func TestRawTranslateGemmaRejectsAutoWithoutSendingRequest(t *testing.T) {
	t.Parallel()
	client := NewClient("http://127.0.0.1:1", "key", 100, 0, 2000, time.Second)
	client.profile = config.ProfileTranslateGemmaRaw
	_, err := client.Translate(context.Background(), translation.TranslateRequest{Text: "hello", Source: "auto", Target: "ru"})
	if err == nil || !strings.Contains(err.Error(), "explicit source language") {
		t.Fatalf("expected source language validation, got %v", err)
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
	if err != nil || result.Text != "translated" || result.DetectedLanguage != "de" || result.TargetLanguage != "ru" {
		t.Fatalf("TranslateImage() = %+v, %v", result, err)
	}
	if len(extractor.sources) != 1 || extractor.sources[0] != "auto" {
		t.Fatalf("OCR sources = %+v", extractor.sources)
	}
}

func TestClientTranslateImageChangesCollidingDetectedTargetBeforeTranslation(t *testing.T) {
	t.Parallel()
	var translationCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		translationCalls.Add(1)
		var request translateGemmaRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		content := request.Messages[0].Content[0]
		if content.SourceLangCode != "ru" || content.TargetLangCode != "en" || content.Text != "recognized source" {
			t.Errorf("translation content = %+v", content)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"translated"}}]}`))
	}))
	defer server.Close()
	extractor := &trackingImageTextExtractor{text: "recognized source"}
	identifier := language.NewLanguageIdentifier(localClassifier{detection: language.Detection{Language: "ru", Reliable: true}})
	client := NewClientWithImageTextExtractor(server.URL, "test-key", 100, 0, 1000, time.Second, extractor, identifier)

	request := localImageRequest(t)
	request.Source = "auto"
	request.Target = "ru"
	request.DefaultLanguageFirst = "ru"
	request.DefaultLanguageSecond = "en"
	result, err := client.TranslateImage(context.Background(), request)
	if err != nil || result.Text != "translated" || result.DetectedLanguage != "ru" || result.TargetLanguage != "en" {
		t.Fatalf("TranslateImage() = %+v, %v", result, err)
	}
	if len(extractor.sources) != 1 || extractor.sources[0] != "auto" {
		t.Fatalf("OCR sources = %+v", extractor.sources)
	}
	if calls := translationCalls.Load(); calls != 1 {
		t.Fatalf("translation calls = %d, want 1", calls)
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
