//go:build windows

package clipboard

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/sympllate/translator/internal/app"
)

const (
	cfUnicodeText  = 13
	gmemMoveable   = 0x0002
	keyeventfKeyup = 0x0002
	inputKeyboard  = 1
	vkControl      = 0x11
	vkC            = 0x43
	vkV            = 0x56
)

var (
	user32                     = syscall.NewLazyDLL("user32.dll")
	openClipboard              = user32.NewProc("OpenClipboard")
	closeClipboard             = user32.NewProc("CloseClipboard")
	emptyClipboard             = user32.NewProc("EmptyClipboard")
	getClipboardData           = user32.NewProc("GetClipboardData")
	setClipboardData           = user32.NewProc("SetClipboardData")
	isClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	getClipboardSequenceNumber = user32.NewProc("GetClipboardSequenceNumber")
	sendInput                  = user32.NewProc("SendInput")
	lstrlenW                   = user32.NewProc("lstrlenW")
	kernel32                   = syscall.NewLazyDLL("kernel32.dll")
	globalAlloc                = kernel32.NewProc("GlobalAlloc")
	globalLock                 = kernel32.NewProc("GlobalLock")
	globalUnlock               = kernel32.NewProc("GlobalUnlock")
	globalFree                 = kernel32.NewProc("GlobalFree")
	globalSize                 = kernel32.NewProc("GlobalSize")
	rtlMoveMemory              = kernel32.NewProc("RtlMoveMemory")
)

type Manager struct{ logger *log.Logger }

func New(logger *log.Logger) *Manager { return &Manager{logger: logger} }

func (m *Manager) CopySelection(ctx context.Context, wait time.Duration) (string, app.ClipboardSnapshot, error) {
	previous, err := m.snapshot(ctx)
	if err != nil {
		return "", app.ClipboardSnapshot{}, fmt.Errorf("сохранить буфер обмена: %w", err)
	}
	sequence, _, _ := getClipboardSequenceNumber.Call()
	if err := sendShortcut(vkC); err != nil {
		return "", previous, fmt.Errorf("эмулировать Ctrl+C: %w", err)
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	var selected string
	for {
		select {
		case <-ctx.Done():
			m.restoreBestEffort(context.Background(), previous)
			return "", previous, ctx.Err()
		case <-deadline.C:
			m.restoreBestEffort(ctx, previous)
			return "", previous, errors.New("не удалось получить выделенный текст: приложение не поддержало Ctrl+C или текст не выделен")
		case <-ticker.C:
			current, _, _ := getClipboardSequenceNumber.Call()
			if current == sequence {
				continue
			}
			value, hasText, readErr := m.read(ctx)
			if readErr != nil || !hasText || strings.TrimSpace(value) == "" {
				continue
			}
			selected = value
			m.restoreBestEffort(ctx, previous)
			return selected, previous, nil
		}
	}
}

func (m *Manager) PasteText(ctx context.Context, text string, previous app.ClipboardSnapshot) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("пустой перевод не будет вставлен")
	}
	if err := m.write(ctx, text); err != nil {
		return fmt.Errorf("поместить перевод в буфер обмена: %w", err)
	}
	if err := sendShortcut(vkV); err != nil {
		m.restoreBestEffort(ctx, previous)
		return fmt.Errorf("эмулировать Ctrl+V: %w", err)
	}
	timer := time.NewTimer(180 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		m.restoreBestEffort(context.Background(), previous)
		return ctx.Err()
	case <-timer.C:
	}
	m.restoreBestEffort(ctx, previous)
	return nil
}

func (m *Manager) WriteText(text string) error { return m.write(context.Background(), text) }

func (m *Manager) snapshot(ctx context.Context) (app.ClipboardSnapshot, error) {
	text, hasText, err := m.read(ctx)
	return app.ClipboardSnapshot{Text: text, HasText: hasText}, err
}

