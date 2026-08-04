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
	if cfg.Ollama.Model != "m" || cfg.DefaultLanguagePair.Second != "en" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestLoadRejectsBrokenAndInvalidConfig(t *testing.T) {
	t.Parallel()
	for name, data := range map[string]string{
		"broken":   `{`,
		"unknown":  `{"unexpected":true}`,
		"invalid":  `{"ollama":{"baseUrl":"file:///tmp/x"}}`,
		"trailing": `{} {}`,
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
	if !strings.Contains(string(data), "translator-gemma") || cfg.Ollama.Model == "" {
		t.Fatal("default config not written")
	}
}
