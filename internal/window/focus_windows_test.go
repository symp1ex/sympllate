//go:build windows

package window

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sympllate/translator/internal/app"
)

func TestOriginTargetCaptureIncludesFocusedControlAndIdentity(t *testing.T) {
	manager := &OriginTargetManager{
		foreground: func() uintptr { return 10 },
		exists:     func(hwnd uintptr) bool { return hwnd == 10 || hwnd == 11 },
		identity:   func(uintptr) (uint32, uint32) { return 12, 13 },
		focus:      func(uint32) (uintptr, bool) { return 11, true },
	}
	target, err := manager.Capture()
	if err != nil {
		t.Fatal(err)
	}
	want := app.OriginTarget{Window: 10, Focus: 11, ThreadID: 12, ProcessID: 13}
	if target != want {
		t.Fatalf("Capture() = %+v, want %+v", target, want)
	}
}

func TestOriginTargetActivationWaitsForForegroundAndFocusedControl(t *testing.T) {
	var foreground atomic.Uintptr
	var focus atomic.Uintptr
	foreground.Store(20)
	focus.Store(21)
	target := app.OriginTarget{Window: 10, Focus: 11, ThreadID: 12, ProcessID: 13}
	manager := &OriginTargetManager{
		foreground: func() uintptr { return foreground.Load() },
		exists:     func(hwnd uintptr) bool { return hwnd == 10 || hwnd == 11 },
		identity:   func(uintptr) (uint32, uint32) { return 12, 13 },
		focus:      func(uint32) (uintptr, bool) { return focus.Load(), true },
		activate: func(uintptr) bool {
			foreground.Store(10)
			return true
		},
		poll: time.Millisecond, timeout: 100 * time.Millisecond,
	}
	go func() {
		time.Sleep(10 * time.Millisecond)
		focus.Store(11)
	}()
	if err := manager.Activate(context.Background(), target); err != nil {
		t.Fatal(err)
	}
}

func TestOriginTargetActivationTimeoutDoesNotAcceptWrongForeground(t *testing.T) {
	target := app.OriginTarget{Window: 10, Focus: 11, ThreadID: 12, ProcessID: 13}
	manager := &OriginTargetManager{
		foreground: func() uintptr { return 20 },
		exists:     func(hwnd uintptr) bool { return hwnd == 10 || hwnd == 11 },
		identity:   func(uintptr) (uint32, uint32) { return 12, 13 },
		focus:      func(uint32) (uintptr, bool) { return 11, true },
		activate:   func(uintptr) bool { return true },
		poll:       time.Millisecond,
		timeout:    10 * time.Millisecond,
	}
	err := manager.Activate(context.Background(), target)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("Activate() error = %v", err)
	}
}

func TestOriginTargetRejectsReusedWindowHandle(t *testing.T) {
	target := app.OriginTarget{Window: 10, Focus: 11, ThreadID: 12, ProcessID: 13}
	manager := &OriginTargetManager{
		exists:   func(uintptr) bool { return true },
		identity: func(uintptr) (uint32, uint32) { return 99, 100 },
	}
	if manager.Exists(target) {
		t.Fatal("reused HWND with another thread/process identity was accepted")
	}
}
