//go:build windows

package tray

import (
	"errors"
	"fmt"
	"log"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

const (
	wmDestroy       = 0x0002
	wmClose         = 0x0010
	wmNull          = 0x0000
	wmQuit          = 0x0012
	wmContextMenu   = 0x007B
	wmLButtonDblClk = 0x0203
	wmRButtonUp     = 0x0205
	wmApp           = 0x8000
	trayCallback    = wmApp + 1

	nimAdd        = 0x00000000
	nimDelete     = 0x00000002
	nimSetVersion = 0x00000004

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004
	nifShowTip = 0x00000080

	notifyIconVersion4 = 4

	mfString = 0x00000000

	tpmRightButton = 0x0002
	tpmReturnCmd   = 0x0100

	menuOpen = 1
	menuQuit = 2

	smCXSmallIcon = 49
	smCYSmallIcon = 50
)

var (
	trayUser32   = syscall.NewLazyDLL("user32.dll")
	trayShell32  = syscall.NewLazyDLL("shell32.dll")
	trayKernel32 = syscall.NewLazyDLL("kernel32.dll")

	appendMenu               = trayUser32.NewProc("AppendMenuW")
	createIconFromResourceEx = trayUser32.NewProc("CreateIconFromResourceEx")
	createPopupMenu          = trayUser32.NewProc("CreatePopupMenu")
	createWindowEx           = trayUser32.NewProc("CreateWindowExW")
	defWindowProc            = trayUser32.NewProc("DefWindowProcW")
	destroyIcon              = trayUser32.NewProc("DestroyIcon")
	destroyMenu              = trayUser32.NewProc("DestroyMenu")
	destroyWindow            = trayUser32.NewProc("DestroyWindow")
	getCursorPos             = trayUser32.NewProc("GetCursorPos")
	getMessage               = trayUser32.NewProc("GetMessageW")
	getSystemMetrics         = trayUser32.NewProc("GetSystemMetrics")
	postMessage              = trayUser32.NewProc("PostMessageW")
	postQuitMessage          = trayUser32.NewProc("PostQuitMessage")
	registerClassEx          = trayUser32.NewProc("RegisterClassExW")
	registerWindowMessage    = trayUser32.NewProc("RegisterWindowMessageW")
	setForegroundWindow      = trayUser32.NewProc("SetForegroundWindow")
	setMenuDefaultItem       = trayUser32.NewProc("SetMenuDefaultItem")
	trackPopupMenu           = trayUser32.NewProc("TrackPopupMenu")
	translateMessage         = trayUser32.NewProc("TranslateMessage")
	dispatchMessage          = trayUser32.NewProc("DispatchMessageW")
	getModuleHandle          = trayKernel32.NewProc("GetModuleHandleW")
	shellNotifyIcon          = trayShell32.NewProc("Shell_NotifyIconW")

	trayClassOnce sync.Once
	trayClassErr  error
	trayWndProc   uintptr
	trayWindows   sync.Map
)

type point struct {
	x int32
	y int32
}

type message struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	point   point
	private uint32
}

type windowClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   uintptr
	icon       uintptr
	cursor     uintptr
	background uintptr
	menuName   *uint16
	className  *uint16
	smallIcon  uintptr
}

type notifyIconData struct {
	size        uint32
	hwnd        uintptr
	id          uint32
	flags       uint32
	callback    uint32
	icon        uintptr
	tip         [128]uint16
	state       uint32
	stateMask   uint32
	info        [256]uint16
	version     uint32
	infoTitle   [64]uint16
	infoFlags   uint32
	guidItem    [16]byte
	balloonIcon uintptr
}

type Tray struct {
	logger *log.Logger
	onOpen func()
	quit   *quitSignal

	mu       sync.Mutex
	launched bool
	started  bool
	quitting bool
	closing  bool
	hwnd     uintptr
	done     chan struct{}
	handlers sync.WaitGroup

	icon           uintptr
	iconAdded      bool
	taskbarCreated uint32
}

func New(onOpen func(), logger *log.Logger) *Tray {
	return &Tray{
		logger: logger,
		onOpen: onOpen,
		quit:   newQuitSignal(),
		done:   make(chan struct{}),
	}
}

func (t *Tray) Start() error {
	t.mu.Lock()
	if t.started {
		t.mu.Unlock()
		return nil
	}
	if t.closing {
		t.mu.Unlock()
		return errors.New("tray has already been closed")
	}
	t.started = true
	t.launched = true
	t.mu.Unlock()

	ready := make(chan error, 1)
	go t.run(ready)
	if err := <-ready; err != nil {
		t.mu.Lock()
		t.started = false
		t.closing = true
		t.mu.Unlock()
		<-t.done
		return err
	}
	return nil
}

