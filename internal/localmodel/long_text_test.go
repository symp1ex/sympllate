package localmodel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/sympllate/translator/internal/app"
	"github.com/sympllate/translator/internal/language"
	"github.com/sympllate/translator/internal/translation"
)

func TestShortTranslationUsesSingleRequestWithoutTokenCount(t *testing.T) {
	for _, profile := range []string{"generic", "translategemma"} {
		t.Run(profile, func(t *testing.T) {
			var completionCalls int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/input_tokens") {
					t.Error("short translation made a token-count request")
					return
				}
				completionCalls++
				source := requestSource(t, r, profile, "en", "ru")
				writeTranslation(t, w, strings.ToUpper(source))
			}))
			defer server.Close()
			client := newClient(server.URL, "key", 2048, 256, 0, 5000, time.Second)
			client.profile = profile
			result, err := client.Translate(t.Context(), translation.TranslateRequest{Text: "short text", Source: "en", Target: "ru"})
			if err != nil || result.Text != "SHORT TEXT" || completionCalls != 1 {
				t.Fatalf("Translate() = %+v, %v; calls=%d", result, err, completionCalls)
			}
		})
	}
}

func TestLongTranslationPreservesProfileProtocolAndStructure(t *testing.T) {
	for _, profile := range []string{"generic", "translategemma"} {
		t.Run(profile, func(t *testing.T) {
			first := strings.Repeat("alpha beta gamma. ", 6)
			second := strings.Repeat("привет мир. ", 7) + "\nthird line"
			source := first + "\n\n" + second
			var mu sync.Mutex
			var chunks []string
			var tokenCounts int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				text := requestSource(t, r, profile, "en", "ru")
				if strings.HasSuffix(r.URL.Path, "/input_tokens") {
					mu.Lock()
					tokenCounts++
					mu.Unlock()
					_ = json.NewEncoder(w).Encode(inputTokenCountResponse{InputTokens: utf8.RuneCountInString(text) + 40})
					return
				}
				mu.Lock()
				chunks = append(chunks, text)
				mu.Unlock()
				writeTranslation(t, w, strings.ToUpper(text))
			}))
			defer server.Close()
			client := newClient(server.URL, "key", 220, 40, 0, 5000, time.Second)
			client.profile = profile
			result, err := client.Translate(t.Context(), translation.TranslateRequest{Text: source, Source: "en", Target: "ru"})
			if err != nil || result.Text != strings.ToUpper(source) {
				t.Fatalf("Translate() = %q, %v; want %q", result.Text, err, strings.ToUpper(source))
			}
			if tokenCounts == 0 {
				t.Fatalf("chunks=%q token-count calls=%d, want long-text path", chunks, tokenCounts)
			}
			wantChunks := []string{strings.TrimSpace(first), second}
			if strings.Join(chunks, "\x00") != strings.Join(wantChunks, "\x00") {
				t.Fatalf("source chunks = %#v, want %#v", chunks, wantChunks)
			}
		})
	}
}

func TestLongTranslationFallsBackWhenTokenCountingIsUnavailable(t *testing.T) {
	source := strings.Repeat("word ", 100)
	var completionCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		text := requestSource(t, r, "translategemma", "en", "ru")
		if strings.HasSuffix(r.URL.Path, "/input_tokens") {
			http.NotFound(w, r)
			return
		}
		completionCalls++
		writeTranslation(t, w, strings.ToUpper(text))
	}))
	defer server.Close()
	client := newClient(server.URL, "key", 400, 50, 0, 5000, time.Second)
	result, err := client.Translate(t.Context(), translation.TranslateRequest{Text: source, Source: "en", Target: "ru"})
	if err != nil || result.Text != strings.ToUpper(source) || completionCalls < 2 {
		t.Fatalf("Translate() = %q, %v; completion calls=%d", result.Text, err, completionCalls)
	}
}

