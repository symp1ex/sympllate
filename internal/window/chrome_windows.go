//go:build windows

package window

import (
	"errors"
	"sync"
	"syscall"
	"unsafe"

	webview "github.com/jchv/go-webview2"
)

const (
	chromeGWLStyle    = ^uintptr(15) // -16
	chromeGWLPWndProc = ^uintptr(3)  // -4

	chromeWSCaption      = 0x00C00000
	chromeWSSysMenu      = 0x00080000
	chromeWSMinimizeBox  = 0x00020000
	chromeWSMaximizeBox  = 0x00010000
	chromeWSThickFrame   = 0x00040000
	chromeWSPopup        = 0x80000000
	chromeWSVisible      = 0x10000000
	chromeWSClipSiblings = 0x04000000
	chromeWSClipChildren = 0x02000000

	chromeSWPNoSize       = 0x0001
	chromeSWPNoMove       = 0x0002
	chromeSWPNoZOrder     = 0x0004
	chromeSWPNoActivate   = 0x0010
	chromeSWPFrameChanged = 0x0020

	chromeWMGetMinMaxInfo = 0x0024
	chromeWMNCCalcSize    = 0x0083
	chromeWMNCHitTest     = 0x0084
	chromeWMNCDestroy     = 0x0082
	chromeWMClose         = 0x0010
	chromeWMNCLButtonDown = 0x00A1

	chromeSWMaximize = 3
	chromeSWMinimize = 6
	chromeSWRestore  = 9

	chromeHTClient      = 1
	chromeHTCaption     = 2
	chromeHTLeft        = 10
	chromeHTRight       = 11
	chromeHTTop         = 12
	chromeHTTopLeft     = 13
	chromeHTTopRight    = 14
	chromeHTBottom      = 15
	chromeHTBottomLeft  = 16
	chromeHTBottomRight = 17
)

type chromePoint struct {
	X int32
	Y int32
}

type chromeRect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

type chromeMinMaxInfo struct {
	Reserved     chromePoint
	MaxSize      chromePoint
	MaxPosition  chromePoint
	MinTrackSize chromePoint
	MaxTrackSize chromePoint
}

type chromeOptions struct {
	minWidth             int32
	minHeight            int32
	titleBarHeight       int32
	titleBarButtonsWidth int32
	onClose              func()
}

var (
	chromeUser32   = syscall.NewLazyDLL("user32.dll")
	chromeKernel32 = syscall.NewLazyDLL("kernel32.dll")

	chromeGetWindowLongPtr = chromeUser32.NewProc("GetWindowLongPtrW")
	chromeSetWindowLongPtr = chromeUser32.NewProc("SetWindowLongPtrW")
	chromeSetWindowPos     = chromeUser32.NewProc("SetWindowPos")
	chromeCallWindowProc   = chromeUser32.NewProc("CallWindowProcW")
	chromeGetWindowRect    = chromeUser32.NewProc("GetWindowRect")
	chromeGetCursorPos     = chromeUser32.NewProc("GetCursorPos")
	chromeShowWindow       = chromeUser32.NewProc("ShowWindow")
	chromeIsIconic         = chromeUser32.NewProc("IsIconic")
	chromeIsZoomed         = chromeUser32.NewProc("IsZoomed")
	chromePostMessage      = chromeUser32.NewProc("PostMessageW")
	chromeReleaseCapture   = chromeUser32.NewProc("ReleaseCapture")
	chromeSendMessage      = chromeUser32.NewProc("SendMessageW")
	chromeCopyMemory       = chromeKernel32.NewProc("RtlMoveMemory")

	chromeOnce       sync.Once
	chromeWindowProc uintptr
	chromeOldProcs   sync.Map
	chromeWindows    sync.Map
)

func applyWindowChrome(w webview.WebView, minWidth, minHeight int, onClose func()) error {
	chromeOnce.Do(func() {
		chromeWindowProc = syscall.NewCallback(windowChromeProc)
	})

	hwnd := uintptr(w.Window())
	if hwnd == 0 {
		return errors.New("failed to apply custom frame: window HWND is empty")
	}

	chromeWindows.Store(hwnd, chromeOptions{
		minWidth:             int32(minWidth),
		minHeight:            int32(minHeight),
		titleBarHeight:       52,
		titleBarButtonsWidth: 158,
		onClose:              onClose,
	})

	oldProc, _, _ := chromeGetWindowLongPtr.Call(hwnd, chromeGWLPWndProc)
	if oldProc == 0 {
		chromeWindows.Delete(hwnd)
		return errors.New("failed to get the window handler for the custom frame")
	}
	chromeOldProcs.Store(hwnd, oldProc)
	chromeSetWindowLongPtr.Call(hwnd, chromeGWLPWndProc, chromeWindowProc)

	style, _, _ := chromeGetWindowLongPtr.Call(hwnd, chromeGWLStyle)
	style |= chromeWSCaption |
		chromeWSSysMenu |
		chromeWSMinimizeBox |
		chromeWSMaximizeBox |
		chromeWSThickFrame |
		chromeWSVisible |
		chromeWSClipSiblings |
		chromeWSClipChildren
	style &^= chromeWSPopup
	chromeSetWindowLongPtr.Call(hwnd, chromeGWLStyle, style)
	chromeSetWindowPos.Call(
		hwnd,
		0,
		0,
		0,
		0,
		0,
		chromeSWPNoMove|chromeSWPNoSize|chromeSWPNoZOrder|chromeSWPNoActivate|chromeSWPFrameChanged,
	)
	return nil
}

