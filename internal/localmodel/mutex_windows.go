//go:build windows

package localmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

const errorAlreadyExists syscall.Errno = 183

var createMutexW = kernel32.NewProc("CreateMutexW")

type InstanceLock struct {
	handle syscall.Handle
	once   sync.Once
	err    error
}

func AcquireInstanceLock(executableDir string) (*InstanceLock, error) {
	identity := strings.ToLower(filepath.Clean(executableDir))
	sum := sha256.Sum256([]byte(identity))
	name, err := syscall.UTF16PtrFromString("Local\\SympllatePortable-" + hex.EncodeToString(sum[:16]))
	if err != nil {
		return nil, fmt.Errorf("сформировать имя single-instance mutex: %w", err)
	}
	result, _, callErr := createMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if result == 0 {
		return nil, fmt.Errorf("создать single-instance mutex: %w", callErr)
	}
	handle := syscall.Handle(result)
	if errors.Is(callErr, errorAlreadyExists) {
		_ = syscall.CloseHandle(handle)
		return nil, errors.New("Portable-версия Sympllate уже запущена из этого каталога")
	}
	return &InstanceLock{handle: handle}, nil
}

func (l *InstanceLock) Close() error {
	l.once.Do(func() {
		if l.handle != 0 {
			l.err = syscall.CloseHandle(l.handle)
			l.handle = 0
		}
	})
	return l.err
}
