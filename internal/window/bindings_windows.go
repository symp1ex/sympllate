//go:build windows

package window

import (
	"errors"

	webview "github.com/jchv/go-webview2"
	"github.com/sympllate/translator/internal/app"
	"github.com/sympllate/translator/internal/clipboard"
	"github.com/sympllate/translator/internal/config"
	"github.com/sympllate/translator/internal/language"
	"github.com/sympllate/translator/internal/ollama"
)

type ClientConfig struct {
	DefaultLanguagePair    config.LanguagePair `json:"defaultLanguagePair"`
	FallbackTargetLanguage string              `json:"fallbackTargetLanguage"`
	MaxInputCharacters     int                 `json:"maxInputCharacters"`
}

func bindCommon(w webview.WebView, mode string, cfg config.Config, service *app.Service, clip *clipboard.Manager, popup *Popup) error {
	bindings := []struct {
		name string
		fn   any
	}{
		{"Translate", func(req ollama.TranslateRequest) (string, error) { return service.StartTranslate(req) }},
		{"GetTranslation", func(id string) (app.JobStatus, error) { return service.Job(id) }},
		{"GetConfig", func() ClientConfig {
			return ClientConfig{cfg.DefaultLanguagePair, cfg.FallbackTargetLanguage, cfg.Limits.MaxInputCharacters}
		}},
		{"GetSupportedLanguages", func() []language.Language { return language.Supported() }},
		{"GetWindowMode", func() string { return mode }},
		{"CopyText", func(text string) error {
			if text == "" {
				return errors.New("нечего копировать")
			}
			return clip.WriteText(text)
		}},
		{"HidePopup", func() {
			if popup != nil {
				popup.Hide()
			}
		}},
		{"GetPopupState", func() app.PopupState {
			if popup == nil {
				return app.PopupState{}
			}
			return popup.State()
		}},
	}
	for _, binding := range bindings {
		if err := w.Bind(binding.name, binding.fn); err != nil {
			return errors.New("создать binding " + binding.name + ": " + err.Error())
		}
	}
	return nil
}