func (t *Tray) Quit() <-chan struct{} {
	return t.quit.channel()
}

func (t *Tray) Close() {
	t.mu.Lock()
	if t.closing {
		launched, done := t.launched, t.done
		t.mu.Unlock()
		if launched {
			<-done
		}
		t.handlers.Wait()
		return
	}
	t.closing = true
	hwnd, started, done := t.hwnd, t.started, t.done
	t.mu.Unlock()

	if started && hwnd != 0 {
		postMessage.Call(hwnd, wmClose, 0, 0)
	}
	if started {
		<-done
	}
	t.handlers.Wait()
}

func (t *Tray) run(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(t.done)

	if err := ensureTrayWindowClass(); err != nil {
		ready <- err
		return
	}
	width := int(systemMetric(smCXSmallIcon, 16))
	height := int(systemMetric(smCYSmallIcon, 16))
	icon, err := createTrayIcon(width, height)
	if err != nil {
		ready <- err
		return
	}
	t.icon = icon
	defer func() {
		if t.icon != 0 {
			destroyIcon.Call(t.icon)
			t.icon = 0
		}
	}()

	className, _ := syscall.UTF16PtrFromString("Sympllate.Tray.Window")
	windowName, _ := syscall.UTF16PtrFromString("Sympllate Tray")
	instance, _, _ := getModuleHandle.Call(0)
	hwnd, _, callErr := createWindowEx.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(windowName)),
		0,
		0, 0, 0, 0,
		0, 0, instance, 0,
	)
	if hwnd == 0 {
		ready <- win32Error("create tray message window", callErr)
		return
	}
	trayWindows.Store(hwnd, t)
	t.mu.Lock()
	t.hwnd = hwnd
	t.mu.Unlock()
	defer func() {
		t.deleteIcon()
		trayWindows.Delete(hwnd)
		destroyWindow.Call(hwnd)
		t.mu.Lock()
		t.hwnd = 0
		t.started = false
		t.mu.Unlock()
	}()

	taskbarCreatedName, err := syscall.UTF16PtrFromString("TaskbarCreated")
	if err != nil {
		ready <- fmt.Errorf("encode TaskbarCreated message name: %w", err)
		return
	}
	registeredMessage, _, callErr := registerWindowMessage.Call(uintptr(unsafe.Pointer(taskbarCreatedName)))
	if registeredMessage == 0 {
		ready <- win32Error("register TaskbarCreated message", callErr)
		return
	}
	t.taskbarCreated = uint32(registeredMessage)
	if err := t.addIcon(); err != nil {
		ready <- err
		return
	}
	ready <- nil

	var msg message
	for {
		result, _, callErr := getMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(result) == -1 {
			t.logf("tray message loop failed: %v", win32Error("GetMessageW", callErr))
			return
		}
		if result == 0 || msg.message == wmQuit {
			return
		}
		translateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		dispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func (t *Tray) addIcon() error {
	data, err := t.notificationData()
	if err != nil {
		return err
	}
	result, _, callErr := shellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&data)))
	if result == 0 {
		return win32Error("add tray icon", callErr)
	}
	t.iconAdded = true
	data.version = notifyIconVersion4
	result, _, callErr = shellNotifyIcon.Call(nimSetVersion, uintptr(unsafe.Pointer(&data)))
	if result == 0 {
		t.deleteIcon()
		return win32Error("set tray icon notification version", callErr)
	}
	return nil
}

func (t *Tray) deleteIcon() {
	if !t.iconAdded {
		return
	}
	data, err := t.notificationData()
	if err == nil {
		shellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&data)))
	}
	t.iconAdded = false
}

func (t *Tray) notificationData() (notifyIconData, error) {
	data := notifyIconData{
		hwnd:     t.hwnd,
		id:       1,
		flags:    nifMessage | nifIcon | nifTip | nifShowTip,
		callback: trayCallback,
		icon:     t.icon,
	}
	data.size = uint32(unsafe.Sizeof(data))
	tip, err := syscall.UTF16FromString("Sympllate")
	if err != nil {
		return notifyIconData{}, fmt.Errorf("encode tray tooltip: %w", err)
	}
	copy(data.tip[:], tip)
	return data, nil
}

