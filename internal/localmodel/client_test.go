package localmodel

import (
	"context"
	"encoding/json"
	"errors"
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
	if err == nil || !strings.Contains(err.Error(), "не ответила вовремя") {
		t.Fatalf("Translate() error = %v", err)
	}
}
