//go:build windows

package window

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStartupWindowCloseIsIdempotent(t *testing.T) {
	t.Parallel()
	w := NewStartupWindow(nil)
	done := make(chan struct{})
	var closeCalls atomic.Int32
	w.done = done
	w.closeUI = func() {
		closeCalls.Add(1)
		close(done)
	}
	closed := make(chan struct{})
	go func() {
		w.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close blocked after the UI close completed")
	}
	w.Close()
	if got := closeCalls.Load(); got != 1 {
		t.Fatalf("UI close calls = %d, want 1", got)
	}
	if w.WasClosedByUser() {
		t.Fatal("programmatic Close was reported as a user close")
	}
}

func TestStartupWindowUserCloseCancelsOnlyOnce(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	w := NewStartupWindow(func() { calls.Add(1) })
	w.notifyUserClose()
	w.notifyUserClose()
	w.Close()
	if got := calls.Load(); got != 1 {
		t.Fatalf("user close callback calls = %d, want 1", got)
	}
	if !w.WasClosedByUser() {
		t.Fatal("user close was not recorded")
	}
}

func TestStartupWindowProgrammaticCloseWinsRaceWithoutCancellation(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	w := NewStartupWindow(func() { calls.Add(1) })
	w.Close()

	var group sync.WaitGroup
	for range 16 {
		group.Add(1)
		go func() {
			defer group.Done()
			w.notifyUserClose()
			w.Close()
		}()
	}
	group.Wait()
	if got := calls.Load(); got != 0 {
		t.Fatalf("user close callback calls after programmatic close = %d, want 0", got)
	}
	if w.WasClosedByUser() {
		t.Fatal("programmatic close was changed into a user close")
	}
}
