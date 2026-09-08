package app

import (
	"context"
	"errors"
	"io"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/sympllate/translator/internal/config"
	"github.com/sympllate/translator/internal/language"
	"github.com/sympllate/translator/internal/translation"
)

type copyResult struct {
	text     string
	snapshot ClipboardSnapshot
	err      error
}

type fakeSelection struct {
	mu          sync.Mutex
	copies      []copyResult
	copyCalls   int
	pastes      []string
	pasteSnaps  []ClipboardSnapshot
	pasteErr    error
	beforePaste func()
}

func (f *fakeSelection) CopySelection(context.Context, time.Duration) (string, ClipboardSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.copyCalls++
	if len(f.copies) == 0 {
		return "", ClipboardSnapshot{}, errors.New("unexpected CopySelection")
	}
	result := f.copies[0]
	f.copies = f.copies[1:]
	return result.text, result.snapshot, result.err
}

func (f *fakeSelection) PasteText(_ context.Context, text string, snapshot ClipboardSnapshot) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.beforePaste != nil {
		f.beforePaste()
	}
	if f.pasteErr != nil {
		return f.pasteErr
	}
	f.pastes = append(f.pastes, text)
	f.pasteSnaps = append(f.pasteSnaps, snapshot)
	return nil
}

func (f *fakeSelection) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.copyCalls, len(f.pastes)
}

type translatorFunc func(context.Context, translation.TranslateRequest) (translation.TranslateResult, error)

func (f translatorFunc) Translate(ctx context.Context, request translation.TranslateRequest) (translation.TranslateResult, error) {
	return f(ctx, request)
}

type completerFunc func(context.Context, string) (string, error)

func (f completerFunc) Complete(ctx context.Context, prompt string) (string, error) {
	return f(ctx, prompt)
}

type pendingTranslation struct {
	ctx      context.Context
	request  translation.TranslateRequest
	response chan translationResponse
}

type translationResponse struct {
	result translation.TranslateResult
	err    error
}

type pendingTranslator struct {
	calls              chan *pendingTranslation
	ignoreCancellation bool
}

func newPendingTranslator(ignoreCancellation bool) *pendingTranslator {
	return &pendingTranslator{calls: make(chan *pendingTranslation, 10), ignoreCancellation: ignoreCancellation}
}

func (f *pendingTranslator) Translate(ctx context.Context, request translation.TranslateRequest) (translation.TranslateResult, error) {
	call := &pendingTranslation{ctx: ctx, request: request, response: make(chan translationResponse, 1)}
	f.calls <- call
	if f.ignoreCancellation {
		response := <-call.response
		return response.result, response.err
	}
	select {
	case response := <-call.response:
		return response.result, response.err
	case <-ctx.Done():
		return translation.TranslateResult{}, ctx.Err()
	}
}

type fakePopup struct {
	mu      sync.Mutex
	shown   []PopupState
	updated []PopupState
	hides   int
}

func (f *fakePopup) Show(state PopupState) {
	f.mu.Lock()
	f.shown = append(f.shown, state)
	f.mu.Unlock()
}

func (f *fakePopup) Update(state PopupState) {
	f.mu.Lock()
	f.updated = append(f.updated, state)
	f.mu.Unlock()
}

func (f *fakePopup) Hide() {
	f.mu.Lock()
	f.hides++
	f.mu.Unlock()
}

func (f *fakePopup) lastState() PopupState {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.updated) != 0 {
		return f.updated[len(f.updated)-1]
	}
	if len(f.shown) != 0 {
		return f.shown[len(f.shown)-1]
	}
	return PopupState{}
}

type fakeTargets struct {
	mu            sync.Mutex
	target        OriginTarget
	captureErr    error
	exists        bool
	activateErr   error
	activateCalls int
	activated     bool
}

func (f *fakeTargets) Capture() (OriginTarget, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.target, f.captureErr
}

func (f *fakeTargets) Exists(OriginTarget) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.exists
}

