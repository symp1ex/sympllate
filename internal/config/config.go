package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/sympllate/translator/internal/language"
)

type Config struct {
	Provider               SelectSetting    `json:"provider"`
	LocalModel             LocalModelConfig `json:"localModel,omitempty"`
	Ollama                 OllamaConfig     `json:"ollama"`
	Hotkeys                HotkeyConfig     `json:"hotkeys"`
	DefaultLanguagePair    LanguagePair     `json:"defaultLanguagePair"`
	FallbackTargetLanguage SelectSetting    `json:"fallbackTargetLanguage"`
	UI                     UIConfig         `json:"ui"`
	Limits                 LimitsConfig     `json:"limits"`
}

const (
	ProviderAuto   = "auto"
	ProviderOllama = "ollama"
	ProviderLocal  = "local"
)

type SelectSetting struct {
	Active string   `json:"active"`
	List   []string `json:"list"`
}

// UnmarshalJSON accepts the previous string representation so existing
// config.json files can still be opened and then saved in the select format.
func (s *SelectSetting) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return errors.New("empty select value")
	}
	if trimmed[0] == '"' {
		var active string
		if err := json.Unmarshal(trimmed, &active); err != nil {
			return err
		}
		s.Active = active
		return nil
	}
	if trimmed[0] != '{' {
		return errors.New("select setting must be a string or an object with active and list")
	}
	type selectSettingAlias SelectSetting
	next := selectSettingAlias(*s)
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&next); err != nil {
		return err
	}
	*s = SelectSetting(next)
	return nil
}

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
	First  SelectSetting `json:"first"`
	Second SelectSetting `json:"second"`
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
	languages := supportedTargetLanguages()
	return Config{
		Provider:               newSelectSetting(ProviderAuto, []string{ProviderAuto, ProviderOllama, ProviderLocal}),
		LocalModel:             LocalModelConfig{ModelFile: "translator.gguf", StartupTimeoutSeconds: 180, FitTargetMiB: 1024},
		Ollama:                 OllamaConfig{BaseURL: "http://127.0.0.1:11434", Model: "translategemma:latest", TimeoutSeconds: 120, KeepAlive: "10m", NumCtx: 2048, NumPredict: 1024, Temperature: 0},
		Hotkeys:                HotkeyConfig{ShowTranslation: "Ctrl+Win+X", ReplaceSelection: "Ctrl+Win+R"},
		DefaultLanguagePair:    LanguagePair{First: newSelectSetting("ru", languages), Second: newSelectSetting("en", languages)},
		FallbackTargetLanguage: newSelectSetting("ru", languages),
		UI:                     UIConfig{MainWindowWidth: 900, MainWindowHeight: 620, PopupWidth: 520, PopupHeight: 360, AlwaysOnTopPopup: true},
		Limits:                 LimitsConfig{MaxInputCharacters: 12000, ClipboardWaitMilliseconds: 800},
	}
}

func newSelectSetting(active string, values []string) SelectSetting {
	return SelectSetting{Active: active, List: append([]string(nil), values...)}
}

func supportedTargetLanguages() []string {
	values := make([]string, 0, len(language.Supported()))
	for _, item := range language.Supported() {
		if item.Code != "auto" {
			values = append(values, item.Code)
		}
	}
	return values
}

func ExecutableDir() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("determine application path: %w", err)
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
		return Config{}, fmt.Errorf("read configuration %q: %w", path, err)
	}
	// Provider was introduced after the first config format. Its absence must
	// keep selecting Ollama instead of changing existing installations to auto.
	cfg := Default()
	cfg.Provider.Active = ProviderOllama
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("invalid config.json: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("invalid config.json: extra data follows the root object")
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
		return Config{}, false, fmt.Errorf("marshal configuration: %w", marshalErr)
	}
	data = append(data, '\n')
	if writeErr := os.WriteFile(path, data, 0o600); writeErr != nil {
		return Config{}, false, fmt.Errorf("create config.json next to the application: %w", writeErr)
	}
	return cfg, true, nil
}

