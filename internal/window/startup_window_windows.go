//go:build windows

package window

import (
	"errors"
	"runtime"
	"sync"

	webview "github.com/jchv/go-webview2"
	"github.com/sympllate/translator/internal/logger"
)

const (
	startupWindowWidth  = 420
	startupWindowHeight = 190
)

type startupWindowLifecycle struct {
	mu         sync.Mutex
	finished   bool
	userClosed bool
}

func (l *startupWindowLifecycle) finishByUser() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.finished {
		return false
	}
	l.finished = true
	l.userClosed = true
	return true
}

func (l *startupWindowLifecycle) finishProgrammatically() {
	l.mu.Lock()
	l.finished = true
	l.mu.Unlock()
}

func (l *startupWindowLifecycle) wasClosedByUser() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.userClosed
}

// StartupWindow shows early startup feedback without depending on the main
// frontend bundle or application services.
type StartupWindow struct {
	onUserClose func()
	lifecycle   startupWindowLifecycle

	mu        sync.RWMutex
	w         webview.WebView
	closeUI   func()
	done      chan struct{}
	closeOnce sync.Once
}

func NewStartupWindow(onUserClose func()) *StartupWindow {
	return &StartupWindow{onUserClose: onUserClose}
}

// Start returns after the document has rendered and notified the Go binding.
func (s *StartupWindow) Start() error {
	ready := make(chan error, 1)
	s.mu.Lock()
	s.done = make(chan struct{})
	s.mu.Unlock()
	go s.run(ready)
	return <-ready
}

func (s *StartupWindow) run(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var readyOnce sync.Once
	signalReady := func(err error) {
		readyOnce.Do(func() { ready <- err })
	}
	defer func() {
		signalReady(errors.New("startup window closed before it was ready"))
		s.mu.RLock()
		done := s.done
		s.mu.RUnlock()
		close(done)
	}()

	w := webview.NewWithOptions(webview.WebViewOptions{
		AutoFocus: true,
		WindowOptions: webview.WindowOptions{
			Title:  "Sympllate",
			Width:  startupWindowWidth,
			Height: startupWindowHeight,
			Center: true,
		},
	})
	if w == nil {
		signalReady(errors.New("failed to create startup WebView2 window"))
		return
	}
	hwnd := uintptr(w.Window())
	if hwnd == 0 {
		w.Destroy()
		signalReady(errors.New("the startup WebView2 window did not provide an HWND"))
		return
	}
	showWindow.Call(hwnd, swHide)
	s.mu.Lock()
	s.w = w
	s.closeUI = func() {
		showWindow.Call(hwnd, swHide)
		w.Dispatch(func() {
			destroyNativeWindow.Call(hwnd)
		})
	}
	s.mu.Unlock()
	icons, err := setTaskbarIcon(hwnd)
	if err != nil {
		logger.Sympllate.Warnf("startup window taskbar icon was not set: %v", err)
	}
	defer func() {
		s.mu.Lock()
		s.w = nil
		s.closeUI = nil
		s.mu.Unlock()
		w.Destroy()
		icons.destroy()
	}()

	if err := w.Bind("StartupReady", func() {
		showWindow.Call(hwnd, swShow)
		setForegroundWindow.Call(hwnd)
		signalReady(nil)
	}); err != nil {
		signalReady(errors.New("create startup ready binding: " + err.Error()))
		return
	}
	if err := w.Bind("WindowClose", s.notifyUserClose); err != nil {
		signalReady(errors.New("create startup close binding: " + err.Error()))
		return
	}
	if err := w.Bind("WindowDrag", func() { dragWindow(w) }); err != nil {
		signalReady(errors.New("create startup drag binding: " + err.Error()))
		return
	}
	if err := applyFixedWindowChrome(w, 44, 48, s.notifyUserClose); err != nil {
		signalReady(err)
		return
	}
	w.SetHtml(startupWindowHTML)
	w.Run()
}