func (f *fakeTargets) Activate(context.Context, OriginTarget) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.activateCalls++
	if f.activateErr == nil {
		f.activated = true
	}
	return f.activateErr
}

func testController(translator Translator, selection *fakeSelection, targets *fakeTargets, popup *fakePopup) *HotkeyController {
	cfg := config.Default()
	identifier := testIdentifier(language.Detection{Language: "ru", Reliable: true})
	return NewHotkeyController(context.Background(), cfg, translator, nil, identifier, selection, targets, popup, log.New(io.Discard, "", 0))
}

func waitTranslation(t *testing.T, translator *pendingTranslator) *pendingTranslation {
	t.Helper()
	select {
	case call := <-translator.calls:
		return call
	case <-time.After(time.Second):
		t.Fatal("translation was not started")
		return nil
	}
}

func respond(call *pendingTranslation, text string, err error) {
	call.response <- translationResponse{result: translation.TranslateResult{Text: text}, err: err}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition was not satisfied")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestReplaceSelectionWithoutSessionUsesDirectFlow(t *testing.T) {
	selection := &fakeSelection{copies: []copyResult{{text: "Привет", snapshot: ClipboardSnapshot{Text: "clipboard", HasText: true}}}}
	var requests []translation.TranslateRequest
	translator := translatorFunc(func(_ context.Context, request translation.TranslateRequest) (translation.TranslateResult, error) {
		requests = append(requests, request)
		return translation.TranslateResult{Text: "Hello"}, nil
	})
	targets := &fakeTargets{exists: true}
	controller := testController(translator, selection, targets, &fakePopup{})

	controller.ReplaceSelection()

	copyCalls, pasteCalls := selection.counts()
	if copyCalls != 1 || pasteCalls != 1 {
		t.Fatalf("CopySelection calls = %d, PasteText calls = %d", copyCalls, pasteCalls)
	}
	if len(requests) != 1 || requests[0].Source != "ru" || requests[0].Target != "en" {
		t.Fatalf("direct translation requests = %+v", requests)
	}
	if selection.pastes[0] != "Hello" {
		t.Fatalf("pasted text = %q", selection.pastes[0])
	}
}

func TestQuickTranslationReliableDetectionUsesChooseDirection(t *testing.T) {
	tests := []struct {
		name       string
		detected   string
		wantSource string
		wantTarget string
	}{
		{name: "first side", detected: "ru", wantSource: "ru", wantTarget: "en"},
		{name: "second side", detected: "en", wantSource: "en", wantTarget: "ru"},
		{name: "third language", detected: "de", wantSource: "de", wantTarget: "ru"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selection := &fakeSelection{copies: []copyResult{{text: "selection"}}}
			targets := &fakeTargets{target: OriginTarget{Window: 1}, exists: true}
			popup := &fakePopup{}
			var requests []translation.TranslateRequest
			translator := translatorFunc(func(_ context.Context, request translation.TranslateRequest) (translation.TranslateResult, error) {
				requests = append(requests, request)
				return translation.TranslateResult{Text: "translated"}, nil
			})
			completerCalls := 0
			completer := completerFunc(func(context.Context, string) (string, error) {
				completerCalls++
				return "", errors.New("unexpected fallback")
			})
			controller := NewHotkeyController(
				context.Background(), config.Default(), translator, completer,
				testIdentifier(language.Detection{Language: test.detected, Reliable: true}),
				selection, targets, popup, log.New(io.Discard, "", 0),
			)

			controller.ShowTranslation()
			controller.requests.Wait()
			if len(requests) != 1 || requests[0].Source != test.wantSource || requests[0].Target != test.wantTarget || completerCalls != 0 {
				t.Fatalf("requests = %+v, completer calls = %d", requests, completerCalls)
			}
			state := popup.lastState()
			if state.Source != test.wantSource || state.Target != test.wantTarget || state.DetectedLanguage != test.detected {
				t.Fatalf("popup state = %+v", state)
			}
		})
	}
}

