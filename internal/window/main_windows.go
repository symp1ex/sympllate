//go:build windows

package window

import (
	"errors"
	"fmt"
	"log"
	"runtime"
	"sync"

	webview "github.com/jchv/go-webview2"
	"github.com/sympllate/translator/internal/app"
	"github.com/sympllate/translator/internal/clipboard"
	"github.com/sympllate/translator/internal/config"
)

type mainWindowState uint8

const (
	mainWindowIdle mainWindowState = iota
	mainWindowStarting
	mainWindowRunning
	mainWindowStopping
	mainWindowStopped
)

type MainWindow struct {
	cfg     config.Config
	html    string
	service *app.Service
	clip    *clipboard.Manager
	popup   *Popup
	logger  *log.Logger
	onError func(error)

	mu    sync.Mutex
	state mainWindowState
	w     webview.WebView
	hwnd  uintptr
	done  chan struct{}
}

func NewMainWindow(cfg config.Config, html string, service *app.Service, clip *clipboard.Manager, popup *Popup, logger *log.Logger, onError func(error)) *MainWindow {
	return &MainWindow{
		cfg:     cfg,
		html:    html,
		service: service,
		clip:    clip,
		popup:   popup,
		logger:  logger,
		onError: onError,
		state:   mainWindowIdle,
	}
}

// Open schedules the first WebView creation on its own OS thread, or shows the
// existing instance. It intentionally returns before WebView creation begins.
func (m *MainWindow) Open() {
	start, w, hwnd := m.beginOpen()
	if w != nil {
		w.Dispatch(func() { showMainWindow(hwnd) })
		return
	}
	if start {
		go m.run()
	}
}

func (m *MainWindow) Hide() {
	m.mu.Lock()
	if m.state != mainWindowRunning {
		m.mu.Unlock()
		return
	}
	w, hwnd := m.w, m.hwnd
	m.mu.Unlock()
	if w != nil {
		w.Dispatch(func() { showWindow.Call(hwnd, swHide) })
	}
}

func (m *MainWindow) Shutdown() {
	m.mu.Lock()
	switch m.state {
	case mainWindowStopped:
		m.mu.Unlock()
		return
	case mainWindowIdle:
		m.state = mainWindowStopped
		m.mu.Unlock()
		return
	case mainWindowStarting, mainWindowRunning:
		m.state = mainWindowStopping
	}
	w, done := m.w, m.done
	m.mu.Unlock()

	if w != nil {
		// go-webview2's Windows Terminate uses PostQuitMessage, so it must run
		// on the WebView thread even though the interface documents it as safe.
		w.Dispatch(func() { w.Terminate() })
	}
	if done != nil {
		<-done
	}
}

func (m *MainWindow) beginOpen() (bool, webview.WebView, uintptr) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch m.state {
	case mainWindowIdle:
		m.state = mainWindowStarting
		m.done = make(chan struct{})
		return true, nil, 0
	case mainWindowRunning:
		return false, m.w, m.hwnd
	default:
		return false, nil, 0
	}
}

func (m *MainWindow) run() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	w := webview.NewWithOptions(webview.WebViewOptions{
		AutoFocus: true,
		WindowOptions: webview.WindowOptions{
			Title:  "Sympllate",
			Width:  uint(m.cfg.UI.MainWindowWidth),
			Height: uint(m.cfg.UI.MainWindowHeight),
			Center: true,
		},
	})
	if w == nil {
		m.failOpen(errors.New("не удалось создать основное окно WebView2; проверьте наличие Microsoft Edge WebView2 Runtime"))
		return
	}
	hwnd := uintptr(w.Window())
	if hwnd == 0 {
		m.destroyWebView(w, hwnd)
		m.failOpen(errors.New("основное окно WebView2 не предоставило HWND"))
		return
	}
	if err := bindCommon(w, "main", m.cfg, m.service, m.clip, m.popup); err != nil {
		m.destroyWebView(w, hwnd)
		m.failOpen(fmt.Errorf("настроить основное окно: %w", err))
		return
	}
	if err := applyWindowChrome(w, 400, 300, m.Hide); err != nil {
		m.destroyWebView(w, hwnd)
		m.failOpen(fmt.Errorf("настроить рамку основного окна: %w", err))
		return
	}
	w.SetHtml(m.html)

	m.mu.Lock()
	stopping := m.state == mainWindowStopping
	if !stopping {
		m.w = w
		m.hwnd = hwnd
		m.state = mainWindowRunning
	}
	m.mu.Unlock()
	if stopping {
		m.destroyWebView(w, hwnd)
		m.finishWorker()
		return
	}

	showMainWindow(hwnd)
	w.Run()
	m.destroyWebView(w, hwnd)
	unexpected := m.finishWorker()
	if unexpected && m.logger != nil {
		m.logger.Printf("main window message loop stopped unexpectedly; Open can create it again")
	}
}

func (m *MainWindow) failOpen(err error) {
	m.mu.Lock()
	notify := m.state != mainWindowStopping && m.state != mainWindowStopped
	if notify {
		m.state = mainWindowIdle
	} else {
		m.state = mainWindowStopped
	}
	m.w = nil
	m.hwnd = 0
	done := m.done
	m.done = nil
	m.mu.Unlock()
	if done != nil {
		close(done)
	}
	if m.logger != nil {
		m.logger.Printf("main window creation failed: %v", err)
	}
	if notify && m.onError != nil {
		go m.onError(err)
	}
}

func (m *MainWindow) finishWorker() bool {
	m.mu.Lock()
	unexpected := m.state == mainWindowRunning
	if m.state == mainWindowStopping {
		m.state = mainWindowStopped
	} else if m.state != mainWindowStopped {
		m.state = mainWindowIdle
	}
	m.w = nil
	m.hwnd = 0
	done := m.done
	m.done = nil
	m.mu.Unlock()
	if done != nil {
		close(done)
	}
	return unexpected
}

func (m *MainWindow) destroyWebView(w webview.WebView, hwnd uintptr) {
	w.Destroy()
	if hwnd != 0 {
		destroyNativeWindow.Call(hwnd)
	}
}

func showMainWindow(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	if iconic, _, _ := chromeIsIconic.Call(hwnd); iconic != 0 {
		showWindow.Call(hwnd, chromeSWRestore)
	}
	showWindow.Call(hwnd, swShow)
	setForegroundWindow.Call(hwnd)
}
