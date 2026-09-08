package localmodel

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sympllate/translator/internal/translation"
)

func TestProfileRequestsPreserveSource(t *testing.T) {
	for _, profile := range []string{"translategemma", "generic"} {
		t.Run(profile, func(t *testing.T) {
			source := "  # Header\n\nThis is **important** and \"quoted\".\n`id_name` {value} https://example.org 12 kg\r\n```go\npath := `C:\\new`\n```\nПривет <tag> & </text>\t"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
					t.Errorf("unexpected endpoint: %s %s", r.Method, r.URL.Path)
				}
				payload, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				if profile == "translategemma" {
					var body struct {
						Messages []struct {
							Role    string              `json:"role"`
							Content []map[string]string `json:"content"`
						} `json:"messages"`
					}
					if err := json.Unmarshal(payload, &body); err != nil {
						t.Error(err)
						return
					}
					if len(body.Messages) != 1 || body.Messages[0].Role != "user" || len(body.Messages[0].Content) != 1 {
						t.Errorf("unexpected messages: %+v", body.Messages)
						return
					}
					part := body.Messages[0].Content[0]
					if len(part) != 4 || part["type"] != "text" || part["source_lang_code"] != "en" || part["target_lang_code"] != "de-DE" || part["text"] != source {
						t.Errorf("unexpected native content: %+v", part)
					}
					for _, forbidden := range []string{"You are a machine translation engine", "Source text (JSON string)", "Decode it literally", "Return only", "Rules:", "<text>"} {
						if strings.Contains(string(payload), forbidden) {
							t.Errorf("native request contains generic instruction %q", forbidden)
						}
					}
				} else {
					var body chatRequest
					if err := json.Unmarshal(payload, &body); err != nil {
						t.Error(err)
						return
					}
					if len(body.Messages) != 1 || body.Messages[0].Role != "user" {
						t.Errorf("unexpected messages: %+v", body.Messages)
						return
					}
					prompt := body.Messages[0].Content
					for _, required := range []string{"Translate the following text from en to de-DE", "Return only the translation", "Do not add commentary", "Markdown", "line breaks", "URLs", "numbers", "units", "inline code", "identifiers", "placeholders"} {
						if !strings.Contains(prompt, required) {
							t.Errorf("missing instruction %q", required)
						}
					}
					expected := buildGenericPrompt(translation.TranslateRequest{Text: source, Source: "en", Target: "de-DE"})
					if prompt != expected.text || strings.Contains(prompt, "Decode it literally") {
						t.Errorf("source was changed or not separated: %q", prompt)
					}
				}
				_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"Translation: \"Legitimate quotes\""}}]}`)
			}))
			defer server.Close()
			client := NewClient(server.URL, "key", 100, 0, 2000, time.Second)
			client.profile = profile
			result, err := client.Translate(t.Context(), translation.TranslateRequest{Text: source, Source: "en", Target: "de-DE"})
			if err != nil || result.Text != `"Legitimate quotes"` {
				t.Fatalf("Translate = %+v, %v", result, err)
			}
		})
	}
}

func TestTranslateGemmaRejectsAutoWithoutSendingRequest(t *testing.T) {
	client := NewClient("http://127.0.0.1:1", "key", 100, 0, 2000, time.Second)
	_, err := client.Translate(context.Background(), translation.TranslateRequest{Text: "hello", Source: "auto", Target: "ru"})
	if err == nil || !strings.Contains(err.Error(), "explicit source language") {
		t.Fatalf("expected source language validation, got %v", err)
	}
	client.imageTextExtractor = fakeImageTextExtractor{text: "recognized text"}
	imageRequest := localImageRequest(t)
	imageRequest.Source = "auto"
	if _, err := client.TranslateImage(t.Context(), imageRequest); err == nil || !strings.Contains(err.Error(), "explicit source language") {
		t.Fatalf("expected the same validation after OCR, got %v", err)
	}
}

func TestGenericAllowsAutoAndCompletePreservesBatchProtocol(t *testing.T) {
	var prompts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body chatRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		prompts = append(prompts, body.Messages[0].Content)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"{\"blocks\":[{\"id\":\"1\",\"text\":\"translated\"}]}"}}]}`)
	}))
	defer server.Close()
	client := NewClient(server.URL, "key", 100, 0, 2000, time.Second)
	batchPrompt := `Return JSON: {"blocks":[{"id":"1","text":"hello"}]}`
	for _, profile := range []string{"translategemma", "generic"} {
		client.profile = profile
		result, err := client.Complete(t.Context(), batchPrompt)
		if err != nil || result != `{"blocks":[{"id":"1","text":"translated"}]}` {
			t.Fatalf("Complete(%s) = %q, %v", profile, result, err)
		}
	}
	_, err := client.Translate(t.Context(), translation.TranslateRequest{Text: "hello", Source: "auto", Target: "ru"})
	if err != nil || len(prompts) != 3 || prompts[0] != batchPrompt || prompts[1] != batchPrompt || !strings.Contains(prompts[2], "from auto to ru") {
		t.Fatalf("prompts = %q, err = %v", prompts, err)
	}
}