func TestQuickTranslationUnreliableSupportedDetectionUsesAutoSource(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		detected   string
		wantTarget string
	}{
		{name: "first side", text: "ambiguous first language", detected: "ru", wantTarget: "en"},
		{name: "second side", text: "ambiguous second language", detected: "en", wantTarget: "ru"},
		{name: "Polish regression", text: "Wyhodząc", detected: "pl", wantTarget: "ru"},
		{name: "Han lining regression", text: "麂皮内衬: 麂皮内衬提供柔软且保护性的表面，防止手表出现划痕和损坏。", detected: "zh", wantTarget: "ru"},
		{name: "Han product regression", text: "男士女士新款绿色纸质翻盖式防尘耐用波浪纹手表收纳盒，带绒面革内衬，适合户外使用", detected: "zh", wantTarget: "ru"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selection := &fakeSelection{copies: []copyResult{{text: test.text}}}
			targets := &fakeTargets{target: OriginTarget{Window: 1}, exists: true}
			popup := &fakePopup{}
			var requests []translation.TranslateRequest
			translator := translatorFunc(func(_ context.Context, request translation.TranslateRequest) (translation.TranslateResult, error) {
				requests = append(requests, request)
				return translation.TranslateResult{Text: "translated"}, nil
			})
			completerCalls := 0
			completer := completerFunc(func(context.Context, string) (string, error) {
				completerCalls++
				return "", errors.New("unexpected fallback")
			})
			controller := NewHotkeyController(
				context.Background(), config.Default(), translator, completer,
				testIdentifier(language.Detection{Language: test.detected, Reliable: false}),
				selection, targets, popup, log.New(io.Discard, "", 0),
			)

			controller.ShowTranslation()
			controller.requests.Wait()
			if len(requests) != 1 || requests[0].Source != "auto" || requests[0].Target != test.wantTarget || completerCalls != 0 {
				t.Fatalf("requests = %+v, completer calls = %d", requests, completerCalls)
			}
			state := popup.lastState()
			if state.Source != test.detected || state.Target != test.wantTarget || state.DetectedLanguage != test.detected || state.TranslatedText != "translated" || state.Error != "" {
				t.Fatalf("popup state = %+v", state)
			}
		})
	}
}

func TestQuickTranslationClassifierFailureUsesDeterministicFallback(t *testing.T) {
	selection := &fakeSelection{copies: []copyResult{{text: "ambiguous selection"}}}
	popup := &fakePopup{}
	var requests []translation.TranslateRequest
	completerCalls := 0
	identifier := language.NewLanguageIdentifier(testClassifier{panics: true})
	controller := NewHotkeyController(
		context.Background(), config.Default(),
		translatorFunc(func(_ context.Context, request translation.TranslateRequest) (translation.TranslateResult, error) {
			requests = append(requests, request)
			return translation.TranslateResult{Text: "перевод"}, nil
		}),
		completerFunc(func(context.Context, string) (string, error) {
			completerCalls++
			return "", errors.New("unexpected fallback")
		}),
		identifier, selection, &fakeTargets{target: OriginTarget{Window: 1}, exists: true}, popup, log.New(io.Discard, "", 0),
	)

	controller.ShowTranslation()
	controller.requests.Wait()
	if len(requests) != 1 || requests[0].Source != "auto" || requests[0].Target != "ru" || completerCalls != 0 || popup.lastState().TranslatedText != "перевод" {
		t.Fatalf("requests = %+v, completer calls = %d, state = %+v", requests, completerCalls, popup.lastState())
	}
}

