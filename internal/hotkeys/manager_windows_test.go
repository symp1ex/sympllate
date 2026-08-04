//go:build windows

package hotkeys

import (
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func TestReleaseKeys(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		combination Combination
		want        []uint32
	}{
		{
			name:        "Ctrl+Win+X",
			combination: Combination{Modifiers: ModControl | ModWin | ModNoRepeat, VirtualKey: 'X'},
			want:        []uint32{vkControl, vkLWin, vkRWin, 'X'},
		},
		{
			name:        "Ctrl+Alt+T",
			combination: Combination{Modifiers: ModControl | ModAlt | ModNoRepeat, VirtualKey: 'T'},
			want:        []uint32{vkControl, vkMenu, 'T'},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := releaseKeys(test.combination); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("releaseKeys() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestLaunchWaitsForChordRelease(t *testing.T) {
	t.Parallel()
	var pressed atomic.Bool
	pressed.Store(true)
	called := make(chan struct{})
	manager := NewManager(Combination{}, Combination{}, nil, nil)
	manager.stop = make(chan struct{})
	manager.keyDown = func(uint32) bool { return pressed.Load() }
	manager.poll = time.Millisecond
	manager.timeout = time.Second

	manager.launch(Combination{Modifiers: ModControl | ModAlt, VirtualKey: 'T'}, func() { close(called) })
	select {
	case <-called:
		t.Fatal("callback started while the trigger chord was pressed")
	case <-time.After(25 * time.Millisecond):
	}
	pressed.Store(false)
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("callback did not start after the trigger chord was released")
	}
	manager.handlers.Wait()
}

func TestLaunchStopsWaitingAtTimeout(t *testing.T) {
	t.Parallel()
	called := make(chan struct{})
	manager := NewManager(Combination{}, Combination{}, nil, nil)
	manager.stop = make(chan struct{})
	manager.keyDown = func(uint32) bool { return true }
	manager.poll = time.Millisecond
	manager.timeout = 20 * time.Millisecond

	manager.launch(Combination{Modifiers: ModControl | ModWin, VirtualKey: 'X'}, func() { close(called) })
	waited := make(chan struct{})
	go func() {
		manager.handlers.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(time.Second):
		t.Fatal("handler did not stop after the release timeout")
	}
	select {
	case <-called:
		t.Fatal("callback started although the trigger chord stayed pressed")
	default:
	}
}

func TestCloseStopsReleaseWait(t *testing.T) {
	t.Parallel()
	called := make(chan struct{})
	manager := NewManager(Combination{}, Combination{}, nil, nil)
	manager.started = true
	manager.done = make(chan struct{})
	close(manager.done)
	manager.stop = make(chan struct{})
	manager.keyDown = func(uint32) bool { return true }
	manager.poll = time.Millisecond
	manager.timeout = time.Hour

	manager.launch(Combination{Modifiers: ModControl | ModAlt, VirtualKey: 'T'}, func() { close(called) })
	closed := make(chan struct{})
	go func() {
		manager.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close blocked on a handler waiting for chord release")
	}
	select {
	case <-called:
		t.Fatal("callback started while the manager was closing")
	default:
	}
}

func TestGetAsyncKeyStateResolves(t *testing.T) {
	t.Parallel()
	if err := getAsyncKeyState.Find(); err != nil {
		t.Fatalf("resolve user32.dll GetAsyncKeyState: %v", err)
	}
}
