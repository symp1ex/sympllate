package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
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

type OriginTarget struct {
	Window    uintptr
	Focus     uintptr
	ThreadID  uint32
	ProcessID uint32
}

type OriginTargets interface {
	Capture() (OriginTarget, error)
	Exists(target OriginTarget) bool
	Activate(ctx context.Context, target OriginTarget) error
}

type PopupState struct {
	Source           string `json:"source"`
	Target           string `json:"target"`
	DetectedLanguage string `json:"detectedLanguage,omitempty"`
	TranslatedText   string `json:"translatedText,omitempty"`
	Loading          bool   `json:"loading"`
	Error            string `json:"error,omitempty"`
}

type Popup interface {
	Show(state PopupState)
	Update(state PopupState)
	Hide()
}

type QuickTranslationHandler interface {
	ChangeQuickTranslationTarget(target string) error
	EndQuickTranslation()
}

type quickTranslationSession struct {
	id                uint64
	sourceText        string
	clipboard         ClipboardSnapshot
	origin            OriginTarget
	source            string
	target            string
	detectedLanguage  string
	translatedText    string
	translationTarget string
	loading           bool
	translationError  string
	err               string
	replacing         bool
	requestGeneration uint64
	cancelRequest     context.CancelFunc
}

type translationRequest struct {
	sessionID  uint64
	generation uint64
	ctx        context.Context
	cancel     context.CancelFunc
	request    ollama.TranslateRequest
}

type linkedReplacement struct {
	sessionID uint64
	text      string
	clipboard ClipboardSnapshot
	origin    OriginTarget
	source    string
	target    string
	chars     int
}

type HotkeyController struct {
	ctx         context.Context
	cfg         config.Config
	translator  Translator
	detector    language.Detector
	selection   SelectionIO
	targets     OriginTargets
	popup       Popup
	logger      *log.Logger
	showBusy    atomic.Bool
	replaceBusy atomic.Bool

	mu             sync.Mutex
	session        *quickTranslationSession
	nextSessionID  uint64
	showGeneration uint64
	closed         bool
	requests       sync.WaitGroup
}

func NewHotkeyController(ctx context.Context, cfg config.Config, translator Translator, detector language.Detector, selection SelectionIO, targets OriginTargets, popup Popup, logger *log.Logger) *HotkeyController {
	return &HotkeyController{ctx: ctx, cfg: cfg, translator: translator, detector: detector, selection: selection, targets: targets, popup: popup, logger: logger}
}

func (c *HotkeyController) ShowTranslation() {
	if !c.showBusy.CompareAndSwap(false, true) {
		c.logger.Printf("show hotkey ignored: previous selection capture is still running")
		return
	}
	defer c.showBusy.Store(false)

	showGeneration, ok := c.beginShow()
	if !ok {
		return
	}
	origin, err := c.targets.Capture()
	if err != nil {
		c.logger.Printf("show hotkey origin target error: %v", err)
		c.popup.Hide()
		return
	}
	text, previous, err := c.selection.CopySelection(c.ctx, c.clipboardWait())
	if !c.showIsCurrent(showGeneration) {
		return
	}
	if err != nil {
		c.logger.Printf("show hotkey clipboard error: %v", err)
		c.showFailedSession(showGeneration, origin, previous, err)
		return
	}

	direction := c.direction(text)
	c.mu.Lock()
	if c.closed || c.showGeneration != showGeneration {
		c.mu.Unlock()
		return
	}
	c.nextSessionID++
	session := &quickTranslationSession{
		id:               c.nextSessionID,
		sourceText:       text,
		clipboard:        previous,
		origin:           origin,
		source:           direction.Source,
		target:           direction.Target,
		detectedLanguage: direction.Detected,
	}
	c.session = session
	request, state := c.queueTranslationLocked(session)
	c.popup.Show(state)
	c.mu.Unlock()
	c.runTranslation(request)
}

func (c *HotkeyController) ReplaceSelection() {
	if !c.replaceBusy.CompareAndSwap(false, true) {
		c.failReplace(errors.New("замена по горячей клавише уже выполняется"))
		return
	}
	defer c.replaceBusy.Store(false)

	replacement, handled := c.prepareLinkedReplacement()
	if handled {
		if replacement != nil {
			c.replaceFromSession(*replacement)
		}
		return
	}
	c.replaceDirectly()
}

func (c *HotkeyController) ChangeQuickTranslationTarget(target string) error {
	if !supportedTarget(target) {
		return fmt.Errorf("неподдерживаемый целевой язык %q", target)
	}
	c.mu.Lock()
	session := c.session
	if c.closed || session == nil {
		c.mu.Unlock()
		return errors.New("активная сессия быстрого перевода отсутствует")
	}
	if session.replacing {
		c.mu.Unlock()
		return errors.New("замена выделения уже выполняется")
	}
	if !c.targets.Exists(session.origin) {
		err := errors.New("исходное окно больше недоступно; сессия быстрого перевода завершена")
		c.invalidateSessionLocked()
		c.mu.Unlock()
		c.logger.Printf("quick translation target change failed: %v", err)
		c.popup.Hide()
		return err
	}
	if session.target == target && (session.loading || session.translationTarget == target) {
		c.mu.Unlock()
		return nil
	}
	session.target = target
	request, state := c.queueTranslationLocked(session)
	c.popup.Update(state)
	c.mu.Unlock()
	c.runTranslation(request)
	return nil
}

