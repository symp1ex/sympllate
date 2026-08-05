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
	payload, err := json.Marshal(chatRequest{
		Model: c.model, Messages: []chatMessage{{Role: "user", Content: prompt}}, Stream: false,
		MaxTokens: c.numPredict, Temperature: c.temperature,
	})
	if err != nil {
		return translation.TranslateResult{}, fmt.Errorf("сформировать локальный запрос: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return translation.TranslateResult{}, fmt.Errorf("создать локальный запрос: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Authorization", "Bearer "+c.apiKey)
	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return translation.TranslateResult{}, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return translation.TranslateResult{}, errors.New("локальная модель не ответила вовремя")
		}
		return translation.TranslateResult{}, fmt.Errorf("локальная модель недоступна: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return translation.TranslateResult{}, fmt.Errorf("прочитать ответ локальной модели: %w", err)
	}
	if len(body) > maxResponseBytes {
		return translation.TranslateResult{}, errors.New("ответ локальной модели слишком большой")
	}
	return ParseChatResponse(response.StatusCode, body)
}

func ParseChatResponse(statusCode int, body []byte) (translation.TranslateResult, error) {
	var decoded chatResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		if statusCode < 200 || statusCode >= 300 {
			return translation.TranslateResult{}, fmt.Errorf("локальная модель вернула HTTP %d и некорректный ответ", statusCode)
		}
		return translation.TranslateResult{}, fmt.Errorf("локальная модель вернула некорректный JSON: %w", err)
	}
	if statusCode < 200 || statusCode >= 300 {
		message := strings.TrimSpace(decoded.Error.Message)
		if message == "" {
			message = http.StatusText(statusCode)
		}
		return translation.TranslateResult{}, fmt.Errorf("локальная модель вернула HTTP %d: %s", statusCode, message)
	}
	if len(decoded.Choices) == 0 {
		return translation.TranslateResult{}, errors.New("локальная модель вернула ответ без вариантов перевода")
	}
	result := translation.CleanResult(decoded.Choices[0].Message.Content)
	if result == "" {
		return translation.TranslateResult{}, errors.New("локальная модель вернула пустой перевод")
	}
	return translation.TranslateResult{Text: result}, nil
}