func (t *Tray) recoverIcon() {
	if !t.acceptingActions() {
		return
	}
	t.iconAdded = false
	if err := t.addIcon(); err != nil {
		t.logf("restore tray icon after Explorer restart: %v", err)
	}
}

func (t *Tray) showMenu() {
	menu, _, callErr := createPopupMenu.Call()
	if menu == 0 {
		t.logf("create tray menu: %v", win32Error("CreatePopupMenu", callErr))
		return
	}
	defer destroyMenu.Call(menu)

	if !appendMenuItem(menu, menuOpen, "Open") || !appendMenuItem(menu, menuQuit, "Quit") {
		t.logf("create tray menu items")
		return
	}
	if result, _, _ := setMenuDefaultItem.Call(menu, menuOpen, 0); result == 0 {
		t.logf("set Open as default tray menu item")
	}

	var cursor point
	if result, _, callErr := getCursorPos.Call(uintptr(unsafe.Pointer(&cursor))); result == 0 {
		t.logf("get cursor position for tray menu: %v", win32Error("GetCursorPos", callErr))
		return
	}
	setForegroundWindow.Call(t.hwnd)
	command, _, _ := trackPopupMenu.Call(
		menu,
		tpmRightButton|tpmReturnCmd,
		uintptr(cursor.x),
		uintptr(cursor.y),
		0,
		t.hwnd,
		0,
	)
	postMessage.Call(t.hwnd, wmNull, 0, 0)

	switch command {
	case menuOpen:
		t.launchOpen()
	case menuQuit:
		t.requestQuit()
	}
}

func (t *Tray) launchOpen() {
	t.mu.Lock()
	if t.quitting || t.closing || t.onOpen == nil {
		t.mu.Unlock()
		return
	}
	callback := t.onOpen
	t.handlers.Add(1)
	t.mu.Unlock()
	go func() {
		defer t.handlers.Done()
		callback()
	}()
}

func (t *Tray) requestQuit() {
	t.mu.Lock()
	if t.quitting || t.closing {
		t.mu.Unlock()
		return
	}
	t.quitting = true
	t.mu.Unlock()
	t.quit.signal()
}

func (t *Tray) acceptingActions() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.quitting && !t.closing
}

func trayWindowProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	value, ok := trayWindows.Load(hwnd)
	if !ok {
		result, _, _ := defWindowProc.Call(hwnd, uintptr(msg), wParam, lParam)
		return result
	}
	t := value.(*Tray)
	if msg == t.taskbarCreated && t.taskbarCreated != 0 {
		t.recoverIcon()
		return 0
	}
	switch msg {
	case trayCallback:
		if !t.acceptingActions() {
			return 0
		}
		switch uint32(lParam & 0xffff) {
		case wmLButtonDblClk:
			t.launchOpen()
		case wmRButtonUp, wmContextMenu:
			t.showMenu()
		}
		return 0
	case wmClose:
		destroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		postQuitMessage.Call(0)
		return 0
	default:
		result, _, _ := defWindowProc.Call(hwnd, uintptr(msg), wParam, lParam)
		return result
	}
}

func ensureTrayWindowClass() error {
	trayClassOnce.Do(func() {
		trayWndProc = syscall.NewCallback(trayWindowProc)
		className, _ := syscall.UTF16PtrFromString("Sympllate.Tray.Window")
		instance, _, _ := getModuleHandle.Call(0)
		class := windowClassEx{
			size:      uint32(unsafe.Sizeof(windowClassEx{})),
			wndProc:   trayWndProc,
			instance:  instance,
			className: className,
		}
		if atom, _, callErr := registerClassEx.Call(uintptr(unsafe.Pointer(&class))); atom == 0 {
			trayClassErr = win32Error("register tray window class", callErr)
		}
	})
	return trayClassErr
}

func appendMenuItem(menu, id uintptr, label string) bool {
	text, err := syscall.UTF16PtrFromString(label)
	if err != nil {
		return false
	}
	result, _, _ := appendMenu.Call(menu, mfString, id, uintptr(unsafe.Pointer(text)))
	return result != 0
}

func systemMetric(index int, fallback uintptr) uintptr {
	value, _, _ := getSystemMetrics.Call(uintptr(index))
	if value == 0 {
		return fallback
	}
	return value
}

func win32Error(operation string, err error) error {
	if err == nil || err == syscall.Errno(0) {
		return errors.New(operation + " failed")
	}
	return fmt.Errorf("%s failed: %w", operation, err)
}

func (t *Tray) logf(format string, args ...any) {
	if t.logger != nil {
		t.logger.Printf(format, args...)
	}
}
