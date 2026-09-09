package translation

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
)

type queuedCompleter struct {
	responses []string
	err       error
	prompts   []string
}

type batchBlockCompleter struct {
	requests      []TranslateRequest
	completeCalls int
	translate     func(TranslateRequest) (TranslateResult, error)
}

func (c *batchBlockCompleter) Complete(context.Context, string) (string, error) {
	c.completeCalls++
	return "", errors.New("unexpected structured completion")
}

func (c *batchBlockCompleter) BatchBlockTranslator() BatchBlockTranslator { return c }

func (c *batchBlockCompleter) Translate(_ context.Context, req TranslateRequest) (TranslateResult, error) {
	c.requests = append(c.requests, req)
	return c.translate(req)
}

func (c *queuedCompleter) Complete(_ context.Context, prompt string) (string, error) {
	c.prompts = append(c.prompts, prompt)
	if c.err != nil {
		return "", c.err
	}
	if len(c.responses) == 0 {
		return completePrompt(prompt), nil
	}
	response := c.responses[0]
	c.responses = c.responses[1:]
	return response, nil
}

func completePrompt(prompt string) string {
	marker := "Input JSON:\n"
	index := strings.LastIndex(prompt, marker)
	if index < 0 {
		return `{}`
	}
	var input promptDocument
	_ = json.Unmarshal([]byte(prompt[index+len(marker):]), &input)
	for index := range input.Blocks {
		input.Blocks[index].Text = "translated:" + input.Blocks[index].Text
	}
	data, _ := json.Marshal(struct {
		Blocks []promptBlock `json:"blocks"`
	}{input.Blocks})
	return string(data)
}