func TestQuickTranslationUnsupportedDetectionUsesDeterministicFallback(t *testing.T) {
	selection := &fakeSelection{copies: []copyResult{{text: "অস্পষ্ট নির্বাচন"}}}
	popup := &fakePopup{}
	var requests []translation.TranslateRequest
	completerCalls := 0
	controller := NewHotkeyController(
		context.Background(), config.Default(),
		translatorFunc(func(_ context.Context, request translation.TranslateRequest) (translation.TranslateResult, error) {
			requests = append(requests, request)
			return translation.TranslateResult{Text: "перевод"}, nil
		}),
		completerFunc(func(context.Context, string) (string, error) {
			completerCalls++
			return "", errors.New("unexpected fallback")
		}),
		testIdentifier(language.Detection{Language: "bn", Reliable: true}),
		selection, &fakeTargets{target: OriginTarget{Window: 1}, exists: true}, popup, log.New(io.Discard, "", 0),
	)

	controller.ShowTranslation()
	controller.requests.Wait()
	state := popup.lastState()
	if len(requests) != 1 || requests[0].Source != "auto" || requests[0].Target != "ru" || completerCalls != 0 || state.Source != "auto" || state.Target != "ru" || state.DetectedLanguage != "" {
		t.Fatalf("requests = %+v, completer calls = %d, state = %+v", requests, completerCalls, state)
	}
}

func TestDirectReplaceUnreliableDetectionUsesAutoSource(t *testing.T) {
	selection := &fakeSelection{copies: []copyResult{{text: "ambiguous", snapshot: ClipboardSnapshot{Text: "saved", HasText: true}}}}
	targets := &fakeTargets{exists: true}
	var requests []translation.TranslateRequest
	completerCalls := 0
	controller := NewHotkeyController(
		context.Background(), config.Default(),
		translatorFunc(func(_ context.Context, request translation.TranslateRequest) (translation.TranslateResult, error) {
			requests = append(requests, request)
			return translation.TranslateResult{Text: "перевод"}, nil
		}),
		completerFunc(func(context.Context, string) (string, error) {
			completerCalls++
			return "", errors.New("unexpected fallback")
		}),
		testIdentifier(language.Detection{Language: "de", Reliable: false}),
		selection, targets, &fakePopup{}, log.New(io.Discard, "", 0),
	)

	controller.ReplaceSelection()
	if len(requests) != 1 || requests[0].Source != "auto" || requests[0].Target != "ru" || completerCalls != 0 || len(selection.pastes) != 1 || selection.pastes[0] != "перевод" || selection.pasteSnaps[0].Text != "saved" {
		t.Fatalf("requests = %+v, completer calls = %d, pastes = %+v, snapshots = %+v", requests, completerCalls, selection.pastes, selection.pasteSnaps)
	}
}

func TestQuickTranslationUnreliableDetectionKeepsAutoSourceAfterTargetChange(t *testing.T) {
	selection := &fakeSelection{copies: []copyResult{{text: "Wyhodząc"}}}
	var requests []translation.TranslateRequest
	completerCalls := 0
	controller := NewHotkeyController(
		context.Background(), config.Default(),
		translatorFunc(func(_ context.Context, request translation.TranslateRequest) (translation.TranslateResult, error) {
			requests = append(requests, request)
			return translation.TranslateResult{Text: "translated"}, nil
		}),
		completerFunc(func(context.Context, string) (string, error) {
			completerCalls++
			return "", errors.New("unexpected fallback")
		}),
		testIdentifier(language.Detection{Language: "pl", Reliable: false}),
		selection, &fakeTargets{target: OriginTarget{Window: 1}, exists: true}, &fakePopup{}, log.New(io.Discard, "", 0),
	)

	controller.ShowTranslation()
	controller.requests.Wait()
	if err := controller.ChangeQuickTranslationTarget("de"); err != nil {
		t.Fatal(err)
	}
	controller.requests.Wait()
	if len(requests) != 2 || requests[0].Source != "auto" || requests[0].Target != "ru" || requests[1].Source != "auto" || requests[1].Target != "de" || completerCalls != 0 {
		t.Fatalf("requests = %+v, completer calls = %d", requests, completerCalls)
	}
}

