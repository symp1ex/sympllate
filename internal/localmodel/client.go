package localmodel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sympllate/translator/internal/translation"
)

const maxResponseBytes = 4 << 20

type Client struct {
	endpoint           string
	apiKey             string
	model              string
	numPredict         int
	temperature        float64
	maxInputCharacters int
	httpClient         *http.Client
	imageTextExtractor ImageTextExtractor
	requestMu          sync.Mutex
}

type ImageTextExtractor interface {
	Capability() translation.ImageCapability
	Recognize(ctx context.Context, image translation.ValidatedImage, source string) (string, error)
}

func NewClient(baseURL, apiKey string, numPredict int, temperature float64, maxInputCharacters int, timeout time.Duration) *Client {
	return &Client{
		endpoint: strings.TrimRight(baseURL, "/") + "/v1/chat/completions",
		apiKey:   apiKey, model: ModelAlias, numPredict: numPredict, temperature: temperature,
		maxInputCharacters: maxInputCharacters, httpClient: &http.Client{Timeout: timeout},
	}
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Stream      bool          `json:"stream"`
	MaxTokens   int           `json:"max_tokens"`
	Temperature float64       `json:"temperature"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *Client) Translate(ctx context.Context, req translation.TranslateRequest) (translation.TranslateResult, error) {
	if err := translation.ValidateRequest(req, c.maxInputCharacters); err != nil {
		return translation.TranslateResult{}, err
	}
	prompt, err := translation.BuildPrompt(req.Text, req.Source, req.Target)
	if err != nil {
		return translation.TranslateResult{}, err
	}
	text, err := c.Complete(ctx, prompt)
	if err != nil {
		return translation.TranslateResult{}, err
	}
	return translation.TranslateResult{Text: translation.CleanResult(text)}, nil
}

// Complete runs one raw TranslateGemma prompt. Model requests are serialized so
// manual, single-image, and batch translations cannot overlap.
func (c *Client) Complete(ctx context.Context, prompt string) (string, error) {
	if strings.TrimSpace(prompt) == "" {
		return "", errors.New("model prompt is empty")
	}
	c.requestMu.Lock()
	defer c.requestMu.Unlock()
	payload, err := json.Marshal(chatRequest{
		Model: c.model, Messages: []chatMessage{{Role: "user", Content: prompt}}, Stream: false,
		MaxTokens: c.numPredict, Temperature: c.temperature,
	})
	if err != nil {
		return "", fmt.Errorf("marshal local request: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create local request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+c.apiKey)
	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return "", context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", errors.New("the local model did not respond in time")
		}
		return "", fmt.Errorf("the local model is unavailable: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return "", fmt.Errorf("read local model response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return "", errors.New("the local model response is too large")
	}
	result, err := ParseChatResponse(response.StatusCode, body)
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

func NewClientWithImageTextExtractor(baseURL, apiKey string, numPredict int, temperature float64, maxInputCharacters int, timeout time.Duration, extractor ImageTextExtractor) *Client {
	client := NewClient(baseURL, apiKey, numPredict, temperature, maxInputCharacters, timeout)
	client.imageTextExtractor = extractor
	return client
}

func (c *Client) TranslateImage(ctx context.Context, req translation.ImageTranslateRequest) (translation.ImageTranslateResult, error) {
	validated, err := translation.ValidateImageRequest(req)
	if err != nil {
		return translation.ImageTranslateResult{}, err
	}
	capability := c.ImageCapability()
	if !capability.Supported {
		return translation.ImageTranslateResult{}, errors.New(capability.Reason)
	}
	text, err := c.imageTextExtractor.Recognize(ctx, validated, req.Source)
	if err != nil {
		return translation.ImageTranslateResult{}, err
	}
	if strings.TrimSpace(text) == "" {
		return translation.ImageTranslateResult{}, nil
	}
	result, err := c.Translate(ctx, translation.TranslateRequest{Text: text, Source: req.Source, Target: req.Target})
	if err != nil {
		return translation.ImageTranslateResult{}, err
	}
	return translation.ImageTranslateResult{Text: result.Text, DetectedLanguage: result.DetectedLanguage}, nil
}

func (c *Client) ImageCapability() translation.ImageCapability {
	if c.imageTextExtractor == nil {
		return translation.ImageCapability{Supported: false, Reason: "local image translation requires Tesseract OCR in the application bin directory"}
	}
	return c.imageTextExtractor.Capability()
}

func (c *Client) ProviderName() string { return "local" }

func ParseChatResponse(statusCode int, body []byte) (translation.TranslateResult, error) {
	var decoded chatResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		if statusCode < 200 || statusCode >= 300 {
			return translation.TranslateResult{}, fmt.Errorf("the local model returned HTTP %d and an invalid response", statusCode)
		}
		return translation.TranslateResult{}, fmt.Errorf("the local model returned invalid JSON: %w", err)
	}
	if statusCode < 200 || statusCode >= 300 {
		message := strings.TrimSpace(decoded.Error.Message)
		if message == "" {
			message = http.StatusText(statusCode)
		}
		return translation.TranslateResult{}, fmt.Errorf("the local model returned HTTP %d: %s", statusCode, message)
	}
	if len(decoded.Choices) == 0 {
		return translation.TranslateResult{}, errors.New("the local model returned no translation choices")
	}
	result := translation.CleanResult(decoded.Choices[0].Message.Content)
	if result == "" {
		return translation.TranslateResult{}, errors.New("the local model returned an empty translation")
	}
	return translation.TranslateResult{Text: result}, nil
}
