package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadValidConfig(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.json")
	data := `{"ollama":{"baseUrl":"http://127.0.0.1:11434","model":"m","timeoutSeconds":10,"keepAlive":"10m","numCtx":2048,"numPredict":1024,"temperature":0},"hotkeys":{"showTranslation":"Ctrl+Alt+T","replaceSelection":"Ctrl+Alt+R"},"defaultLanguagePair":{"first":"ru","second":"en"},"fallbackTargetLanguage":"ru","ui":{"mainWindowWidth":900,"mainWindowHeight":620,"popupWidth":520,"popupHeight":360,"alwaysOnTopPopup":true},"limits":{"maxInputCharacters":12000,"clipboardWaitMilliseconds":800}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Ollama.Model != "m" || cfg.DefaultLanguagePair.Second.Active != "en" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.Provider.Active != ProviderOllama {
		t.Fatalf("legacy config provider = %q, want ollama", cfg.Provider.Active)
	}
	if cfg.OCRBackend.Active != OCRBackendTesseract {
		t.Fatalf("legacy config OCR backend = %q, want tesseract", cfg.OCRBackend.Active)
	}
	if len(cfg.Provider.List) == 0 || len(cfg.DefaultLanguagePair.First.List) == 0 {
		t.Fatal("legacy select values were not populated with default options")
	}
}

func TestValidateProviders(t *testing.T) {
	t.Parallel()
	for _, provider := range []string{ProviderAuto, ProviderOllama, ProviderLocal} {
		cfg := Default()
		cfg.Provider.Active = provider
		if err := cfg.Validate(); err != nil {
			t.Errorf("provider %q: %v", provider, err)
		}
	}
	cfg := Default()
	cfg.Provider.Active = "unexpected"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "provider") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestOCRBackendDefaultsAndValidation(t *testing.T) {
	t.Parallel()
	cfg := Default()
	if cfg.OCRBackend.Active != OCRBackendTesseract || strings.Join(cfg.OCRBackend.List, ",") != "tesseract,paddleocr" {
		t.Fatalf("OCR backend = %+v", cfg.OCRBackend)
	}
	cfg.OCRBackend.Active = "unknown"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "ocrBackend") {
		t.Fatalf("unexpected error: %v", err)
	}
	cfg = Default()
	cfg.OCRBackend.List = append(cfg.OCRBackend.List, "unknown")
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "ocrBackend.list") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDefaultEnablesUpdaterAndConfiguresLogs(t *testing.T) {
	t.Parallel()
	cfg := Default()
	if !cfg.Updater.Enabled {
		t.Fatal("default updater is disabled")
	}
	if cfg.Logs.LogLevel.Active != LogLevelWarning || cfg.Logs.StoreDays != 2 {
		t.Fatalf("default logs config = %+v", cfg.Logs)
	}
	wantLevels := []string{LogLevelDebug, LogLevelInfo, LogLevelWarning, LogLevelError}
	if strings.Join(cfg.Logs.LogLevel.List, ",") != strings.Join(wantLevels, ",") {
		t.Fatalf("default log levels = %v, want %v", cfg.Logs.LogLevel.List, wantLevels)
	}
	if cfg.ImageBatch.MinimumFontSize != 7 || cfg.ImageBatch.MaximumFontSize != 48 || cfg.ImageBatch.JPEGQuality != 92 {
		t.Fatalf("default image batch config = %+v", cfg.ImageBatch)
	}
}

func TestValidateImageBatchSettings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*Config)
	}{
		{"font range", func(cfg *Config) { cfg.ImageBatch.MinimumFontSize = 20; cfg.ImageBatch.MaximumFontSize = 10 }},
		{"line spacing", func(cfg *Config) { cfg.ImageBatch.LineSpacing = 0.5 }},
		{"jpeg quality", func(cfg *Config) { cfg.ImageBatch.JPEGQuality = 101 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := Default()
			test.change(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatalf("config=%+v", cfg.ImageBatch)
			}
		})
	}
}

func TestLoadLegacyLogLevelPopulatesSelectOptions(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.json")
	data := `{"logs":{"log_level":" ERROR ","store_days":2}}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Logs.LogLevel.Active != LogLevelError || len(cfg.Logs.LogLevel.List) != 4 {
		t.Fatalf("legacy log level = %+v", cfg.Logs.LogLevel)
	}
	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), `"log_level": {`) || !strings.Contains(string(saved), `"active": "error"`) {
		t.Fatalf("log level was not saved in select format: %s", saved)
	}
}

func TestValidateLogLevels(t *testing.T) {
	t.Parallel()
	for _, level := range []string{LogLevelDebug, LogLevelInfo, LogLevelWarning, LogLevelError} {
		cfg := Default()
		cfg.Logs.LogLevel.Active = level
		if err := cfg.Validate(); err != nil {
			t.Errorf("log level %q: %v", level, err)
		}
	}
	cfg := Default()
	cfg.Logs.LogLevel.Active = "unexpected"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "logs.log_level") {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidateMainWindowMinimumSize(t *testing.T) {
	t.Parallel()
	for _, size := range []struct{ width, height int }{{475, 561}, {476, 560}} {
		cfg := Default()
		cfg.UI.MainWindowWidth = size.width
		cfg.UI.MainWindowHeight = size.height
		if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "window dimensions") {
			t.Fatalf("Validate() for %dx%d error = %v, want window size error", size.width, size.height, err)
		}
	}
	cfg := Default()
	cfg.UI.MainWindowWidth = 476
	cfg.UI.MainWindowHeight = 561
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() for 476x561 error = %v", err)
	}
}

func TestLoadRejectsBrokenAndInvalidConfig(t *testing.T) {
	t.Parallel()
	for name, data := range map[string]string{
		"broken":         `{`,
		"unknown":        `{"unexpected":true}`,
		"unknown select": `{"provider":{"active":"auto","list":["auto"],"unexpected":true}}`,
		"invalid":        `{"ollama":{"baseUrl":"file:///tmp/x"}}`,
		"trailing":       `{} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("Load() expected error")
			}
		})
	}
}

func TestLoadOrCreate(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, created, err := LoadOrCreate(path)
	if err != nil || !created {
		t.Fatalf("LoadOrCreate() = created %v, err %v", created, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Ollama.Model == "" || !strings.Contains(string(data), cfg.Ollama.Model) || !strings.Contains(string(data), `"active": "auto"`) {
		t.Fatal("default config not written")
	}
}

func TestSaveAndLoadSelectSettings(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := Default()
	cfg.Provider.Active = ProviderOllama
	cfg.DefaultLanguagePair.First.Active = "de"
	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Provider.Active != ProviderOllama || loaded.DefaultLanguagePair.First.Active != "de" {
		t.Fatalf("unexpected round trip config: %+v", loaded)
	}
}

func TestSaveRejectsInvalidConfigBeforeWriting(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Default()
	cfg.Provider.Active = "unexpected"
	if err := Save(path, cfg); err == nil {
		t.Fatal("Save() expected validation error")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original" {
		t.Fatalf("invalid config overwrote file: %q", data)
	}
}
