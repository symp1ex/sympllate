package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Provider               string           `json:"provider,omitempty"`
	LocalModel             LocalModelConfig `json:"localModel,omitempty"`
	Ollama                 OllamaConfig     `json:"ollama"`
	Hotkeys                HotkeyConfig     `json:"hotkeys"`
	DefaultLanguagePair    LanguagePair     `json:"defaultLanguagePair"`
	FallbackTargetLanguage string           `json:"fallbackTargetLanguage"`
	UI                     UIConfig         `json:"ui"`
	Limits                 LimitsConfig     `json:"limits"`
}

const (
	ProviderAuto   = "auto"
	ProviderOllama = "ollama"
	ProviderLocal  = "local"
)

type LocalModelConfig struct {
	ModelFile             string `json:"modelFile"`
	StartupTimeoutSeconds int    `json:"startupTimeoutSeconds"`
	FitTargetMiB          int    `json:"fitTargetMiB"`
}

type OllamaConfig struct {
	BaseURL        string  `json:"baseUrl"`
	Model          string  `json:"model"`
	TimeoutSeconds int     `json:"timeoutSeconds"`
	KeepAlive      string  `json:"keepAlive"`
	NumCtx         int     `json:"numCtx"`
	NumPredict     int     `json:"numPredict"`
	Temperature    float64 `json:"temperature"`
}

type HotkeyConfig struct {
	ShowTranslation  string `json:"showTranslation"`
	ReplaceSelection string `json:"replaceSelection"`
}

type LanguagePair struct {
	First  string `json:"first"`
	Second string `json:"second"`
}

type UIConfig struct {
	MainWindowWidth  int  `json:"mainWindowWidth"`
	MainWindowHeight int  `json:"mainWindowHeight"`
	PopupWidth       int  `json:"popupWidth"`
	PopupHeight      int  `json:"popupHeight"`
	AlwaysOnTopPopup bool `json:"alwaysOnTopPopup"`
}

type LimitsConfig struct {
	MaxInputCharacters        int `json:"maxInputCharacters"`
	ClipboardWaitMilliseconds int `json:"clipboardWaitMilliseconds"`
}

func Default() Config {
	return Config{
		Provider:               ProviderAuto,
		LocalModel:             LocalModelConfig{StartupTimeoutSeconds: 180, FitTargetMiB: 1024},
		Ollama:                 OllamaConfig{BaseURL: "http://127.0.0.1:11434", Model: "translator-gemma", TimeoutSeconds: 120, KeepAlive: "10m", NumCtx: 2048, NumPredict: 1024, Temperature: 0},
		Hotkeys:                HotkeyConfig{ShowTranslation: "Ctrl+Alt+T", ReplaceSelection: "Ctrl+Alt+R"},
		DefaultLanguagePair:    LanguagePair{First: "ru", Second: "en"},
		FallbackTargetLanguage: "ru",
		UI:                     UIConfig{MainWindowWidth: 900, MainWindowHeight: 620, PopupWidth: 520, PopupHeight: 360, AlwaysOnTopPopup: true},
		Limits:                 LimitsConfig{MaxInputCharacters: 12000, ClipboardWaitMilliseconds: 800},
	}
}

func ExecutableDir() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("определить путь приложения: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err == nil {
		executable = resolved
	}
	return filepath.Dir(executable), nil
}

func ExecutablePath() (string, error) {
	directory, err := ExecutableDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "config.json"), nil
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("прочитать конфигурацию %q: %w", path, err)
	}
	// Provider was introduced after the first config format. Its absence must
	// keep selecting Ollama instead of changing existing installations to auto.
	cfg := Config{Provider: ProviderOllama}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("повреждён config.json: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("повреждён config.json: после корневого объекта есть лишние данные")
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func LoadOrCreate(path string) (Config, bool, error) {
	cfg, err := Load(path)
	if err == nil {
		return cfg, false, nil
	}
	if !errors.Is(unwrapPathError(err), os.ErrNotExist) {
		return Config{}, false, err
	}
	cfg = Default()
	data, marshalErr := json.MarshalIndent(cfg, "", "  ")
	if marshalErr != nil {
		return Config{}, false, fmt.Errorf("сформировать конфигурацию: %w", marshalErr)
	}
	data = append(data, '\n')
	if writeErr := os.WriteFile(path, data, 0o600); writeErr != nil {
		return Config{}, false, fmt.Errorf("создать config.json рядом с приложением: %w", writeErr)
	}
	return cfg, true, nil
}

func unwrapPathError(err error) error {
	for err != nil {
		var pathErr *os.PathError
		if errors.As(err, &pathErr) {
			return pathErr.Err
		}
		return err
	}
	return nil
}

func (c Config) Validate() error {
	switch c.Provider {
	case ProviderAuto, ProviderOllama, ProviderLocal:
	default:
		return errors.New("provider должен быть auto, ollama или local")
	}
	if c.Provider == ProviderAuto || c.Provider == ProviderLocal {
		if c.LocalModel.StartupTimeoutSeconds <= 0 {
			return errors.New("localModel.startupTimeoutSeconds должен быть больше нуля")
		}
		if c.LocalModel.FitTargetMiB <= 0 {
			return errors.New("localModel.fitTargetMiB должен быть больше нуля")
		}
	}
	parsed, err := url.ParseRequestURI(strings.TrimSpace(c.Ollama.BaseURL))
	if err != nil || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("ollama.baseUrl должен быть корректным HTTP(S) URL")
	}
	if strings.TrimSpace(c.Ollama.Model) == "" {
		return errors.New("ollama.model не может быть пустым")
	}
	if c.Ollama.TimeoutSeconds <= 0 || c.Ollama.NumCtx <= 0 || c.Ollama.NumPredict <= 0 {
		return errors.New("timeoutSeconds, numCtx и numPredict должны быть больше нуля")
	}
	if c.Ollama.Temperature < 0 {
		return errors.New("ollama.temperature не может быть отрицательной")
	}
	if strings.TrimSpace(c.Hotkeys.ShowTranslation) == "" || strings.TrimSpace(c.Hotkeys.ReplaceSelection) == "" {
		return errors.New("обе глобальные горячие клавиши обязательны")
	}
	if !validLanguageCode(c.DefaultLanguagePair.First) || !validLanguageCode(c.DefaultLanguagePair.Second) || c.DefaultLanguagePair.First == "auto" || c.DefaultLanguagePair.Second == "auto" || c.DefaultLanguagePair.First == c.DefaultLanguagePair.Second {
		return errors.New("defaultLanguagePair должна содержать два разных языка")
	}
	if !validLanguageCode(c.FallbackTargetLanguage) || c.FallbackTargetLanguage == "auto" {
		return errors.New("fallbackTargetLanguage не может быть пустым")
	}
	if c.UI.MainWindowWidth < 400 || c.UI.MainWindowHeight < 300 || c.UI.PopupWidth < 320 || c.UI.PopupHeight < 240 {
		return errors.New("размеры окон слишком малы")
	}
	if c.Limits.MaxInputCharacters <= 0 || c.Limits.ClipboardWaitMilliseconds <= 0 {
		return errors.New("limits должны быть больше нуля")
	}
	return nil
}

func validLanguageCode(value string) bool {
	if len(value) < 1 || len(value) > 20 {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		letter := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
		if index == 0 && !letter {
			return false
		}
		if index > 0 && !letter && !(character >= '0' && character <= '9') && character != '-' {
			return false
		}
	}
	return true
}