func TestShowThenReplaceUsesSessionTranslationAndOrigin(t *testing.T) {
	snapshot := ClipboardSnapshot{Text: "saved", HasText: true}
	selection := &fakeSelection{copies: []copyResult{{text: "Привет", snapshot: snapshot}}}
	translator := newPendingTranslator(false)
	target := OriginTarget{Window: 10, Focus: 11, ThreadID: 12, ProcessID: 13}
	targets := &fakeTargets{target: target, exists: true}
	popup := &fakePopup{}
	controller := testController(translator, selection, targets, popup)

	controller.ShowTranslation()
	call := waitTranslation(t, translator)
	respond(call, "Hello", nil)
	waitFor(t, func() bool { return popup.lastState().TranslatedText == "Hello" })

	controller.mu.Lock()
	session := *controller.session
	controller.mu.Unlock()
	if session.sourceText != "Привет" || session.clipboard != snapshot || session.origin != target {
		t.Fatalf("session did not preserve selection context: %+v", session)
	}
	controller.ReplaceSelection()

	copyCalls, pasteCalls := selection.counts()
	if copyCalls != 1 || pasteCalls != 1 {
		t.Fatalf("linked flow called CopySelection %d times and PasteText %d times", copyCalls, pasteCalls)
	}
	select {
	case extra := <-translator.calls:
		t.Fatalf("linked replace started another translation: %+v", extra.request)
	default:
	}
	if selection.pastes[0] != "Hello" || selection.pasteSnaps[0] != snapshot {
		t.Fatalf("linked paste = %q, snapshot = %+v", selection.pastes[0], selection.pasteSnaps[0])
	}
	if targets.activateCalls != 1 || !targets.activated {
		t.Fatalf("origin activation calls = %d, activated = %v", targets.activateCalls, targets.activated)
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.session != nil {
		t.Fatal("successful linked replace did not clear session")
	}
}

func TestTargetChangeAutomaticallyRetranslatesAndFeedsReplace(t *testing.T) {
	selection := &fakeSelection{copies: []copyResult{{text: "Привет", snapshot: ClipboardSnapshot{Text: "saved", HasText: true}}}}
	translator := newPendingTranslator(false)
	targets := &fakeTargets{target: OriginTarget{Window: 1, ThreadID: 2, ProcessID: 3}, exists: true}
	popup := &fakePopup{}
	controller := testController(translator, selection, targets, popup)
	controller.ShowTranslation()
	initial := waitTranslation(t, translator)
	respond(initial, "Hello", nil)
	waitFor(t, func() bool { return popup.lastState().TranslatedText == "Hello" })

	if err := controller.ChangeQuickTranslationTarget("de"); err != nil {
		t.Fatal(err)
	}
	changed := waitTranslation(t, translator)
	if changed.request.Target != "de" || changed.request.Text != "Привет" {
		t.Fatalf("automatic target request = %+v", changed.request)
	}
	if state := popup.lastState(); !state.Loading || state.TranslatedText != "" || state.Target != "de" {
		t.Fatalf("loading target state = %+v", state)
	}
	respond(changed, "Hallo", nil)
	waitFor(t, func() bool { return popup.lastState().TranslatedText == "Hallo" })
	controller.ReplaceSelection()
	if len(selection.pastes) != 1 || selection.pastes[0] != "Hallo" {
		t.Fatalf("pastes after target change = %+v", selection.pastes)
	}
}

func TestStaleTargetResponseCannotReplaceCurrentTranslation(t *testing.T) {
	selection := &fakeSelection{copies: []copyResult{{text: "Привет", snapshot: ClipboardSnapshot{}}}}
	translator := newPendingTranslator(true)
	targets := &fakeTargets{target: OriginTarget{Window: 1, ThreadID: 2, ProcessID: 3}, exists: true}
	popup := &fakePopup{}
	controller := testController(translator, selection, targets, popup)
	controller.ShowTranslation()
	oldCall := waitTranslation(t, translator)
	if err := controller.ChangeQuickTranslationTarget("de"); err != nil {
		t.Fatal(err)
	}
	newCall := waitTranslation(t, translator)
	respond(newCall, "Hallo", nil)
	waitFor(t, func() bool { return popup.lastState().TranslatedText == "Hallo" })
	respond(oldCall, "Hello", nil)
	controller.requests.Wait()

	if state := popup.lastState(); state.Target != "de" || state.TranslatedText != "Hallo" {
		t.Fatalf("stale response overwrote current state: %+v", state)
	}
	controller.ReplaceSelection()
	if len(selection.pastes) != 1 || selection.pastes[0] != "Hallo" {
		t.Fatalf("stale translation was pasted: %+v", selection.pastes)
	}
}

func TestLinkedReplaceRejectsLoadingErrorAndEmptyWithoutFallback(t *testing.T) {
	tests := []struct {
		name    string
		result  string
		err     error
		respond bool
	}{
		{name: "loading", respond: false},
		{name: "error", err: errors.New("offline"), respond: true},
		{name: "empty", result: "  ", respond: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selection := &fakeSelection{copies: []copyResult{{text: "Привет", snapshot: ClipboardSnapshot{Text: "saved", HasText: true}}}}
			translator := newPendingTranslator(false)
			targets := &fakeTargets{target: OriginTarget{Window: 1, ThreadID: 2, ProcessID: 3}, exists: true}
			controller := testController(translator, selection, targets, &fakePopup{})
			controller.ShowTranslation()
			call := waitTranslation(t, translator)
			if test.respond {
				respond(call, test.result, test.err)
				controller.requests.Wait()
			}

			controller.ReplaceSelection()
			copyCalls, pasteCalls := selection.counts()
			if copyCalls != 1 || pasteCalls != 0 {
				t.Fatalf("CopySelection calls = %d, PasteText calls = %d", copyCalls, pasteCalls)
			}
			select {
			case extra := <-translator.calls:
				t.Fatalf("direct fallback started translation: %+v", extra.request)
			default:
			}
			controller.Close()
		})
	}
}

