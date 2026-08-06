//go:build windows

package window

import (
	"errors"
	"runtime"
	"sync"

	webview "github.com/jchv/go-webview2"
	"github.com/sympllate/translator/internal/app"
	"github.com/sympllate/translator/internal/clipboard"
	"github.com/sympllate/translator/internal/config"
	"github.com/sympllate/translator/internal/imagebatch"
	"github.com/sympllate/translator/internal/logger"
)

const (
	imageBatchWindowWidth  = 720
	imageBatchWindowHeight = 540
)

type ImageBatchWindow struct {
	cfg     config.Config
	html    string
	service *app.Service
	batch   *imagebatch.Service
	clip    *clipboard.Manager
	popup   *Popup

	mu   sync.RWMutex
	w    webview.WebView
	hwnd uintptr
	done chan struct{}
}

func NewImageBatchWindow(cfg config.Config, html string, service *app.Service, batch *imagebatch.Service, clip *clipboard.Manager, popup *Popup) *ImageBatchWindow {
	return &ImageBatchWindow{cfg: cfg, html: html, service: service, batch: batch, clip: clip, popup: popup}
}

func (b *ImageBatchWindow) Start() error {
	ready := make(chan error, 1)
	b.mu.Lock()
	b.done = make(chan struct{})
	b.mu.Unlock()
	go b.run(ready)
	return <-ready
}

func (b *ImageBatchWindow) run(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(b.done)
	w := webview.NewWithOptions(webview.WebViewOptions{
		AutoFocus: true,
		WindowOptions: webview.WindowOptions{
			Title: "Batch Image Translation", Width: imageBatchWindowWidth, Height: imageBatchWindowHeight, Center: true,
		},
	})
	if w == nil {
		ready <- errors.New("failed to create image batch WebView2 window")
		return
	}
	hwnd := uintptr(w.Window())
	if hwnd == 0 {
		w.Destroy()
		ready <- errors.New("the image batch WebView2 window did not provide an HWND")
		return
	}
	b.mu.Lock()
	b.w, b.hwnd = w, hwnd
	b.mu.Unlock()
	icons, err := setTaskbarIcon(hwnd)
	if err != nil {
		logger.Sympllate.Warnf("image batch window taskbar icon was not set: %v", err)
	}
	defer func() {
		b.mu.Lock()
		b.w, b.hwnd = nil, 0
		b.mu.Unlock()
		w.Destroy()
		icons.destroy()
	}()
	if err := bindCommon(w, "batch", b.cfg, b.service, b.clip, b.popup, b.Hide); err != nil {
		ready <- err
		return
	}
	if err := bindImageBatch(w, hwnd, b.batch); err != nil {
		ready <- err
		return
	}
	if err := applyWindowChrome(w, 520, 380, b.Hide); err != nil {
		ready <- err
		return
	}
	showWindow.Call(hwnd, swHide)
	w.SetHtml(b.html)
	ready <- nil
	w.Run()
}

func (b *ImageBatchWindow) Open() {
	b.mu.RLock()
	hwnd := b.hwnd
	b.mu.RUnlock()
	if hwnd == 0 {
		return
	}
	restoreWindowIfNeeded(hwnd)
	showWindow.Call(hwnd, swShow)
	setForegroundWindow.Call(hwnd)
}

func (b *ImageBatchWindow) Hide() {
	b.mu.RLock()
	hwnd := b.hwnd
	b.mu.RUnlock()
	if hwnd != 0 {
		showWindow.Call(hwnd, swHide)
	}
}

func (b *ImageBatchWindow) Close() {
	b.mu.RLock()
	w, done := b.w, b.done
	b.mu.RUnlock()
	if w != nil {
		w.Dispatch(func() { w.Terminate() })
	}
	if done != nil {
		<-done
	}
}
