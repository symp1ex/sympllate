//go:build windows

package window

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const (
	wmSetIcon = 0x0080

	iconSmall = 0
	iconBig   = 1

	shgfiIcon      = 0x000000100
	shgfiLargeIcon = 0x000000000
	shgfiSmallIcon = 0x000000001
)

type shellFileInfo struct {
	icon        uintptr
	iconIndex   int32
	attributes  uint32
	displayName [260]uint16
	typeName    [80]uint16
}

// taskbarIcons owns the handles returned by SHGetFileInfoW. Windows keeps
// using them after WM_SETICON, so they must outlive the associated HWND.
type taskbarIcons struct {
	big   uintptr
	small uintptr
}

var (
	taskbarShell32        = syscall.NewLazyDLL("shell32.dll")
	taskbarSHGetFileInfoW = taskbarShell32.NewProc("SHGetFileInfoW")
	taskbarDestroyIcon    = windowUser32.NewProc("DestroyIcon")
)

func loadExeShellIcon(small bool) (uintptr, error) {
	executable, err := os.Executable()
	if err != nil {
		return 0, fmt.Errorf("get executable path: %w", err)
	}
	executablePtr, err := syscall.UTF16PtrFromString(executable)
	if err != nil {
		return 0, fmt.Errorf("encode executable path: %w", err)
	}

	var info shellFileInfo
	flags := uintptr(shgfiIcon | shgfiLargeIcon)
	if small {
		flags = shgfiIcon | shgfiSmallIcon
	}
	result, _, callErr := taskbarSHGetFileInfoW.Call(
		uintptr(unsafe.Pointer(executablePtr)),
		0,
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
		flags,
	)
	if result == 0 || info.icon == 0 {
		if callErr == nil || callErr == syscall.Errno(0) {
			return 0, errors.New("SHGetFileInfoW returned no icon")
		}
		return 0, fmt.Errorf("SHGetFileInfoW failed: %w", callErr)
	}
	return info.icon, nil
}

func setTaskbarIcon(hwnd uintptr) (*taskbarIcons, error) {
	if hwnd == 0 {
		return nil, errors.New("window HWND is empty")
	}

	bigIcon, err := loadExeShellIcon(false)
	if err != nil {
		return nil, fmt.Errorf("load large executable shell icon: %w", err)
	}
	smallIcon, err := loadExeShellIcon(true)
	if err != nil {
		taskbarDestroyIcon.Call(bigIcon)
		return nil, fmt.Errorf("load small executable shell icon: %w", err)
	}

	chromeSendMessage.Call(hwnd, wmSetIcon, iconBig, bigIcon)
	chromeSendMessage.Call(hwnd, wmSetIcon, iconSmall, smallIcon)
	return &taskbarIcons{big: bigIcon, small: smallIcon}, nil
}

func (icons *taskbarIcons) destroy() {
	if icons == nil {
		return
	}
	bigIcon, smallIcon := icons.big, icons.small
	icons.big = 0
	icons.small = 0
	if bigIcon != 0 {
		taskbarDestroyIcon.Call(bigIcon)
	}
	if smallIcon != 0 && smallIcon != bigIcon {
		taskbarDestroyIcon.Call(smallIcon)
	}
}
