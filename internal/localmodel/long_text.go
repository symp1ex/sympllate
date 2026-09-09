package localmodel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/sympllate/translator/internal/config"
	"github.com/sympllate/translator/internal/translation"
)

const (
	defaultLocalContextSize = 2048
	tokenCountResponseLimit = 64 << 10
)

var contextOverflowNumbers = regexp.MustCompile(`(?i)request\s*\(\s*(\d+)\s+tokens?\s*\).*?exceeds.*?context\s+size(?:\s+of)?\s*\(?\s*(\d+)\s+tokens?`)

type ContextOverflowError struct {
	StatusCode      int
	RequestedTokens int
	AvailableTokens int
	Message         string
}

func (e *ContextOverflowError) Error() string {
	return fmt.Sprintf("the local model returned HTTP %d: %s", e.StatusCode, e.Message)
}

func parseContextOverflowError(statusCode int, message string) *ContextOverflowError {
	if statusCode != http.StatusBadRequest {
		return nil
	}
	lower := strings.ToLower(message)
	if !strings.Contains(lower, "exceeds") || !strings.Contains(lower, "context size") {
		return nil
	}
	overflow := &ContextOverflowError{StatusCode: statusCode, Message: message}
	if matches := contextOverflowNumbers.FindStringSubmatch(message); len(matches) == 3 {
		overflow.RequestedTokens, _ = strconv.Atoi(matches[1])
		overflow.AvailableTokens, _ = strconv.Atoi(matches[2])
	}
	return overflow
}

type inputTokenCountResponse struct {
	InputTokens int `json:"input_tokens"`
}

type rawTokenizeRequest struct {
	Content      string `json:"content"`
	AddSpecial   bool   `json:"add_special"`
	ParseSpecial bool   `json:"parse_special"`
}

type rawTokenizeResponse struct {
	Tokens []int `json:"tokens"`
}

func (c *Client) translateDocument(ctx context.Context, req translation.TranslateRequest) (string, error) {
	if err := c.validateProfileRequest(req); err != nil {
		return "", err
	}
	return translation.TranslateLongText(ctx, req, translation.LongTextOptions{
		RequestFits:        c.requestFits,
		TranslateOnce:      c.translateOnce,
		ContextOverflow:    contextOverflow,
		SuggestedChunkSize: c.suggestedChunkRunes,
	})
}

func (c *Client) validateProfileRequest(req translation.TranslateRequest) error {
	switch c.profile {
	case "translategemma", config.ProfileTranslateGemmaRaw:
		if strings.EqualFold(req.Source, "auto") {
			return errors.New("TranslateGemma requires an explicit source language; select a source language instead of auto")
		}
		return nil
	case "generic":
		return nil
	default:
		return fmt.Errorf("unsupported local model profile %q", c.profile)
	}
}

func contextOverflow(err error) error {
	var overflow *ContextOverflowError
	if errors.As(err, &overflow) {
		return overflow
	}
	return nil
}

func (c *Client) requestFits(ctx context.Context, req translation.TranslateRequest) (bool, int, error) {
	budget := c.inputTokenBudget()
	estimate := c.fallbackRequestTokenEstimate(req)
	if estimate <= budget {
		return true, 0, nil
	}
	payload, err := c.translationPayload(req)
	if err != nil {
		return false, 0, err
	}
	count, err := c.countInputTokens(ctx, payload)
	if err == nil {
		return count <= budget, count, nil
	}
	if ctx.Err() != nil {
		return false, 0, ctx.Err()
	}
	// Token counting is an optimization. Older compatible servers may not expose
	// the endpoint, so retain a conservative byte-based fallback.
	return estimate <= budget, 0, nil
}

func (c *Client) inputTokenBudget() int {
	contextSize := c.contextSize
	if contextSize <= 0 {
		contextSize = defaultLocalContextSize
	}
	return translation.SafeInputTokenBudget(contextSize, c.numPredict)
}

func (c *Client) fallbackRequestTokenEstimate(req translation.TranslateRequest) int {
	// A tokenizer may emit a byte fallback token for every source byte. Fixed
	// profile allowances cover the stable prompt/chat-template framing.
	overhead := 128
	if c.profile == "generic" {
		overhead = 256
	}
	return len([]byte(req.Text)) + overhead
}

func (c *Client) suggestedChunkRunes(text string, measuredTokens int) int {
	budget := c.inputTokenBudget()
	overhead := c.fallbackRequestTokenEstimate(translation.TranslateRequest{})
	return translation.SuggestedChunkRunes(text, measuredTokens, budget, overhead)
}

func (c *Client) translationPayload(req translation.TranslateRequest) ([]byte, error) {
	switch c.profile {
	case "translategemma":
		return c.marshalTranslateGemmaRequest(req)
	case config.ProfileTranslateGemmaRaw:
		prompt, err := renderTranslateGemmaCanonicalPrompt(req.Source, req.Target, req.Text)
		if err != nil {
			return nil, err
		}
		// /tokenize defaults to no added special tokens. These flags make its
		// sequence match /completion, including exactly one model-added BOS.
		payload, err := json.Marshal(rawTokenizeRequest{Content: prompt, AddSpecial: true, ParseSpecial: true})
		if err != nil {
			return nil, fmt.Errorf("marshal local token-count request: %w", err)
		}
		return payload, nil
	case "generic":
		return c.marshalChatRequest(buildGenericPrompt(req))
	default:
		return nil, fmt.Errorf("unsupported local model profile %q", c.profile)
	}
}

func (c *Client) countInputTokens(ctx context.Context, payload []byte) (int, error) {
	c.requestMu.Lock()
	defer c.requestMu.Unlock()
	endpoint := c.tokenCountEndpoint
	if c.profile == config.ProfileTranslateGemmaRaw {
		endpoint = c.rawTokenEndpoint
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, tokenCountResponseLimit+1))
	if err != nil {
		return 0, err
	}
	if len(body) > tokenCountResponseLimit || response.StatusCode < 200 || response.StatusCode >= 300 {
		return 0, errors.New("local token counting is unavailable")
	}
	if c.profile == config.ProfileTranslateGemmaRaw {
		var decoded rawTokenizeResponse
		if err := json.Unmarshal(body, &decoded); err != nil || len(decoded.Tokens) == 0 {
			return 0, errors.New("local token counting returned an invalid response")
		}
		return len(decoded.Tokens), nil
	}
	var decoded inputTokenCountResponse
	if err := json.Unmarshal(body, &decoded); err != nil || decoded.InputTokens <= 0 {
		return 0, errors.New("local token counting returned an invalid response")
	}
	return decoded.InputTokens, nil
}
