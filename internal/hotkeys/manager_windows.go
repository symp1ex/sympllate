//go:build windows

package hotkeys

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	wmHotkey                   = 0x0312
	wmQuit                     = 0x0012
	showID                     = 1
	replaceID                  = 2
	vkShift             uint32 = 0x10
	vkControl           uint32 = 0x11
	vkMenu              uint32 = 0x12
	vkLWin              uint32 = 0x5B
	vkRWin              uint32 = 0x5C
	releasePollInterval        = 10 * time.Millisecond
	releaseTimeout             = 2 * time.Second
)

var (
	user32             = syscall.NewLazyDLL("user32.dll")
	registerHotKey     = user32.NewProc("RegisterHotKey")
	unregisterHotKey   = user32.NewProc("UnregisterHotKey")
	getMessage         = user32.NewProc("GetMessageW")
	postThreadMessage  = user32.NewProc("PostThreadMessageW")
	getAsyncKeyState   = user32.NewProc("GetAsyncKeyState")
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	getCurrentThreadID = kernel32.NewProc("GetCurrentThreadId")
)

type message struct {
	HWND    uintptr
	Message uint32
	_       uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Point   struct{ X, Y int32 }
	Private uint32
}

type Manager struct {
	show      Combination
	replace   Combination
	onShow    func()
	onReplace func()
	mu        sync.Mutex
	threadID  uintptr
	started   bool
	done      chan struct{}
	stop      chan struct{}
	handlers  sync.WaitGroup
	keyDown   func(uint32) bool
	poll      time.Duration
	timeout   time.Duration
}

func NewManager(show, replace Combination, onShow, onReplace func()) *Manager {
	return &Manager{
		show: show, replace: replace,
		onShow: onShow, onReplace: onReplace,
		keyDown: asyncKeyDown, poll: releasePollInterval, timeout: releaseTimeout,
	}
}

func (m *Manager) Start() error {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return nil
	}
	m.started = true
	m.done = make(chan struct{})
	m.stop = make(chan struct{})
	m.mu.Unlock()
	ready := make(chan error, 1)
	go m.run(ready)
	if err := <-ready; err != nil {
		m.mu.Lock()
		if m.started {
			m.started = false
			close(m.stop)
		}
		m.mu.Unlock()
		return err
	}
	return nil
}

func (m *Manager) run(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(m.done)
	threadID, _, _ := getCurrentThreadID.Call()
	m.mu.Lock()
	m.threadID = threadID
	m.mu.Unlock()
	if err := register(showID, m.show); err != nil {
		ready <- fmt.Errorf("register %s: %w", m.show.Display, err)
		return
	}
	defer unregisterHotKey.Call(0, showID)
	if err := register(replaceID, m.replace); err != nil {
		ready <- fmt.Errorf("register %s: %w", m.replace.Display, err)
		return
	}
	defer unregisterHotKey.Call(0, replaceID)
	ready <- nil
	var msg message
	for {
		result, _, callErr := getMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(result) == -1 {
			_ = callErr
			return
		}
		if result == 0 || msg.Message == wmQuit {
			return
		}
		if msg.Message != wmHotkey {
			continue
		}
		switch msg.WParam {
		case showID:
			m.launch(m.show, m.onShow)
		case replaceID:
			m.launch(m.replace, m.onReplace)
		}
	}
}

func (m *Manager) launch(combination Combination, callback func()) {
	m.mu.Lock()
	stop := m.stop
	m.mu.Unlock()
	m.handlers.Add(1)
	go func() {
		defer m.handlers.Done()
		if !waitForRelease(stop, releaseKeys(combination), m.keyDown, m.poll, m.timeout) {
			return
		}
		callback()
	}()
}

func releaseKeys(combination Combination) []uint32 {
	keys := make([]uint32, 0, 6)
	if combination.Modifiers&ModControl != 0 {
		keys = append(keys, vkControl)
	}
	if combination.Modifiers&ModAlt != 0 {
		keys = append(keys, vkMenu)
	}
	if combination.Modifiers&ModShift != 0 {
		keys = append(keys, vkShift)
	}
	if combination.Modifiers&ModWin != 0 {
		keys = append(keys, vkLWin, vkRWin)
	}
	return append(keys, combination.VirtualKey)
}

func waitForRelease(stop <-chan struct{}, keys []uint32, keyDown func(uint32) bool, poll, timeout time.Duration) bool {
	released := func() bool {
		for _, key := range keys {
			if keyDown(key) {
				return false
			}
		}
		return true
	}
	if released() {
		select {
		case <-stop:
			return false
		default:
			return true
		}
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case <-stop:
			return false
		case <-deadline.C:
			return false
		case <-ticker.C:
			if released() {
				select {
				case <-stop:
					return false
				default:
					return true
				}
			}
		}
	}
}

func asyncKeyDown(key uint32) bool {
	state, _, _ := getAsyncKeyState.Call(uintptr(key))
	return uint16(state)&0x8000 != 0
}

func register(id uintptr, combination Combination) error {
	result, _, err := registerHotKey.Call(0, id, uintptr(combination.Modifiers), uintptr(combination.VirtualKey))
	if result == 0 {
		return fmt.Errorf("hotkey is already registered or unavailable: %w", err)
	}
	return nil
}

func (m *Manager) Close() {
	m.mu.Lock()
	threadID, started, done := m.threadID, m.started, m.done
	m.started = false
	if started {
		close(m.stop)
	}
	m.mu.Unlock()
	if started && threadID != 0 {
		postThreadMessage.Call(threadID, wmQuit, 0, 0)
	}
	if started && done != nil {
		<-done
	}
	m.handlers.Wait()
}