func TestContextOverflowSplitsOnlyFailingChunkAndRetries(t *testing.T) {
	source := "first paragraph words.\n\nsecond paragraph words."
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		text := requestSource(t, r, "translategemma", "en", "ru")
		calls = append(calls, text)
		if utf8.RuneCountInString(text) > 25 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"request (2517 tokens) exceeds the available context size (2048 tokens), try increasing it"}}`)
			return
		}
		writeTranslation(t, w, strings.ToUpper(text))
	}))
	defer server.Close()
	client := newClient(server.URL, "key", 2048, 256, 0, 5000, time.Second)
	result, err := client.Translate(t.Context(), translation.TranslateRequest{Text: source, Source: "en", Target: "ru"})
	if err != nil || result.Text != strings.ToUpper(source) {
		t.Fatalf("Translate() = %q, %v", result.Text, err)
	}
	if len(calls) != 3 || calls[0] != source || calls[1] != "first paragraph words." || calls[2] != "second paragraph words." {
		t.Fatalf("request order = %#v", calls)
	}
}

func TestContextOverflowDoesNotRepeatCompletedChunks(t *testing.T) {
	source := "first successful.\n\nproblematic chunk has many words here.\n\nlast successful."
	var calls []string
	overflowed := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		text := requestSource(t, r, "translategemma", "en", "ru")
		if strings.HasSuffix(r.URL.Path, "/input_tokens") {
			_ = json.NewEncoder(w).Encode(inputTokenCountResponse{InputTokens: utf8.RuneCountInString(text) + 20})
			return
		}
		calls = append(calls, text)
		if strings.Contains(text, "problematic") && !overflowed {
			overflowed = true
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"request (91 tokens) exceeds the available context size (80 tokens)"}}`)
			return
		}
		writeTranslation(t, w, strings.ToUpper(text))
	}))
	defer server.Close()
	client := newClient(server.URL, "key", 100, 25, 0, 5000, time.Second)
	result, err := client.Translate(t.Context(), translation.TranslateRequest{Text: source, Source: "en", Target: "ru"})
	if err != nil || result.Text != strings.ToUpper(source) {
		t.Fatalf("Translate() = %q, %v; calls=%#v", result.Text, err, calls)
	}
	firstCalls := 0
	for _, call := range calls {
		if call == "first successful." {
			firstCalls++
		}
	}
	if !overflowed || firstCalls != 1 {
		t.Fatalf("overflowed=%v first chunk calls=%d, all calls=%#v", overflowed, firstCalls, calls)
	}
}

func TestUnsplittableContextOverflowReturnsTypedError(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = requestSource(t, r, "translategemma", "en", "ru")
		calls++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"request (2517 tokens) exceeds the available context size (2048 tokens), try increasing it"}}`)
	}))
	defer server.Close()
	client := newClient(server.URL, "key", 2048, 256, 0, 5000, time.Second)
	_, err := client.Translate(t.Context(), translation.TranslateRequest{Text: "ab", Source: "en", Target: "ru"})
	var overflow *ContextOverflowError
	if !errors.As(err, &overflow) || overflow.RequestedTokens != 2517 || overflow.AvailableTokens != 2048 {
		t.Fatalf("Translate() error = %#v", err)
	}
	if calls != 2 {
		t.Fatalf("completion calls = %d, want bounded retry", calls)
	}
}

func TestChunkFailureStopsFurtherRequests(t *testing.T) {
	source := "first paragraph.\n\nsecond paragraph.\n\nthird paragraph."
	var completionCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		text := requestSource(t, r, "translategemma", "en", "ru")
		if strings.HasSuffix(r.URL.Path, "/input_tokens") {
			_ = json.NewEncoder(w).Encode(inputTokenCountResponse{InputTokens: utf8.RuneCountInString(text) + 30})
			return
		}
		completionCalls++
		if completionCalls == 2 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":{"message":"generation failed"}}`)
			return
		}
		writeTranslation(t, w, strings.ToUpper(text))
	}))
	defer server.Close()
	client := newClient(server.URL, "key", 100, 25, 0, 5000, time.Second)
	_, err := client.Translate(t.Context(), translation.TranslateRequest{Text: source, Source: "en", Target: "ru"})
	if err == nil || completionCalls != 2 {
		t.Fatalf("Translate() error=%v completion calls=%d", err, completionCalls)
	}
}

func TestChunkTranslationHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := "first paragraph.\n\nsecond paragraph.\n\nthird paragraph."
	var completionCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		text := requestSource(t, r, "translategemma", "en", "ru")
		if strings.HasSuffix(r.URL.Path, "/input_tokens") {
			_ = json.NewEncoder(w).Encode(inputTokenCountResponse{InputTokens: utf8.RuneCountInString(text) + 30})
			return
		}
		completionCalls++
		writeTranslation(t, w, strings.ToUpper(text))
		cancel()
	}))
	defer server.Close()
	client := newClient(server.URL, "key", 100, 25, 0, 5000, time.Second)
	_, err := client.Translate(ctx, translation.TranslateRequest{Text: source, Source: "en", Target: "ru"})
	if !errors.Is(err, context.Canceled) || completionCalls != 1 {
		t.Fatalf("Translate() error=%v completion calls=%d", err, completionCalls)
	}
}