func TestLinkedReplaceRequiresConfirmedLiveOrigin(t *testing.T) {
	tests := []struct {
		name          string
		exists        bool
		activationErr error
		wantActivate  int
	}{
		{name: "closed", exists: false},
		{name: "activation timeout", exists: true, activationErr: errors.New("timeout"), wantActivate: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			selection := &fakeSelection{copies: []copyResult{{text: "Привет", snapshot: ClipboardSnapshot{Text: "saved", HasText: true}}}}
			translator := translatorFunc(func(context.Context, translation.TranslateRequest) (translation.TranslateResult, error) {
				return translation.TranslateResult{Text: "Hello"}, nil
			})
			targets := &fakeTargets{target: OriginTarget{Window: 1, ThreadID: 2, ProcessID: 3}, exists: true}
			popup := &fakePopup{}
			controller := testController(translator, selection, targets, popup)
			controller.ShowTranslation()
			controller.requests.Wait()
			targets.mu.Lock()
			targets.exists = test.exists
			targets.activateErr = test.activationErr
			targets.mu.Unlock()

			controller.ReplaceSelection()
			_, pasteCalls := selection.counts()
			if pasteCalls != 0 || targets.activateCalls != test.wantActivate {
				t.Fatalf("PasteText calls = %d, Activate calls = %d", pasteCalls, targets.activateCalls)
			}
			if popup.lastState().Error == "" {
				t.Fatal("origin failure was not reported")
			}
		})
	}
}

func TestPasteStartsOnlyAfterOriginActivation(t *testing.T) {
	selection := &fakeSelection{copies: []copyResult{{text: "Привет"}}}
	targets := &fakeTargets{target: OriginTarget{Window: 1, ThreadID: 2, ProcessID: 3}, exists: true}
	selection.beforePaste = func() {
		targets.mu.Lock()
		defer targets.mu.Unlock()
		if !targets.activated {
			t.Fatal("PasteText started before origin activation was confirmed")
		}
	}
	controller := testController(translatorFunc(func(context.Context, translation.TranslateRequest) (translation.TranslateResult, error) {
		return translation.TranslateResult{Text: "Hello"}, nil
	}), selection, targets, &fakePopup{})
	controller.ShowTranslation()
	controller.requests.Wait()
	controller.ReplaceSelection()
}

