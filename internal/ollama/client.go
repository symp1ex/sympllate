package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/sympllate/translator/internal/config"
	"github.com/sympllate/translator/internal/translation"
)

type TranslateRequest = translation.TranslateRequest
type TranslateResult = translation.TranslateResult

type Client struct {
	endpoint           string
	model              string
	keepAlive          string
	numCtx             int
	numPredict         int
	temperature        float64
	maxInputCharacters int
	httpClient         *http.Client
	requestMu          sync.Mutex
}

func New(cfg config.OllamaConfig, maxInputCharacters int) (*Client, error) {
	base, err := url.Parse(strings.TrimRight(cfg.BaseURL, "/"))
	if err != nil || base.Host == "" || base.RawQuery != "" || base.Fragment != "" || (base.Scheme != "http" && base.Scheme != "https") {
		return nil, errors.New("invalid Ollama address")
	}
	return &Client{
		endpoint: base.String() + "/api/generate", model: cfg.Model, keepAlive: cfg.KeepAlive,
		numCtx: cfg.NumCtx, numPredict: cfg.NumPredict, temperature: cfg.Temperature,
		maxInputCharacters: maxInputCharacters, httpClient: &http.Client{Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second},
	}, nil
}

type generateRequest struct {
	Model     string          `json:"model"`
	Prompt    string          `json:"prompt"`
	Images    []string        `json:"images,omitempty"`
	Stream    bool            `json:"stream"`
	KeepAlive string          `json:"keep_alive,omitempty"`
	Options   generateOptions `json:"options"`
}

type generateOptions struct {
	Temperature float64 `json:"temperature"`
	NumCtx      int     `json:"num_ctx"`
	NumPredict  int     `json:"num_predict"`
}

type generateResponse struct {
	Response string `json:"response"`
	Error    string `json:"error,omitempty"`
}

func (c *Client) Translate(ctx context.Context, req TranslateRequest) (TranslateResult, error) {
	if err := translation.ValidateRequest(req, c.maxInputCharacters); err != nil {
		return TranslateResult{}, err
	}
	prompt, err := BuildPrompt(req.Text, req.Source, req.Target)
	if err != nil {
		return TranslateResult{}, err
	}
	text, err := c.Complete(ctx, prompt)
	if err != nil {
		return TranslateResult{}, err
	}
	return TranslateResult{Text: translation.CleanResultForSource(text, req.Text)}, nil
}

// Complete runs one raw TranslateGemma prompt. generate serializes all model
// traffic, including Ollama vision calls.
func (c *Client) Complete(ctx context.Context, prompt string) (string, error) {
	if strings.TrimSpace(prompt) == "" {
		return "", errors.New("model prompt is empty")
	}
	result, err := c.generate(ctx, generateRequest{Model: c.model, Prompt: prompt, Stream: false, KeepAlive: c.keepAlive, Options: c.options()}, false)
	if err != nil {
		return "", err
	}
	return result.Text, nil
}

func (c *Client) TranslateImage(ctx context.Context, req translation.ImageTranslateRequest) (translation.ImageTranslateResult, error) {
	validated, err := translation.ValidateImageRequest(req)
	if err != nil {
		return translation.ImageTranslateResult{}, err
	}
	prompt, err := translation.BuildImagePrompt(req.Source, req.Target)
	if err != nil {
		return translation.ImageTranslateResult{}, err
	}
	result, err := c.generate(ctx, generateRequest{
		Model: c.model, Prompt: prompt, Images: []string{validated.DataBase64}, Stream: false,
		KeepAlive: c.keepAlive, Options: c.options(),
	}, true)
	if err != nil {
		if isUnsupportedVisionError(err) {
			return translation.ImageTranslateResult{}, fmt.Errorf("the configured Ollama model %q does not support image input: %w", c.model, err)
		}
		return translation.ImageTranslateResult{}, err
	}
	return translation.ImageTranslateResult{Text: translation.NormalizeImageTranslation(result.Text)}, nil
}

func (c *Client) ImageCapability() translation.ImageCapability {
	return translation.ImageCapability{Supported: true}
}

func (c *Client) ProviderName() string { return "ollama" }

func (c *Client) options() generateOptions {
	return generateOptions{Temperature: c.temperature, NumCtx: c.numCtx, NumPredict: c.numPredict}
}

func (c *Client) generate(ctx context.Context, request generateRequest, allowEmpty bool) (TranslateResult, error) {
	c.requestMu.Lock()
	defer c.requestMu.Unlock()
	payload, err := json.Marshal(request)
	if err != nil {
		return TranslateResult{}, fmt.Errorf("marshal Ollama request: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return TranslateResult{}, fmt.Errorf("create Ollama request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return TranslateResult{}, errors.New("Ollama did not respond in time")
		}
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return TranslateResult{}, context.Canceled
		}
		return TranslateResult{}, fmt.Errorf("Ollama is unavailable at %s: %w", c.endpoint, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return TranslateResult{}, fmt.Errorf("read Ollama response: %w", err)
	}
	return parseResponse(response.StatusCode, body, allowEmpty)
}

func BuildPrompt(text, source, target string) (string, error) {
	return translation.BuildPrompt(text, source, target)
}

func ParseResponse(statusCode int, body []byte) (TranslateResult, error) {
	return parseResponse(statusCode, body, false)
}

func ParseImageResponse(statusCode int, body []byte) (translation.ImageTranslateResult, error) {
	result, err := parseResponse(statusCode, body, true)
	if err != nil {
		return translation.ImageTranslateResult{}, err
	}
	return translation.ImageTranslateResult{Text: translation.NormalizeImageTranslation(result.Text)}, nil
}

func parseResponse(statusCode int, body []byte, allowEmpty bool) (TranslateResult, error) {
	var decoded generateResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		if statusCode < 200 || statusCode >= 300 {
			return TranslateResult{}, fmt.Errorf("Ollama returned HTTP %d and an invalid response", statusCode)
		}
		return TranslateResult{}, fmt.Errorf("Ollama returned invalid JSON: %w", err)
	}
	if statusCode < 200 || statusCode >= 300 {
		message := strings.TrimSpace(decoded.Error)
		if message == "" {
			message = strings.TrimSpace(decoded.Response)
		}
		if message == "" {
			message = http.StatusText(statusCode)
		}
		return TranslateResult{}, fmt.Errorf("Ollama returned HTTP %d: %s", statusCode, message)
	}
	if decoded.Error != "" {
		return TranslateResult{}, errors.New(decoded.Error)
	}
	result := translation.CleanResult(decoded.Response)
	if result == "" && !allowEmpty {
		return TranslateResult{}, errors.New("Ollama returned an empty translation")
	}
	return TranslateResult{Text: result}, nil
}

func isUnsupportedVisionError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "does not support image") ||
		strings.Contains(message, "image input is not supported") ||
		strings.Contains(message, "does not support images")
}