func minimizeWindow(w webview.WebView) {
	if hwnd := uintptr(w.Window()); hwnd != 0 {
		chromeShowWindow.Call(hwnd, chromeSWMinimize)
	}
}

func toggleWindowMaximized(w webview.WebView) bool {
	hwnd := uintptr(w.Window())
	if hwnd == 0 {
		return false
	}
	maximized, _, _ := chromeIsZoomed.Call(hwnd)
	if maximized != 0 {
		chromeShowWindow.Call(hwnd, chromeSWRestore)
		return false
	}
	chromeShowWindow.Call(hwnd, chromeSWMaximize)
	return true
}

func restoreWindowIfNeeded(hwnd uintptr) {
	iconic, _, _ := chromeIsIconic.Call(hwnd)
	maximized, _, _ := chromeIsZoomed.Call(hwnd)
	if iconic != 0 || maximized != 0 {
		chromeShowWindow.Call(hwnd, chromeSWRestore)
	}
}

func closeWindow(w webview.WebView) {
	if hwnd := uintptr(w.Window()); hwnd != 0 {
		chromePostMessage.Call(hwnd, chromeWMClose, 0, 0)
	}
}

func dragWindow(w webview.WebView) {
	hwnd := uintptr(w.Window())
	if hwnd == 0 {
		return
	}
	chromeReleaseCapture.Call()
	chromeSendMessage.Call(hwnd, chromeWMNCLButtonDown, chromeHTCaption, chromeCursorLParam())
}

func resizeWindow(w webview.WebView, hitTest uintptr) {
	switch hitTest {
	case chromeHTLeft,
		chromeHTRight,
		chromeHTTop,
		chromeHTTopLeft,
		chromeHTTopRight,
		chromeHTBottom,
		chromeHTBottomLeft,
		chromeHTBottomRight:
	default:
		return
	}

	hwnd := uintptr(w.Window())
	if hwnd == 0 {
		return
	}
	chromeReleaseCapture.Call()
	chromeSendMessage.Call(hwnd, chromeWMNCLButtonDown, hitTest, chromeCursorLParam())
}

func chromeCursorLParam() uintptr {
	var point chromePoint
	if result, _, _ := chromeGetCursorPos.Call(uintptr(unsafe.Pointer(&point))); result == 0 {
		return 0
	}
	return uintptr(uint32(uint16(point.X)) | uint32(uint16(point.Y))<<16)
}

func windowChromeProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	options := chromeOptionsForWindow(hwnd)
	oldProc, hasOldProc := chromeOldProcs.Load(hwnd)

	switch msg {
	case chromeWMNCCalcSize:
		return 0
	case chromeWMGetMinMaxInfo:
		updateMinMaxInfo(lParam, options)
		return 0
	case chromeWMNCHitTest:
		return hitTestWindowChrome(hwnd, lParam, options)
	case chromeWMClose:
		if options.onClose != nil {
			options.onClose()
			return 0
		}
	case chromeWMNCDestroy:
		var result uintptr = chromeHTClient
		if hasOldProc {
			result, _, _ = chromeCallWindowProc.Call(oldProc.(uintptr), hwnd, uintptr(msg), wParam, lParam)
		}
		chromeOldProcs.Delete(hwnd)
		chromeWindows.Delete(hwnd)
		return result
	}

	if hasOldProc {
		result, _, _ := chromeCallWindowProc.Call(oldProc.(uintptr), hwnd, uintptr(msg), wParam, lParam)
		return result
	}
	return chromeHTClient
}

func updateMinMaxInfo(lParam uintptr, options chromeOptions) {
	if lParam == 0 {
		return
	}
	var info chromeMinMaxInfo
	size := unsafe.Sizeof(info)
	chromeCopyMemory.Call(uintptr(unsafe.Pointer(&info)), lParam, size)
	if options.minWidth > 0 {
		info.MinTrackSize.X = options.minWidth
	}
	if options.minHeight > 0 {
		info.MinTrackSize.Y = options.minHeight
	}
	chromeCopyMemory.Call(lParam, uintptr(unsafe.Pointer(&info)), size)
}

func chromeOptionsForWindow(hwnd uintptr) chromeOptions {
	if value, ok := chromeWindows.Load(hwnd); ok {
		return value.(chromeOptions)
	}
	return chromeOptions{}
}

func hitTestWindowChrome(hwnd, lParam uintptr, options chromeOptions) uintptr {
	var rect chromeRect
	if result, _, _ := chromeGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rect))); result == 0 {
		return chromeHTClient
	}

	x := int32(int16(uint16(lParam & 0xffff)))
	y := int32(int16(uint16((lParam >> 16) & 0xffff)))
	const resizeBorder int32 = 8

	left := x >= rect.Left && x < rect.Left+resizeBorder
	right := x <= rect.Right && x > rect.Right-resizeBorder
	top := y >= rect.Top && y < rect.Top+resizeBorder
	bottom := y <= rect.Bottom && y > rect.Bottom-resizeBorder

	switch {
	case top && left:
		return chromeHTTopLeft
	case top && right:
		return chromeHTTopRight
	case bottom && left:
		return chromeHTBottomLeft
	case bottom && right:
		return chromeHTBottomRight
	case left:
		return chromeHTLeft
	case right:
		return chromeHTRight
	case top:
		return chromeHTTop
	case bottom:
		return chromeHTBottom
	}

	if options.titleBarHeight > 0 &&
		y >= rect.Top &&
		y < rect.Top+options.titleBarHeight &&
		x < rect.Right-options.titleBarButtonsWidth {
		return chromeHTCaption
	}
	return chromeHTClient
}