func TestGenericTranslationNormalResponseIsUnchanged(t *testing.T) {
	t.Parallel()
	prompt := buildGenericPrompt(translation.TranslateRequest{Text: "source", Source: "en", Target: "ru"})
	response := "  **translation**\n\n`C:\\new` and \\d+  "
	if got := recoverGenericTranslation(response, prompt); got != response {
		t.Fatalf("recoverGenericTranslation() = %q, want unchanged %q", got, response)
	}
}

func TestGenericTranslationRecoversEchoedPrompt(t *testing.T) {
	t.Parallel()
	req := translation.TranslateRequest{Text: "source text", Source: "ru", Target: "en"}
	prompt := buildGenericPrompt(req)
	want := `Awful start of the recording because of a missed button - CHECK
Genre of the game: shit, impossible to stop - CHECK
What kind of picture? 3D! - CHECK
Apparently, a joke remained on the unrecorded part, so I'll mark it with a check.`
	echoed := strings.Replace(prompt.text, req.Text, "\n"+want+"\n", 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body chatRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if len(body.Messages) != 1 || body.Messages[0].Content != prompt.text {
			t.Errorf("unexpected generic prompt: %+v", body.Messages)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": echoed}}},
		})
	}))
	defer server.Close()
	client := NewClient(server.URL, "key", 100, 0, 2000, time.Second)
	client.profile = "generic"
	result, err := client.Translate(t.Context(), req)
	if err != nil || result.Text != want {
		t.Fatalf("Translate() = %q, %v; want recovered translation %q", result.Text, err, want)
	}
}

func TestGenericTranslationMarkersDoNotCollideWithSource(t *testing.T) {
	t.Parallel()
	source := "<<<SYMPLLATE_SOURCE_BEGIN_0>>>\n<<<SYMPLLATE_SOURCE_END_1>>>"
	prompt := buildGenericPrompt(translation.TranslateRequest{Text: source, Source: "en", Target: "ru"})
	if prompt.sourceBegin != "<<<SYMPLLATE_SOURCE_BEGIN_2>>>" || prompt.sourceEnd != "<<<SYMPLLATE_SOURCE_END_2>>>" {
		t.Fatalf("markers = %q, %q; want suffix 2", prompt.sourceBegin, prompt.sourceEnd)
	}
	if strings.Contains(source, prompt.sourceBegin) || strings.Contains(source, prompt.sourceEnd) {
		t.Fatalf("markers collide with source: %q", source)
	}
}

func TestGenericTranslationPreservesLiteralTextTags(t *testing.T) {
	t.Parallel()
	source := "This source contains <text> and </text>."
	prompt := buildGenericPrompt(translation.TranslateRequest{Text: source, Source: "en", Target: "ru"})
	wantFraming := prompt.sourceBegin + "\n" + source + "\n" + prompt.sourceEnd
	if !strings.Contains(prompt.text, wantFraming) {
		t.Fatalf("literal text tags were not preserved in prompt: %q", prompt.text)
	}
}

func TestGenericTranslationDoesNotRecoverIncompleteEcho(t *testing.T) {
	t.Parallel()
	prompt := buildGenericPrompt(translation.TranslateRequest{Text: "source", Source: "en", Target: "ru"})
	for _, response := range []string{
		"prefix\n" + prompt.sourceBegin + "\ntranslated text",
		"prefix\ntranslated text\n" + prompt.sourceEnd,
	} {
		if got := recoverGenericTranslation(response, prompt); got != response {
			t.Fatalf("recoverGenericTranslation() = %q, want unchanged %q", got, response)
		}
	}
}

func TestGenericTranslationDoesNotRecoverMultipleMarkerPairs(t *testing.T) {
	t.Parallel()
	prompt := buildGenericPrompt(translation.TranslateRequest{Text: "source", Source: "en", Target: "ru"})
	for _, response := range []string{
		prompt.sourceBegin + "\nfoo\n" + prompt.sourceBegin + "\nbar\n" + prompt.sourceEnd,
		prompt.sourceBegin + "\nfoo\n" + prompt.sourceEnd + "\n" + prompt.sourceBegin + "\nbar\n" + prompt.sourceEnd,
	} {
		if got := recoverGenericTranslation(response, prompt); got != response {
			t.Fatalf("recoverGenericTranslation() = %q, want unchanged %q", got, response)
		}
	}
}

func TestGenericTranslationDoesNotRecoverReversedMarkers(t *testing.T) {
	t.Parallel()
	prompt := buildGenericPrompt(translation.TranslateRequest{Text: "source", Source: "en", Target: "ru"})
	response := prompt.sourceEnd + "\ntranslated text\n" + prompt.sourceBegin
	if got := recoverGenericTranslation(response, prompt); got != response {
		t.Fatalf("recoverGenericTranslation() = %q, want unchanged %q", got, response)
	}
}

func TestGenericTranslationDoesNotReplaceWithEmptyCandidate(t *testing.T) {
	t.Parallel()
	prompt := buildGenericPrompt(translation.TranslateRequest{Text: "source", Source: "en", Target: "ru"})
	response := "prefix\n" + prompt.sourceBegin + "\n \t\n" + prompt.sourceEnd
	if got := recoverGenericTranslation(response, prompt); got != response {
		t.Fatalf("recoverGenericTranslation() = %q, want unchanged %q", got, response)
	}
}
