//go:build windows

package window

import (
	"sync"
	"sync/atomic"
	"testing"
)

func TestMainWindowOnlyStartsOneConcurrentOpen(t *testing.T) {
	window := &MainWindow{state: mainWindowIdle}
	var starts atomic.Int32
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start, _, _ := window.beginOpen(mainWindowViewMain)
			if start {
				starts.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := starts.Load(); got != 1 {
		t.Fatalf("concurrent beginOpen starts = %d, want 1", got)
	}
}

func TestMainWindowCanRetryAfterCreationFailure(t *testing.T) {
	window := &MainWindow{state: mainWindowIdle}
	start, _, _ := window.beginOpen(mainWindowViewMain)
	if !start {
		t.Fatal("first beginOpen did not start creation")
	}
	window.failOpen(assertionError("creation failed"))
	start, _, _ = window.beginOpen(mainWindowViewMain)
	if !start {
		t.Fatal("beginOpen did not retry after creation failure")
	}
}

func TestMainWindowShutdownPreventsOpen(t *testing.T) {
	window := &MainWindow{state: mainWindowIdle}
	window.Shutdown()
	start, webView, hwnd := window.beginOpen(mainWindowViewMain)
	if start || webView != nil || hwnd != 0 {
		t.Fatalf("beginOpen after Shutdown = (%v, %v, %d), want no action", start, webView, hwnd)
	}
	window.Shutdown()
}

func TestMainWindowRemembersRequestedSettingsView(t *testing.T) {
	t.Parallel()
	window := &MainWindow{state: mainWindowIdle, view: mainWindowViewMain}
	start, _, _ := window.beginOpen(mainWindowViewSettings)
	if !start {
		t.Fatal("settings request did not start window creation")
	}
	if got := window.currentView(); got != string(mainWindowViewSettings) {
		t.Fatalf("currentView() = %q, want settings", got)
	}
}

type assertionError string

func (e assertionError) Error() string { return string(e) }