func (c *HotkeyController) EndQuickTranslation() {
	c.mu.Lock()
	c.showGeneration++
	c.invalidateSessionLocked()
	c.mu.Unlock()
}

func (c *HotkeyController) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.showGeneration++
	c.invalidateSessionLocked()
	c.mu.Unlock()
	c.requests.Wait()
}

func (c *HotkeyController) beginShow() (uint64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, false
	}
	c.showGeneration++
	c.invalidateSessionLocked()
	return c.showGeneration, true
}

func (c *HotkeyController) showIsCurrent(generation uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.closed && c.showGeneration == generation
}

func (c *HotkeyController) showFailedSession(generation uint64, origin OriginTarget, previous ClipboardSnapshot, cause error) {
	c.mu.Lock()
	if c.closed || c.showGeneration != generation {
		c.mu.Unlock()
		return
	}
	c.nextSessionID++
	c.session = &quickTranslationSession{
		id:               c.nextSessionID,
		clipboard:        previous,
		origin:           origin,
		source:           c.cfg.DefaultLanguagePair.First,
		target:           c.cfg.DefaultLanguagePair.Second,
		translationError: cause.Error(),
		err:              cause.Error(),
	}
	state := c.popupStateLocked(c.session)
	c.popup.Show(state)
	c.mu.Unlock()
}

func (c *HotkeyController) queueTranslationLocked(session *quickTranslationSession) (translationRequest, PopupState) {
	if session.cancelRequest != nil {
		session.cancelRequest()
	}
	requestContext, cancel := context.WithCancel(c.ctx)
	session.cancelRequest = cancel
	session.requestGeneration++
	session.loading = true
	session.translationError = ""
	session.err = ""
	session.translatedText = ""
	session.translationTarget = ""
	request := translationRequest{
		sessionID:  session.id,
		generation: session.requestGeneration,
		ctx:        requestContext,
		cancel:     cancel,
		request:    ollama.TranslateRequest{Text: session.sourceText, Source: session.source, Target: session.target},
	}
	c.requests.Add(1)
	return request, c.popupStateLocked(session)
}

func (c *HotkeyController) runTranslation(request translationRequest) {
	go func() {
		defer c.requests.Done()
		defer request.cancel()
		started := time.Now()
		result, err := c.translator.Translate(request.ctx, request.request)
		if err == nil && strings.TrimSpace(result.Text) == "" {
			err = errors.New("получен пустой перевод")
		}

		c.mu.Lock()
		session := c.session
		if c.closed || session == nil || session.id != request.sessionID || session.requestGeneration != request.generation || session.target != request.request.Target {
			c.mu.Unlock()
			return
		}
		session.loading = false
		session.cancelRequest = nil
		if err != nil {
			session.translationError = err.Error()
			session.err = err.Error()
			session.translatedText = ""
			session.translationTarget = ""
		} else {
			session.translationError = ""
			session.err = ""
			session.translatedText = result.Text
			session.translationTarget = request.request.Target
			if result.DetectedLanguage != "" {
				session.detectedLanguage = result.DetectedLanguage
			}
		}
		state := c.popupStateLocked(session)
		c.popup.Update(state)
		c.mu.Unlock()

		if err != nil {
			c.logger.Printf("quick translation failed: source=%s target=%s chars=%d duration=%s error=%v", request.request.Source, request.request.Target, len([]rune(request.request.Text)), time.Since(started), err)
			return
		}
		c.logger.Printf("quick translation completed: source=%s target=%s chars=%d duration=%s", request.request.Source, request.request.Target, len([]rune(request.request.Text)), time.Since(started))
	}()
}

func (c *HotkeyController) prepareLinkedReplacement() (*linkedReplacement, bool) {
	c.mu.Lock()
	session := c.session
	if session == nil {
		showInProgress := c.showBusy.Load()
		c.mu.Unlock()
		if showInProgress {
			c.logger.Printf("linked replace rejected: quick translation selection capture is still running")
			return nil, true
		}
		return nil, false
	}
	if !c.targets.Exists(session.origin) {
		err := errors.New("исходное окно больше недоступно; сессия быстрого перевода завершена")
		session.err = err.Error()
		state := c.popupStateLocked(session)
		c.invalidateSessionLocked()
		c.popup.Update(state)
		c.mu.Unlock()
		c.logger.Printf("linked replace rejected: %v", err)
		c.popup.Hide()
		return nil, true
	}
	var err error
	switch {
	case session.replacing:
		err = errors.New("замена выделения уже выполняется")
	case session.loading:
		err = errors.New("перевод для выбранного языка ещё выполняется")
	case session.translationError != "":
		err = fmt.Errorf("последний перевод завершился ошибкой: %s", session.translationError)
	case strings.TrimSpace(session.translatedText) == "":
		err = errors.New("готовый перевод для замены отсутствует")
	case session.translationTarget != session.target:
		err = errors.New("готовый перевод относится к ранее выбранному языку")
	}
	if err != nil {
		session.err = err.Error()
		c.popup.Update(c.popupStateLocked(session))
		c.logger.Printf("linked replace rejected: %v", err)
		c.mu.Unlock()
		return nil, true
	}
	session.replacing = true
	replacement := &linkedReplacement{
		sessionID: session.id,
		text:      session.translatedText,
		clipboard: session.clipboard,
		origin:    session.origin,
		source:    session.source,
		target:    session.target,
		chars:     len([]rune(session.sourceText)),
	}
	c.mu.Unlock()
	return replacement, true
}

