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
	return NewHotkeyController(context.Background(), cfg, translator, language.SimpleDetector{}, selection, targets, popup, log.New(io.Discard, "", 0))
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