func TestBuildStructuredPromptContainsJSONProtocol(t *testing.T) {
	prompt, err := BuildStructuredPrompt("auto", "ru", []promptBlock{{ID: "p1-b1-par1", Text: "Install"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"Return valid JSON only", "Preserve every block ID exactly", "exactly once", "Preserve backslashes", `"sourceLanguage":"auto"`, `"id":"p1-b1-par1"`} {
		if !strings.Contains(prompt, required) {
			t.Errorf("prompt missing %q", required)
		}
	}
}

func TestValidateStructuredResponseNormalizesImageText(t *testing.T) {
	expected := []promptBlock{{ID: "a", Text: "source"}}
	response := `{"blocks":[{"id":"a","text":"one\\r\\n\\r\\ntwo C:\\\\react folder\\nsys"}]}`
	result, err := ValidateStructuredResponse(response, expected, 4096)
	if err != nil || result["a"] != "one\n\ntwo C:\\\\react folder\\nsys" {
		t.Fatalf("result=%q err=%v", result["a"], err)
	}
}

func TestValidateStructuredResponse(t *testing.T) {
	expected := []promptBlock{{ID: "a", Text: "one"}, {ID: "b", Text: "two"}}
	valid := `{"blocks":[{"id":"a","text":"один"},{"id":"b","text":"два"}]}`
	for name, value := range map[string]string{
		"plain":      valid,
		"fence":      "```json\n" + valid + "\n```",
		"CRLF fence": "```\r\n" + valid + "\r\n```",
	} {
		t.Run(name, func(t *testing.T) {
			result, err := ValidateStructuredResponse(value, expected, 4096)
			if err != nil || result["b"] != "два" {
				t.Fatalf("result=%v err=%v", result, err)
			}
		})
	}
	for name, value := range map[string]string{
		"missing":   `{"blocks":[{"id":"a","text":"x"}]}`,
		"extra":     `{"blocks":[{"id":"a","text":"x"},{"id":"z","text":"y"}]}`,
		"duplicate": `{"blocks":[{"id":"a","text":"x"},{"id":"a","text":"y"}]}`,
		"empty":     `{"blocks":[{"id":"a","text":""},{"id":"b","text":"y"}]}`,
		"trailing":  valid + " explanation",
		"invalid":   `{not json}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ValidateStructuredResponse(value, expected, 4096); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestStructuredTranslatorRepairRetry(t *testing.T) {
	completer := &queuedCompleter{responses: []string{`not json`, `{"blocks":[{"id":"p1-b1-par1","text":"готово"}]}`}}
	translator, _ := NewStructuredTranslator(completer, 4000)
	result, chunks, err := translator.Translate(context.Background(), "en", "ru", []TranslationBlock{{ID: "p1-b1-par1", Text: "done"}})
	if err != nil || chunks != 1 || len(completer.prompts) != 2 || result[0].Text != "готово" {
		t.Fatalf("result=%v chunks=%d calls=%d err=%v", result, chunks, len(completer.prompts), err)
	}
	if !strings.Contains(completer.prompts[1], "previous output violated") {
		t.Fatal("repair prompt is not strict")
	}
}

func TestStructuredTranslatorFailsAfterOneRepair(t *testing.T) {
	completer := &queuedCompleter{responses: []string{`{}`, `{}`}}
	translator, _ := NewStructuredTranslator(completer, 4000)
	_, _, err := translator.Translate(context.Background(), "en", "ru", []TranslationBlock{{ID: "a", Text: "text"}})
	var protocolErr *ProtocolError
	if !errors.As(err, &protocolErr) || len(completer.prompts) != 2 {
		t.Fatalf("err=%v calls=%d", err, len(completer.prompts))
	}
}

func TestStructuredTranslatorChunksAndReassemblesOversizedBlock(t *testing.T) {
	completer := &queuedCompleter{}
	translator, _ := NewStructuredTranslator(completer, 1000)
	blocks := []TranslationBlock{
		{ID: "a", Text: strings.Repeat("A", 300)},
		{ID: "b", Text: strings.Repeat("B", 800), Lines: []string{strings.Repeat("B", 400), strings.Repeat("C", 400)}},
	}
	result, chunks, err := translator.Translate(context.Background(), "en", "ru", blocks)
	if err != nil {
		t.Fatal(err)
	}
	if chunks < 2 || len(completer.prompts) != chunks {
		t.Fatalf("chunks=%d calls=%d", chunks, len(completer.prompts))
	}
	if len(result) != 2 || !strings.Contains(result[1].Text, "\n") || len(result[1].Parts) < 2 || !strings.HasPrefix(result[1].Parts[0].ID, "b-part-") {
		t.Fatalf("result=%v", result)
	}
}

func TestStructuredTranslatorWrapsCompletionFailure(t *testing.T) {
	completer := &queuedCompleter{err: errors.New("offline")}
	translator, _ := NewStructuredTranslator(completer, 4000)
	_, _, err := translator.Translate(context.Background(), "en", "ru", []TranslationBlock{{ID: "a", Text: "text"}})
	var completionErr *CompletionError
	if !errors.As(err, &completionErr) {
		t.Fatalf("err=%v", err)
	}
}

func TestStructuredTranslatorUsesBatchBlockTranslatorAndPreservesOrder(t *testing.T) {
	completer := &batchBlockCompleter{translate: func(req TranslateRequest) (TranslateResult, error) {
		return TranslateResult{Text: "translated:" + req.Text}, nil
	}}
	translator, _ := NewStructuredTranslator(completer, 4000)
	blocks := []TranslationBlock{{ID: "b", Text: "second"}, {ID: "a", Text: "first"}}
	result, requests, err := translator.Translate(t.Context(), "en", "ru", blocks)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || completer.completeCalls != 0 || len(completer.requests) != 2 {
		t.Fatalf("requests=%d completeCalls=%d modelRequests=%d", requests, completer.completeCalls, len(completer.requests))
	}
	if result[0].ID != "b" || result[0].Text != "translated:second" || result[1].ID != "a" || result[1].Text != "translated:first" {
		t.Fatalf("result=%+v", result)
	}
	for index, req := range completer.requests {
		if req.Source != "en" || req.Target != "ru" || req.Text != blocks[index].Text {
			t.Errorf("request[%d]=%+v", index, req)
		}
	}
}

func TestStructuredTranslatorBatchBlockPathReassemblesOversizedParts(t *testing.T) {
	completer := &batchBlockCompleter{translate: func(req TranslateRequest) (TranslateResult, error) {
		return TranslateResult{Text: "translated:" + req.Text}, nil
	}}
	translator, _ := NewStructuredTranslator(completer, 1000)
	block := TranslationBlock{ID: "large", Text: strings.Repeat("A", 800), Lines: []string{strings.Repeat("B", 400), strings.Repeat("C", 400)}}
	result, requests, err := translator.Translate(t.Context(), "en", "ru", []TranslationBlock{block})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || len(result[0].Parts) < 2 || requests != len(result[0].Parts) || len(completer.requests) != requests {
		t.Fatalf("result=%+v requests=%d modelRequests=%d", result, requests, len(completer.requests))
	}
	if result[0].ID != block.ID || !strings.Contains(result[0].Text, "\n") {
		t.Fatalf("reassembled result=%+v", result[0])
	}
	for index, part := range result[0].Parts {
		if part.ID != "large-part-"+strconv.Itoa(index+1) || part.SourceText != completer.requests[index].Text || part.TranslatedText != "translated:"+part.SourceText {
			t.Errorf("part[%d]=%+v request=%+v", index, part, completer.requests[index])
		}
	}
}

func TestStructuredTranslatorBatchBlockPathHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	completer := &batchBlockCompleter{}
	completer.translate = func(req TranslateRequest) (TranslateResult, error) {
		cancel()
		return TranslateResult{Text: "translated:" + req.Text}, nil
	}
	translator, _ := NewStructuredTranslator(completer, 4000)
	_, _, err := translator.Translate(ctx, "en", "ru", []TranslationBlock{{ID: "a", Text: "one"}, {ID: "b", Text: "two"}})
	if !errors.Is(err, context.Canceled) || len(completer.requests) != 1 {
		t.Fatalf("err=%v requests=%d", err, len(completer.requests))
	}
}

func TestStructuredTranslatorBatchBlockPathRejectsEmptyTranslation(t *testing.T) {
	completer := &batchBlockCompleter{translate: func(TranslateRequest) (TranslateResult, error) {
		return TranslateResult{Text: " \n "}, nil
	}}
	translator, _ := NewStructuredTranslator(completer, 4000)
	_, _, err := translator.Translate(t.Context(), "en", "ru", []TranslationBlock{{ID: "a", Text: "one"}})
	var completionErr *CompletionError
	if !errors.As(err, &completionErr) || !strings.Contains(err.Error(), "empty translation") {
		t.Fatalf("err=%v", err)
	}
}