func (c *HotkeyController) replaceFromSession(replacement linkedReplacement) {
	if !c.targets.Exists(replacement.origin) {
		c.failLinkedReplacement(replacement.sessionID, errors.New("исходное окно больше недоступно; текст не заменён"), true)
		return
	}
	started := time.Now()
	if err := c.targets.Activate(c.ctx, replacement.origin); err != nil {
		c.failLinkedReplacement(replacement.sessionID, fmt.Errorf("не удалось вернуть фокус в исходное поле: %w", err), false)
		return
	}
	if !c.linkedReplacementIsCurrent(replacement.sessionID) {
		return
	}
	if err := c.selection.PasteText(c.ctx, replacement.text, replacement.clipboard); err != nil {
		c.failLinkedReplacement(replacement.sessionID, err, false)
		return
	}

	c.mu.Lock()
	if c.session != nil && c.session.id == replacement.sessionID {
		c.invalidateSessionLocked()
	}
	c.mu.Unlock()
	c.popup.Hide()
	c.logger.Printf("linked replace completed: source=%s target=%s chars=%d duration=%s", replacement.source, replacement.target, replacement.chars, time.Since(started))
}

func (c *HotkeyController) linkedReplacementIsCurrent(sessionID uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.closed && c.session != nil && c.session.id == sessionID && c.session.replacing
}

func (c *HotkeyController) failLinkedReplacement(sessionID uint64, err error, invalidate bool) {
	c.mu.Lock()
	session := c.session
	if session != nil && session.id == sessionID {
		session.replacing = false
		session.err = err.Error()
		state := c.popupStateLocked(session)
		if invalidate {
			c.invalidateSessionLocked()
		}
		c.popup.Update(state)
	}
	c.mu.Unlock()
	c.logger.Printf("linked replace failed: %v", err)
	if invalidate {
		c.popup.Hide()
	}
}

func (c *HotkeyController) replaceDirectly() {
	text, previous, err := c.selection.CopySelection(c.ctx, c.clipboardWait())
	if err != nil {
		c.failReplace(err)
		return
	}
	direction := c.direction(text)
	started := time.Now()
	result, err := c.translator.Translate(c.ctx, ollama.TranslateRequest{Text: text, Source: direction.Source, Target: direction.Target})
	if err != nil {
		c.failReplace(err)
		return
	}
	if strings.TrimSpace(result.Text) == "" {
		c.failReplace(errors.New("получен пустой перевод; исходный текст не изменён"))
		return
	}
	if err := c.selection.PasteText(c.ctx, result.Text, previous); err != nil {
		c.failReplace(err)
		return
	}
	c.logger.Printf("replace hotkey translation completed: source=%s target=%s chars=%d duration=%s", direction.Source, direction.Target, len([]rune(text)), time.Since(started))
}

func (c *HotkeyController) invalidateSessionLocked() {
	if c.session != nil && c.session.cancelRequest != nil {
		c.session.cancelRequest()
	}
	c.session = nil
}

func (c *HotkeyController) popupStateLocked(session *quickTranslationSession) PopupState {
	return PopupState{
		Source:           session.source,
		Target:           session.target,
		DetectedLanguage: session.detectedLanguage,
		TranslatedText:   session.translatedText,
		Loading:          session.loading,
		Error:            session.err,
	}
}

func (c *HotkeyController) clipboardWait() time.Duration {
	return time.Duration(c.cfg.Limits.ClipboardWaitMilliseconds) * time.Millisecond
}

func (c *HotkeyController) direction(text string) language.Direction {
	return language.ChooseDirection(c.detector.Detect(text), c.cfg.DefaultLanguagePair.First, c.cfg.DefaultLanguagePair.Second, c.cfg.FallbackTargetLanguage)
}

func (c *HotkeyController) failReplace(err error) {
	c.logger.Printf("replace hotkey failed: %v", err)
	c.popup.Show(PopupState{Error: err.Error()})
}

func supportedTarget(target string) bool {
	if target == "auto" {
		return false
	}
	for _, candidate := range language.Supported() {
		if candidate.Code == target {
			return true
		}
	}
	return false
}
