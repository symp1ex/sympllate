//go:build windows

package window

import (
	"errors"
	"time"

	webview "github.com/jchv/go-webview2"
	"github.com/sympllate/translator/internal/app"
	"github.com/sympllate/translator/internal/clipboard"
	"github.com/sympllate/translator/internal/config"
	"github.com/sympllate/translator/internal/language"
	"github.com/sympllate/translator/internal/translation"
)

func bindMainSettings(w webview.WebView, mainWindow *MainWindow) error {
	bindings := []struct {
		name string
		fn   any
	}{
		{"GetInitialView", func() string { return mainWindow.currentView() }},
		{"GetSettingsConfig", func() (config.Config, error) { return config.Load(mainWindow.cfgPath) }},
		{"SaveSettingsConfig", func(cfg config.Config) error {
			if err := config.Save(mainWindow.cfgPath, cfg); err != nil {
				return err
			}
			if mainWindow.onRestart != nil {
				go func() {
					time.Sleep(200 * time.Millisecond)
					mainWindow.onRestart()
				}()
			}
			return nil
		}},
	}
	for _, binding := range bindings {
		if err := w.Bind(binding.name, binding.fn); err != nil {
			return errors.New("create binding " + binding.name + ": " + err.Error())
		}
	}
	return nil
}

type ClientConfig struct {
	DefaultLanguagePair      ClientLanguagePair `json:"defaultLanguagePair"`
	FallbackTargetLanguage   string             `json:"fallbackTargetLanguage"`
	MaxInputCharacters       int                `json:"maxInputCharacters"`
	MaxImageBytes            int                `json:"maxImageBytes"`
	MaxImageBase64Characters int                `json:"maxImageBase64Characters"`
}

type ClientLanguagePair struct {
	First  string `json:"first"`
	Second string `json:"second"`
}

func bindCommon(w webview.WebView, mode string, cfg config.Config, service *app.Service, clip *clipboard.Manager, popup *Popup) error {
	bindings := []struct {
		name string
		fn   any
	}{
		{"Translate", func(req translation.TranslateRequest) (string, error) { return service.StartTranslate(req) }},
		{"GetTranslation", func(id string) (app.JobStatus, error) { return service.Job(id) }},
		{"TranslateImage", func(req translation.ImageTranslateRequest) (string, error) { return service.StartImageTranslate(req) }},
		{"GetImageTranslation", func(id string) (app.ImageJobStatus, error) { return service.ImageJob(id) }},
		{"GetConfig", func() ClientConfig {
			return ClientConfig{
				ClientLanguagePair{cfg.DefaultLanguagePair.First.Active, cfg.DefaultLanguagePair.Second.Active},
				cfg.FallbackTargetLanguage.Active,
				cfg.Limits.MaxInputCharacters,
				translation.MaxImageBytes,
				translation.MaxImageBase64Characters,
			}
		}},
		{"GetSupportedLanguages", func() []language.Language { return language.Supported() }},
		{"GetWindowMode", func() string { return mode }},
		{"WindowMinimize", func() { minimizeWindow(w) }},
		{"WindowToggleMaximize", func() bool { return toggleWindowMaximized(w) }},
		{"WindowClose", func() {
			if mode == "popup" && popup != nil {
				popup.Hide()
				return
			}
			closeWindow(w)
		}},
		{"WindowDrag", func() { dragWindow(w) }},
		{"WindowResize", func(hitTest float64) { resizeWindow(w, uintptr(hitTest)) }},
		{"CopyText", func(text string) error {
			if text == "" {
				return errors.New("nothing to copy")
			}
			return clip.WriteText(text)
		}},
		{"HidePopup", func() {
			if popup != nil {
				popup.Hide()
			}
		}},
		{"SetQuickTranslationTarget", func(target string) error {
			if popup == nil {
				return errors.New("quick translation popup is unavailable")
			}
			return popup.ChangeQuickTranslationTarget(target)
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
			return errors.New("create binding " + binding.name + ": " + err.Error())
		}
	}
	return nil
}
