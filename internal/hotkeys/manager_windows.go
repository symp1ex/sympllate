//go:build windows

package hotkeys

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

const (
	wmHotkey  = 0x0312
	wmQuit    = 0x0012
	showID    = 1
	replaceID = 2
)

var (
	user32             = syscall.NewLazyDLL("user32.dll")
	registerHotKey     = user32.NewProc("RegisterHotKey")
	unregisterHotKey   = user32.NewProc("UnregisterHotKey")
	getMessage         = user32.NewProc("GetMessageW")
	postThreadMessage  = user32.NewProc("PostThreadMessageW")
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
	handlers  sync.WaitGroup
}

func NewManager(show, replace Combination, onShow, onReplace func()) *Manager {
	return &Manager{show: show, replace: replace, onShow: onShow, onReplace: onReplace}
}

func (m *Manager) Start() error {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return nil
	}
	m.started = true
	m.done = make(chan struct{})
	m.mu.Unlock()
	ready := make(chan error, 1)
	go m.run(ready)
	if err := <-ready; err != nil {
		m.mu.Lock()
		m.started = false
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
		ready <- fmt.Errorf("зарегистрировать %s: %w", m.show.Display, err)
		return
	}
	defer unregisterHotKey.Call(0, showID)
	if err := register(replaceID, m.replace); err != nil {
		ready <- fmt.Errorf("зарегистрировать %s: %w", m.replace.Display, err)
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
			m.launch(m.onShow)
		case replaceID:
			m.launch(m.onReplace)
		}
	}
}

func (m *Manager) launch(callback func()) {
	m.handlers.Add(1)
	go func() {
		defer m.handlers.Done()
		callback()
	}()
}

func register(id uintptr, combination Combination) error {
	result, _, err := registerHotKey.Call(0, id, uintptr(combination.Modifiers), uintptr(combination.VirtualKey))
	if result == 0 {
		return fmt.Errorf("комбинация уже занята или недоступна: %w", err)
	}
	return nil
}

func (m *Manager) Close() {
	m.mu.Lock()
	threadID, started, done := m.threadID, m.started, m.done
	m.started = false
	m.mu.Unlock()
	if started && threadID != 0 {
		postThreadMessage.Call(threadID, wmQuit, 0, 0)
	}
	if started && done != nil {
		<-done
	}
	m.handlers.Wait()
}
