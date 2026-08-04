package app

import (
	"context"
	"errors"
	"log"
	"sync/atomic"
	"time"

	"github.com/sympllate/translator/internal/config"
	"github.com/sympllate/translator/internal/language"
	"github.com/sympllate/translator/internal/ollama"
)

type ClipboardSnapshot struct {
	Text    string
	HasText bool
}

type SelectionIO interface {
	CopySelection(ctx context.Context, wait time.Duration) (string, ClipboardSnapshot, error)
	PasteText(ctx context.Context, text string, previous ClipboardSnapshot) error
}

type PopupState struct {
	Source           string `json:"source"`
	Target           string `json:"target"`
	DetectedLanguage string `json:"detectedLanguage,omitempty"`
	SourceText       string `json:"sourceText"`
	TranslatedText   string `json:"translatedText,omitempty"`
	Loading          bool   `json:"loading"`
	Error            string `json:"error,omitempty"`
}

type Popup interface {
	Show(state PopupState)
	Update(state PopupState)
}

type HotkeyController struct {
	ctx         context.Context
	cfg         config.Config
	service     *Service
	detector    language.Detector
	selection   SelectionIO
	popup       Popup
	logger      *log.Logger
	showBusy    atomic.Bool
	replaceBusy atomic.Bool
}

func NewHotkeyController(ctx context.Context, cfg config.Config, service *Service, detector language.Detector, selection SelectionIO, popup Popup, logger *log.Logger) *HotkeyController {
	return &HotkeyController{ctx: ctx, cfg: cfg, service: service, detector: detector, selection: selection, popup: popup, logger: logger}
}

func (c *HotkeyController) ShowTranslation() {
	if !c.showBusy.CompareAndSwap(false, true) {
		c.popup.Show(PopupState{Error: "Перевод по горячей клавише уже выполняется"})
		return
	}
	defer c.showBusy.Store(false)
	text, _, err := c.selection.CopySelection(c.ctx, time.Duration(c.cfg.Limits.ClipboardWaitMilliseconds)*time.Millisecond)
	if err != nil {
		c.logger.Printf("show hotkey clipboard error: %v", err)
		c.popup.Show(PopupState{Error: err.Error()})
		return
	}
	direction := c.direction(text)
	state := PopupState{Source: direction.Source, Target: direction.Target, DetectedLanguage: direction.Detected, SourceText: text, Loading: true}
	c.popup.Show(state)
	started := time.Now()
	result, err := c.service.Translate(c.ctx, ollama.TranslateRequest{Text: text, Source: direction.Source, Target: direction.Target})
	state.Loading = false
	if err != nil {
		state.Error = err.Error()
		c.logger.Printf("show hotkey translation failed: source=%s target=%s chars=%d duration=%s error=%v", direction.Source, direction.Target, len([]rune(text)), time.Since(started), err)
	} else {
		state.TranslatedText = result.Text
		c.logger.Printf("show hotkey translation completed: source=%s target=%s chars=%d duration=%s", direction.Source, direction.Target, len([]rune(text)), time.Since(started))
	}
	c.popup.Update(state)
}

func (c *HotkeyController) ReplaceSelection() {
	if !c.replaceBusy.CompareAndSwap(false, true) {
		c.popup.Show(PopupState{Error: "Замена по горячей клавише уже выполняется"})
		return
	}
	defer c.replaceBusy.Store(false)
	text, previous, err := c.selection.CopySelection(c.ctx, time.Duration(c.cfg.Limits.ClipboardWaitMilliseconds)*time.Millisecond)
	if err != nil {
		c.failReplace(err)
		return
	}
	direction := c.direction(text)
	started := time.Now()
	result, err := c.service.Translate(c.ctx, ollama.TranslateRequest{Text: text, Source: direction.Source, Target: direction.Target})
	if err != nil {
		c.failReplace(err)
		return
	}
	if result.Text == "" {
		c.failReplace(errors.New("получен пустой перевод; исходный текст не изменён"))
		return
	}
	if err := c.selection.PasteText(c.ctx, result.Text, previous); err != nil {
		c.failReplace(err)
		return
	}
	c.logger.Printf("replace hotkey translation completed: source=%s target=%s chars=%d duration=%s", direction.Source, direction.Target, len([]rune(text)), time.Since(started))
}

func (c *HotkeyController) direction(text string) language.Direction {
	return language.ChooseDirection(c.detector.Detect(text), c.cfg.DefaultLanguagePair.First, c.cfg.DefaultLanguagePair.Second, c.cfg.FallbackTargetLanguage)
}

func (c *HotkeyController) failReplace(err error) {
	c.logger.Printf("replace hotkey failed: %v", err)
	c.popup.Show(PopupState{Error: err.Error()})
}