func (s *StartupWindow) notifyUserClose() {
	if !s.lifecycle.finishByUser() {
		return
	}
	if s.onUserClose != nil {
		s.onUserClose()
	}
	go s.Close()
}

func (s *StartupWindow) WasClosedByUser() bool {
	return s.lifecycle.wasClosedByUser()
}

// Close is idempotent and never turns a programmatic close into a user cancel.
func (s *StartupWindow) Close() {
	s.lifecycle.finishProgrammatically()
	s.closeOnce.Do(func() {
		s.mu.RLock()
		closeUI, done := s.closeUI, s.done
		s.mu.RUnlock()
		if closeUI != nil {
			closeUI()
		}
		if done != nil {
			<-done
		}
	})
}

const startupWindowHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>
:root { color-scheme:dark; --background:#11161a; --surface-container:#171d22; --outline-variant:#323c45; --on-surface:#e6ebef; --on-surface-variant:#aab4bd; --primary:#a9bac8; --error-container:#93000a; --on-error-container:#ffdad6; --state-rgb:230, 235, 239; --state-hover:0.08; --state-pressed:0.12; }
* { box-sizing:border-box; }
html, body { width:100%; height:100%; margin:0; overflow:hidden; font-family:"Segoe UI",system-ui,sans-serif; background:transparent; color:var(--on-surface); user-select:none; }
.window { height:100%; border:1px solid var(--outline-variant); background:var(--background); }
.titlebar { height:44px; display:flex; align-items:center; justify-content:space-between; border-bottom:1px solid var(--outline-variant); padding-left:16px; background:var(--surface-container); }
.title { font-size:14px; font-weight:650; letter-spacing:.02em; }
.close { width:48px; height:43px; display:inline-flex; align-items:center; justify-content:center; border:0; background:transparent; color:var(--on-surface-variant); cursor:pointer; box-shadow:var(--focus-shadow, none); }
.close svg { width:16px; height:16px; display:block; fill:none; stroke:currentColor; stroke-width:1.5; stroke-linecap:round; pointer-events:none; }
.close:hover { background:var(--error-container); color:var(--on-error-container); box-shadow:var(--focus-shadow, inset 0 0 0 0 transparent), inset 0 0 0 100px rgba(var(--state-rgb), var(--state-hover)); }
.close:active { box-shadow:var(--focus-shadow, inset 0 0 0 0 transparent), inset 0 0 0 100px rgba(var(--state-rgb), var(--state-pressed)); }
.close:focus-visible { --focus-shadow:inset 0 0 0 1px var(--primary); outline:none; }
.content { height:calc(100% - 44px); display:flex; align-items:center; gap:18px; margin:0; padding:22px 38px; background:var(--background); }
.spinner { width:34px; height:34px; flex:0 0 34px; border:3px solid var(--outline-variant); border-top-color:var(--primary); border-radius:50%; animation:spin .85s linear infinite; }
h1 { margin:0; font-size:18px; font-weight:650; }
p { margin:7px 0 0; color:var(--on-surface-variant); font-size:13px; line-height:1.45; }
@keyframes spin { to { transform:rotate(360deg); } }
@media (prefers-reduced-motion:reduce) { .spinner { animation-duration:1.8s; } }
</style>
</head>
<body>
<main class="window">
  <header class="titlebar" onpointerdown="if(event.button===0)window.WindowDrag()">
    <span class="title">Sympllate</span>
    <button class="close" aria-label="Close" title="Close" onpointerdown="event.stopPropagation()" onclick="window.WindowClose()"><svg viewBox="0 0 16 16" aria-hidden="true" focusable="false"><path d="m4.5 4.5 7 7m0-7-7 7"/></svg></button>
  </header>
  <section class="content">
    <span class="spinner" role="status" aria-label="Loading"></span>
    <div><h1>Loading local model</h1><p>Preparing the model for translation&hellip;</p></div>
  </section>
</main>
<script>requestAnimationFrame(function(){requestAnimationFrame(function(){window.StartupReady()})})</script>
</body>
</html>`