func Save(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal configuration: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("save config.json: %w", err)
	}
	return nil
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
	if err := validateSelectSetting("provider", c.Provider); err != nil {
		return err
	}
	switch c.Provider.Active {
	case ProviderAuto, ProviderOllama, ProviderLocal:
	default:
		return errors.New("provider must be auto, ollama, or local")
	}
	for _, provider := range c.Provider.List {
		switch provider {
		case ProviderAuto, ProviderOllama, ProviderLocal:
		default:
			return fmt.Errorf("provider.list contains unknown value %q", provider)
		}
	}
	if c.Provider.Active == ProviderAuto || c.Provider.Active == ProviderLocal {
		if c.LocalModel.StartupTimeoutSeconds <= 0 {
			return errors.New("localModel.startupTimeoutSeconds must be greater than zero")
		}
		if c.LocalModel.FitTargetMiB <= 0 {
			return errors.New("localModel.fitTargetMiB must be greater than zero")
		}
	}
	parsed, err := url.ParseRequestURI(strings.TrimSpace(c.Ollama.BaseURL))
	if err != nil || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("ollama.baseUrl must be a valid HTTP(S) URL")
	}
	if strings.TrimSpace(c.Ollama.Model) == "" {
		return errors.New("ollama.model cannot be empty")
	}
	if c.Ollama.TimeoutSeconds <= 0 || c.Ollama.NumCtx <= 0 || c.Ollama.NumPredict <= 0 {
		return errors.New("timeoutSeconds, numCtx, and numPredict must be greater than zero")
	}
	if c.Ollama.Temperature < 0 {
		return errors.New("ollama.temperature cannot be negative")
	}
	if strings.TrimSpace(c.Hotkeys.ShowTranslation) == "" || strings.TrimSpace(c.Hotkeys.ReplaceSelection) == "" {
		return errors.New("both global hotkeys are required")
	}
	if err := validateLanguageSetting("defaultLanguagePair.first", c.DefaultLanguagePair.First); err != nil {
		return err
	}
	if err := validateLanguageSetting("defaultLanguagePair.second", c.DefaultLanguagePair.Second); err != nil {
		return err
	}
	if c.DefaultLanguagePair.First.Active == c.DefaultLanguagePair.Second.Active {
		return errors.New("defaultLanguagePair must contain two different languages")
	}
	if err := validateLanguageSetting("fallbackTargetLanguage", c.FallbackTargetLanguage); err != nil {
		return err
	}
	if c.UI.MainWindowWidth < 476 || c.UI.MainWindowHeight < 561 || c.UI.PopupWidth < 320 || c.UI.PopupHeight < 240 {
		return errors.New("window dimensions are too small")
	}
	if c.Limits.MaxInputCharacters <= 0 || c.Limits.ClipboardWaitMilliseconds <= 0 {
		return errors.New("limits must be greater than zero")
	}
	return nil
}

func validateSelectSetting(name string, setting SelectSetting) error {
	if strings.TrimSpace(setting.Active) == "" {
		return fmt.Errorf("%s.active cannot be empty", name)
	}
	if len(setting.List) == 0 {
		return fmt.Errorf("%s.list cannot be empty", name)
	}
	for _, option := range setting.List {
		if option == setting.Active {
			return nil
		}
	}
	return fmt.Errorf("%s.active must be present in list", name)
}

func validateLanguageSetting(name string, setting SelectSetting) error {
	if err := validateSelectSetting(name, setting); err != nil {
		return err
	}
	if !validLanguageCode(setting.Active) || setting.Active == "auto" {
		return fmt.Errorf("%s.active must contain a supported language code", name)
	}
	for _, code := range setting.List {
		if !validLanguageCode(code) || code == "auto" {
			return fmt.Errorf("%s.list contains invalid language %q", name, code)
		}
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
