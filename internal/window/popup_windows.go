//go:build windows

package window

import (
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	webview "github.com/jchv/go-webview2"
	"github.com/sympllate/translator/internal/app"
	"github.com/sympllate/translator/internal/clipboard"
	"github.com/sympllate/translator/internal/config"
)

const (
	swHide                  = 0
	swShow                  = 5
	swpNoActivate           = 0x0010
	swpShowWindow           = 0x0040
	hwndTopmost             = ^uintptr(0)
	hwndTop                 = uintptr(0)
	monitorDefaultToNearest = 2
)

var windowUser32 = syscall.NewLazyDLL("user32.dll")
var (
	showWindow          = windowUser32.NewProc("ShowWindow")
	setWindowPos        = windowUser32.NewProc("SetWindowPos")
	setForegroundWindow = windowUser32.NewProc("SetForegroundWindow")
	getCursorPos        = windowUser32.NewProc("GetCursorPos")
	monitorFromPoint    = windowUser32.NewProc("MonitorFromPoint")
	getMonitorInfo      = windowUser32.NewProc("GetMonitorInfoW")
	destroyNativeWindow = windowUser32.NewProc("DestroyWindow")
)

type point struct{ X, Y int32 }
type rect struct{ Left, Top, Right, Bottom int32 }
type monitorInfo struct {
	Size    uint32
	Monitor rect
	Work    rect
	Flags   uint32
}

type Popup struct {
	cfg     config.Config
	html    string
	service *app.Service
	clip    *clipboard.Manager
	mu      sync.RWMutex
	handler app.QuickTranslationHandler
	w       webview.WebView
	hwnd    uintptr
	state   app.PopupState
	done    chan struct{}
}

func NewPopup(cfg config.Config, html string, service *app.Service, clip *clipboard.Manager) *Popup {
	return &Popup{cfg: cfg, html: html, service: service, clip: clip}
}

func (p *Popup) SetQuickTranslationHandler(handler app.QuickTranslationHandler) {
	p.mu.Lock()
	p.handler = handler
	p.mu.Unlock()
}

func (p *Popup) Start() error {
	ready := make(chan error, 1)
	p.mu.Lock()
	p.done = make(chan struct{})
	p.mu.Unlock()
	go p.run(ready)
	return <-ready
}

func (p *Popup) run(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(p.done)
	w := webview.NewWithOptions(webview.WebViewOptions{AutoFocus: true, WindowOptions: webview.WindowOptions{Title: "Быстрый перевод", Width: uint(p.cfg.UI.PopupWidth), Height: uint(p.cfg.UI.PopupHeight), Center: true}})
	if w == nil {
		ready <- errors.New("не удалось создать popup WebView2")
		return
	}
	p.mu.Lock()
	p.w = w
	p.hwnd = uintptr(w.Window())
	p.mu.Unlock()
	defer func() { p.mu.Lock(); p.w = nil; p.hwnd = 0; p.mu.Unlock(); w.Destroy() }()
	if err := bindCommon(w, "popup", p.cfg, p.service, p.clip, p); err != nil {
		ready <- err
		return
	}
	if err := applyWindowChrome(w, 320, 240, p.Hide); err != nil {
		ready <- err
		return
	}
	showWindow.Call(p.hwnd, swHide)
	w.SetHtml(p.html)
	ready <- nil
	w.Run()
}

func (p *Popup) Show(state app.PopupState)   { p.setState(state, true) }
func (p *Popup) Update(state app.PopupState) { p.setState(state, false) }

func (p *Popup) setState(state app.PopupState, show bool) {
	p.mu.Lock()
	p.state = state
	w, hwnd := p.w, p.hwnd
	p.mu.Unlock()
	if w == nil {
		return
	}
	payload, _ := json.Marshal(state)
	w.Dispatch(func() {
		w.Eval("window.dispatchEvent(new CustomEvent('translator-popup',{detail:" + string(payload) + "}))")
	})
	if show {
		p.position(hwnd)
		showWindow.Call(hwnd, swShow)
		setForegroundWindow.Call(hwnd)
	}
}

func (p *Popup) position(hwnd uintptr) {
	restoreWindowIfNeeded(hwnd)
	var cursor point
	if result, _, _ := getCursorPos.Call(uintptr(unsafe.Pointer(&cursor))); result == 0 {
		return
	}
	packed := uintptr(uint32(cursor.X)) | (uintptr(uint32(cursor.Y)) << 32)
	monitor, _, _ := monitorFromPoint.Call(packed, monitorDefaultToNearest)
	if monitor == 0 {
		return
	}
	info := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
	if result, _, _ := getMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info))); result == 0 {
		return
	}
	w, h := int32(p.cfg.UI.PopupWidth), int32(p.cfg.UI.PopupHeight)
	x, y := cursor.X+16, cursor.Y+20
	if x+w > info.Work.Right {
		x = info.Work.Right - w
	}
	if y+h > info.Work.Bottom {
		y = cursor.Y - h - 16
	}
	if x < info.Work.Left {
		x = info.Work.Left
	}
	if y < info.Work.Top {
		y = info.Work.Top
	}
	insertAfter := hwndTop
	if p.cfg.UI.AlwaysOnTopPopup {
		insertAfter = hwndTopmost
	}
	setWindowPos.Call(hwnd, insertAfter, uintptr(x), uintptr(y), uintptr(w), uintptr(h), swpShowWindow)
}

func (p *Popup) Hide() {
	p.mu.RLock()
	hwnd, handler := p.hwnd, p.handler
	p.mu.RUnlock()
	if hwnd != 0 {
		showWindow.Call(hwnd, swHide)
	}
	if handler != nil {
		handler.EndQuickTranslation()
	}
}

func (p *Popup) ChangeQuickTranslationTarget(target string) error {
	p.mu.RLock()
	handler := p.handler
	p.mu.RUnlock()
	if handler == nil {
		return errors.New("обработчик быстрого перевода не настроен")
	}
	return handler.ChangeQuickTranslationTarget(target)
}
func (p *Popup) State() app.PopupState { p.mu.RLock(); defer p.mu.RUnlock(); return p.state }
func (p *Popup) Close() {
	p.mu.RLock()
	w, done := p.w, p.done
	p.mu.RUnlock()
	if w != nil {
		w.Dispatch(func() { w.Terminate() })
	}
	if done != nil {
		<-done
	}
}

func (p *Popup) String() string {
	return fmt.Sprintf("popup(%dx%d)", p.cfg.UI.PopupWidth, p.cfg.UI.PopupHeight)
}