func (m *Manager) restoreBestEffort(ctx context.Context, snapshot app.ClipboardSnapshot) {
	if !snapshot.HasText {
		return
	}
	if err := m.write(ctx, snapshot.Text); err != nil {
		m.logger.Printf("clipboard restore failed: %v", err)
	}
}

func (m *Manager) read(ctx context.Context) (string, bool, error) {
	if err := openWithRetry(ctx); err != nil {
		return "", false, err
	}
	defer closeClipboard.Call()
	available, _, _ := isClipboardFormatAvailable.Call(cfUnicodeText)
	if available == 0 {
		return "", false, nil
	}
	handle, _, err := getClipboardData.Call(cfUnicodeText)
	if handle == 0 {
		return "", false, fmt.Errorf("GetClipboardData: %w", err)
	}
	pointer, _, err := globalLock.Call(handle)
	if pointer == 0 {
		return "", false, fmt.Errorf("GlobalLock: %w", err)
	}
	defer globalUnlock.Call(handle)
	size, _, _ := globalSize.Call(handle)
	if size < 2 {
		return "", true, nil
	}
	length, _, _ := lstrlenW.Call(pointer)
	if length > size/2 {
		return "", false, errors.New("некорректный Unicode-текст в буфере обмена")
	}
	values := make([]uint16, int(length))
	if len(values) > 0 {
		rtlMoveMemory.Call(uintptr(unsafe.Pointer(&values[0])), pointer, uintptr(len(values)*2))
	}
	return string(utf16.Decode(values)), true, nil
}

func (m *Manager) write(ctx context.Context, text string) error {
	values := utf16.Encode([]rune(text))
	values = append(values, 0)
	size := uintptr(len(values) * 2)
	handle, _, err := globalAlloc.Call(gmemMoveable, size)
	if handle == 0 {
		return fmt.Errorf("GlobalAlloc: %w", err)
	}
	owned := true
	defer func() {
		if owned {
			globalFree.Call(handle)
		}
	}()
	pointer, _, err := globalLock.Call(handle)
	if pointer == 0 {
		return fmt.Errorf("GlobalLock: %w", err)
	}
	rtlMoveMemory.Call(pointer, uintptr(unsafe.Pointer(&values[0])), size)
	globalUnlock.Call(handle)
	if err := openWithRetry(ctx); err != nil {
		return err
	}
	defer closeClipboard.Call()
	if result, _, callErr := emptyClipboard.Call(); result == 0 {
		return fmt.Errorf("EmptyClipboard: %w", callErr)
	}
	if result, _, callErr := setClipboardData.Call(cfUnicodeText, handle); result == 0 {
		return fmt.Errorf("SetClipboardData: %w", callErr)
	}
	owned = false
	return nil
}

func openWithRetry(ctx context.Context) error {
	for attempts := 0; attempts < 25; attempts++ {
		if result, _, _ := openClipboard.Call(0); result != 0 {
			return nil
		}
		timer := time.NewTimer(12 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return errors.New("буфер обмена занят другим приложением")
}

type input struct {
	Type uint32
	_    uint32
	Data [32]byte
}
type keyboardInput struct {
	VK        uint16
	Scan      uint16
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

func sendShortcut(key uint16) error {
	inputs := []input{keyboard(vkControl, 0), keyboard(key, 0), keyboard(key, keyeventfKeyup), keyboard(vkControl, keyeventfKeyup)}
	result, _, err := sendInput.Call(uintptr(len(inputs)), uintptr(unsafe.Pointer(&inputs[0])), unsafe.Sizeof(inputs[0]))
	if result != uintptr(len(inputs)) {
		return fmt.Errorf("SendInput отправил %d из %d событий: %w", result, len(inputs), err)
	}
	return nil
}

func keyboard(key uint16, flags uint32) input {
	var value input
	value.Type = inputKeyboard
	details := (*keyboardInput)(unsafe.Pointer(&value.Data[0]))
	details.VK = key
	details.Flags = flags
	return value
}
