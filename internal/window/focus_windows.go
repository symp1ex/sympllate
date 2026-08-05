//go:build windows

package window

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unsafe"

	"github.com/sympllate/translator/internal/app"
)

const (
	originActivationPoll    = 15 * time.Millisecond
	originActivationTimeout = 600 * time.Millisecond
)

var (
	getForegroundWindow      = windowUser32.NewProc("GetForegroundWindow")
	isWindow                 = windowUser32.NewProc("IsWindow")
	getWindowThreadProcessID = windowUser32.NewProc("GetWindowThreadProcessId")
	getGUIThreadInfo         = windowUser32.NewProc("GetGUIThreadInfo")
)

type guiThreadInfo struct {
	Size          uint32
	Flags         uint32
	Active        uintptr
	Focus         uintptr
	Capture       uintptr
	MenuOwner     uintptr
	MoveSize      uintptr
	Caret         uintptr
	CaretPosition rect
}

type OriginTargetManager struct {
	foreground func() uintptr
	exists     func(uintptr) bool
	identity   func(uintptr) (uint32, uint32)
	focus      func(uint32) (uintptr, bool)
	activate   func(uintptr) bool
	poll       time.Duration
	timeout    time.Duration
}

func NewOriginTargetManager() *OriginTargetManager {
	return &OriginTargetManager{
		foreground: foregroundWindow,
		exists:     windowExists,
		identity:   windowIdentity,
		focus:      focusedWindow,
		activate:   activateWindow,
		poll:       originActivationPoll,
		timeout:    originActivationTimeout,
	}
}

func (m *OriginTargetManager) Capture() (app.OriginTarget, error) {
	hwnd := m.foreground()
	if hwnd == 0 {
		return app.OriginTarget{}, errors.New("не удалось определить активное исходное окно")
	}
	threadID, processID := m.identity(hwnd)
	if threadID == 0 || processID == 0 || !m.exists(hwnd) {
		return app.OriginTarget{}, errors.New("исходное окно больше недоступно")
	}
	focus, ok := m.focus(threadID)
	if !ok || focus == 0 {
		return app.OriginTarget{}, errors.New("не удалось определить исходное поле с фокусом ввода")
	}
	return app.OriginTarget{Window: hwnd, Focus: focus, ThreadID: threadID, ProcessID: processID}, nil
}

func (m *OriginTargetManager) Exists(target app.OriginTarget) bool {
	if target.Window == 0 || !m.exists(target.Window) {
		return false
	}
	threadID, processID := m.identity(target.Window)
	if threadID != target.ThreadID || processID != target.ProcessID {
		return false
	}
	if target.Focus == 0 || !m.exists(target.Focus) {
		return false
	}
	focusThreadID, focusProcessID := m.identity(target.Focus)
	return focusThreadID == target.ThreadID && focusProcessID == target.ProcessID
}

func (m *OriginTargetManager) Activate(ctx context.Context, target app.OriginTarget) error {
	if !m.Exists(target) {
		return errors.New("исходное окно или поле ввода закрыто")
	}
	if m.isActive(target) {
		return nil
	}
	if !m.activate(target.Window) {
		return errors.New("Windows отклонила активацию исходного окна")
	}
	deadline := time.NewTimer(m.timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(m.poll)
	defer ticker.Stop()
	for {
		if m.isActive(target) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("истекло время ожидания возврата фокуса в исходное поле")
		case <-ticker.C:
			if !m.Exists(target) {
				return errors.New("исходное окно или поле ввода закрыто во время активации")
			}
		}
	}
}

func (m *OriginTargetManager) isActive(target app.OriginTarget) bool {
	if m.foreground() != target.Window {
		return false
	}
	if target.Focus == 0 {
		return true
	}
	focus, ok := m.focus(target.ThreadID)
	return ok && focus == target.Focus
}

func foregroundWindow() uintptr {
	hwnd, _, _ := getForegroundWindow.Call()
	return hwnd
}

func windowExists(hwnd uintptr) bool {
	result, _, _ := isWindow.Call(hwnd)
	return result != 0
}

func windowIdentity(hwnd uintptr) (uint32, uint32) {
	var processID uint32
	threadID, _, _ := getWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&processID)))
	return uint32(threadID), processID
}

func focusedWindow(threadID uint32) (uintptr, bool) {
	info := guiThreadInfo{Size: uint32(unsafe.Sizeof(guiThreadInfo{}))}
	result, _, _ := getGUIThreadInfo.Call(uintptr(threadID), uintptr(unsafe.Pointer(&info)))
	return info.Focus, result != 0
}

func activateWindow(hwnd uintptr) bool {
	result, _, _ := setForegroundWindow.Call(hwnd)
	return result != 0
}

func (m *OriginTargetManager) String() string {
	return fmt.Sprintf("origin-target(timeout=%s)", m.timeout)
}