func TestNonContextHTTPErrorsAreNotRetried(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = requestSource(t, r, "translategemma", "en", "ru")
				calls++
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":{"message":"ordinary failure"}}`)
			}))
			defer server.Close()
			client := newClient(server.URL, "key", 2048, 256, 0, 5000, time.Second)
			_, err := client.Translate(t.Context(), translation.TranslateRequest{Text: "short text", Source: "en", Target: "ru"})
			var overflow *ContextOverflowError
			if err == nil || errors.As(err, &overflow) || calls != 1 {
				t.Fatalf("Translate() error=%v calls=%d", err, calls)
			}
		})
	}
}

type countingClassifier struct{ calls int }

func (c *countingClassifier) Detect(string) language.Detection {
	c.calls++
	return language.Detection{Language: "de", Confidence: 1, Reliable: true, Reason: language.DetectionReasonClassifier}
}

func TestAutoDetectionRunsOnceBeforeLongTextTranslation(t *testing.T) {
	classifier := &countingClassifier{}
	identifier := language.NewLanguageIdentifier(classifier)
	source := strings.Repeat("deutscher text. ", 20)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		text := requestSource(t, r, "translategemma", "de", "ru")
		if strings.HasSuffix(r.URL.Path, "/input_tokens") {
			_ = json.NewEncoder(w).Encode(inputTokenCountResponse{InputTokens: utf8.RuneCountInString(text) + 30})
			return
		}
		writeTranslation(t, w, strings.ToUpper(text))
	}))
	defer server.Close()
	client := newClient(server.URL, "key", 140, 30, 0, 5000, time.Second)
	service := app.NewService(t.Context(), client, identifier, "ru", "en", nil)
	result, err := service.Translate(t.Context(), translation.TranslateRequest{Text: source, Source: "auto", Target: "ru"})
	if err != nil || result.DetectedLanguage != "de" || classifier.calls != 1 {
		t.Fatalf("Translate() = %+v, %v; detection calls=%d", result, err, classifier.calls)
	}
}

func requestSource(t *testing.T, r *http.Request, profile, wantSource, wantTarget string) string {
	t.Helper()
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		t.Error(err)
		return ""
	}
	r.Body = io.NopCloser(strings.NewReader(string(payload)))
	if profile == "translategemma" {
		var body translateGemmaRequest
		if err := json.Unmarshal(payload, &body); err != nil || len(body.Messages) != 1 || len(body.Messages[0].Content) != 1 {
			t.Errorf("invalid TranslateGemma request: %s (%v)", payload, err)
			return ""
		}
		content := body.Messages[0].Content[0]
		if content.Type != "text" || content.SourceLangCode != wantSource || content.TargetLangCode != wantTarget {
			t.Errorf("invalid TranslateGemma content: %+v", content)
			return ""
		}
		return content.Text
	}
	var body chatRequest
	if err := json.Unmarshal(payload, &body); err != nil || len(body.Messages) != 1 {
		t.Errorf("invalid generic request: %s (%v)", payload, err)
		return ""
	}
	prompt := body.Messages[0].Content
	wantDirection := "Translate the following text from " + wantSource + " to " + wantTarget + "."
	if !strings.Contains(prompt, wantDirection) {
		t.Errorf("generic prompt direction = %q, want %q", prompt, wantDirection)
		return ""
	}
	begin := strings.Index(prompt, "<<<SYMPLLATE_SOURCE_BEGIN_")
	if begin < 0 {
		t.Errorf("generic prompt lost source marker: %q", prompt)
		return ""
	}
	begin = strings.Index(prompt[begin:], "\n") + begin + 1
	end := strings.LastIndex(prompt, "\n<<<SYMPLLATE_SOURCE_END_")
	if begin <= 0 || end < begin {
		t.Errorf("generic prompt has invalid source framing: %q", prompt)
		return ""
	}
	return prompt[begin:end]
}

func writeTranslation(t *testing.T, w http.ResponseWriter, text string) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(map[string]any{
		"choices": []any{map[string]any{"message": map[string]string{"content": text}}},
	}); err != nil {
		t.Error(err)
	}
}
