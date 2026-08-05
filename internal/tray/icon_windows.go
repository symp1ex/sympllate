//go:build windows

package tray

import (
	_ "embed"
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

//go:embed assets/tray.ico
var trayIconData []byte

func createTrayIcon(width, height int) (uintptr, error) {
	frames, err := parseICO(trayIconData)
	if err != nil {
		return 0, fmt.Errorf("parse embedded tray icon: %w", err)
	}
	frame, err := selectIconFrame(frames, width, height)
	if err != nil {
		return 0, fmt.Errorf("select embedded tray icon: %w", err)
	}
	if len(frame.data) == 0 {
		return 0, errors.New("selected tray icon image is empty")
	}

	icon, _, callErr := createIconFromResourceEx.Call(
		uintptr(unsafe.Pointer(&frame.data[0])),
		uintptr(len(frame.data)),
		1,
		0x00030000,
		uintptr(width),
		uintptr(height),
		0,
	)
	runtime.KeepAlive(frame.data)
	if icon == 0 {
		if callErr == syscall.Errno(0) {
			return 0, errors.New("CreateIconFromResourceEx rejected the selected ICO image")
		}
		return 0, fmt.Errorf("CreateIconFromResourceEx rejected the selected ICO image: %w", callErr)
	}
	return icon, nil
}