func TestQuickTranslationSessionLifecycle(t *testing.T) {
	t.Run("new show replaces old", func(t *testing.T) {
		selection := &fakeSelection{copies: []copyResult{{text: "Первый"}, {text: "Второй"}}}
		translator := newPendingTranslator(false)
		targets := &fakeTargets{target: OriginTarget{Window: 1, ThreadID: 2, ProcessID: 3}, exists: true}
		controller := testController(translator, selection, targets, &fakePopup{})
		controller.ShowTranslation()
		first := waitTranslation(t, translator)
		controller.ShowTranslation()
		second := waitTranslation(t, translator)
		respond(second, "Second", nil)
		controller.requests.Wait()
		select {
		case <-first.ctx.Done():
		default:
			t.Fatal("new ShowTranslation did not cancel old request")
		}
		controller.mu.Lock()
		defer controller.mu.Unlock()
		if controller.session == nil || controller.session.sourceText != "Второй" || controller.session.translatedText != "Second" {
			t.Fatalf("replacement session = %+v", controller.session)
		}
	})

	t.Run("popup close clears session", func(t *testing.T) {
		selection := &fakeSelection{copies: []copyResult{{text: "Привет"}}}
		translator := newPendingTranslator(false)
		controller := testController(translator, selection, &fakeTargets{target: OriginTarget{Window: 1, ThreadID: 2, ProcessID: 3}, exists: true}, &fakePopup{})
		controller.ShowTranslation()
		call := waitTranslation(t, translator)
		controller.EndQuickTranslation()
		select {
		case <-call.ctx.Done():
		case <-time.After(time.Second):
			t.Fatal("popup close did not cancel translation")
		}
		controller.requests.Wait()
		controller.mu.Lock()
		defer controller.mu.Unlock()
		if controller.session != nil {
			t.Fatal("popup close did not clear session")
		}
	})

	t.Run("successful replace returns to direct flow", func(t *testing.T) {
		selection := &fakeSelection{copies: []copyResult{{text: "Привет"}, {text: "Пока"}}}
		var requests []translation.TranslateRequest
		translator := translatorFunc(func(_ context.Context, request translation.TranslateRequest) (translation.TranslateResult, error) {
			requests = append(requests, request)
			if request.Text == "Привет" {
				return translation.TranslateResult{Text: "Hello"}, nil
			}
			return translation.TranslateResult{Text: "Bye"}, nil
		})
		controller := testController(translator, selection, &fakeTargets{target: OriginTarget{Window: 1, ThreadID: 2, ProcessID: 3}, exists: true}, &fakePopup{})
		controller.ShowTranslation()
		controller.requests.Wait()
		controller.ReplaceSelection()
		controller.ReplaceSelection()
		copyCalls, pasteCalls := selection.counts()
		if copyCalls != 2 || pasteCalls != 2 || len(requests) != 2 || selection.pastes[1] != "Bye" {
			t.Fatalf("direct flow after session: copies=%d pastes=%d requests=%+v pasted=%+v", copyCalls, pasteCalls, requests, selection.pastes)
		}
	})

	t.Run("close cancels and waits for requests", func(t *testing.T) {
		selection := &fakeSelection{copies: []copyResult{{text: "Привет"}}}
		translator := newPendingTranslator(false)
		controller := testController(translator, selection, &fakeTargets{target: OriginTarget{Window: 1, ThreadID: 2, ProcessID: 3}, exists: true}, &fakePopup{})
		controller.ShowTranslation()
		call := waitTranslation(t, translator)
		closed := make(chan struct{})
		go func() { controller.Close(); close(closed) }()
		select {
		case <-call.ctx.Done():
		case <-time.After(time.Second):
			t.Fatal("Close did not cancel active request")
		}
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatal("Close did not wait for request termination")
		}
		controller.mu.Lock()
		defer controller.mu.Unlock()
		if controller.session != nil {
			t.Fatal("Close left an active session")
		}
	})
}
