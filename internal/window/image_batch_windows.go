//go:build windows

package window

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"syscall"
	"unicode/utf16"
	"unsafe"

	webview "github.com/jchv/go-webview2"
	"github.com/sympllate/translator/internal/imagebatch"
)

var (
	comdlg32             = syscall.NewLazyDLL("comdlg32.dll")
	getOpenFileNameW     = comdlg32.NewProc("GetOpenFileNameW")
	commDlgExtendedError = comdlg32.NewProc("CommDlgExtendedError")
	shell32Dialog        = syscall.NewLazyDLL("shell32.dll")
	shBrowseForFolderW   = shell32Dialog.NewProc("SHBrowseForFolderW")
	shGetPathFromIDListW = shell32Dialog.NewProc("SHGetPathFromIDListW")
	ole32Dialog          = syscall.NewLazyDLL("ole32.dll")
	coInitializeExDialog = ole32Dialog.NewProc("CoInitializeEx")
	coUninitializeDialog = ole32Dialog.NewProc("CoUninitialize")
	coTaskMemFreeDialog  = ole32Dialog.NewProc("CoTaskMemFree")
)

const (
	ofnAllowMultiSelect = 0x00000200
	ofnExplorer         = 0x00080000
	ofnFileMustExist    = 0x00001000
	ofnPathMustExist    = 0x00000800
	ofnHideReadOnly     = 0x00000004
	ofnDontAddToRecent  = 0x02000000
	bifReturnOnlyFSDirs = 0x0001
	bifNewDialogStyle   = 0x0040
	coinitApartment     = 0x2
	rpcEChangedMode     = 0x80010106
)

type openFileName struct {
	structSize       uint32
	owner            uintptr
	instance         uintptr
	filter           *uint16
	customFilter     *uint16
	maxCustomFilter  uint32
	filterIndex      uint32
	file             *uint16
	maxFile          uint32
	fileTitle        *uint16
	maxFileTitle     uint32
	initialDirectory *uint16
	title            *uint16
	flags            uint32
	fileOffset       uint16
	fileExtension    uint16
	defaultExtension *uint16
	customData       uintptr
	hook             uintptr
	templateName     *uint16
	reserved         uintptr
	reservedFlags    uint32
	flagsEx          uint32
}

type browseInfo struct {
	owner       uintptr
	root        uintptr
	displayName *uint16
	title       *uint16
	flags       uint32
	callback    uintptr
	parameter   uintptr
	image       int32
}

func bindImageBatch(w webview.WebView, hwnd uintptr, service *imagebatch.Service) error {
	if service == nil {
		return errors.New("image batch service is unavailable")
	}
	bindings := []struct {
		name string
		fn   any
	}{
		{"SelectBatchImageFiles", func() (imagebatch.BatchSelection, error) {
			paths, cancelled, err := selectBatchImageFiles(hwnd)
			if err != nil {
				return imagebatch.BatchSelection{}, err
			}
			if cancelled {
				return imagebatch.BatchSelection{Kind: imagebatch.SelectionFiles}, nil
			}
			return service.SelectFiles(paths)
		}},
		{"SelectBatchImageDirectory", func() (imagebatch.BatchSelection, error) {
			path, cancelled, err := selectBatchImageDirectory(hwnd)
			if err != nil {
				return imagebatch.BatchSelection{}, err
			}
			if cancelled {
				return imagebatch.BatchSelection{Kind: imagebatch.SelectionDirectory}, nil
			}
			return service.SelectDirectory(path)
		}},
		{"StartImageBatch", func(request imagebatch.StartImageBatchRequest) (string, error) { return service.Start(request) }},
		{"GetImageBatchStatus", func(id string) (imagebatch.ImageBatchStatus, error) { return service.Status(id) }},
		{"CancelImageBatch", func(id string) error { return service.Cancel(id) }},
	}
	for _, binding := range bindings {
		if err := w.Bind(binding.name, binding.fn); err != nil {
			return fmt.Errorf("create binding %s: %w", binding.name, err)
		}
	}
	return nil
}

func bindImageBatchLauncher(w webview.WebView, batchWindow *ImageBatchWindow) error {
	if batchWindow == nil {
		return errors.New("image batch window is unavailable")
	}
	if err := w.Bind("OpenImageBatchWindow", func() { batchWindow.Open() }); err != nil {
		return fmt.Errorf("create binding OpenImageBatchWindow: %w", err)
	}
	return nil
}

func selectBatchImageFiles(owner uintptr) ([]string, bool, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	buffer := make([]uint16, 65536)
	filter := utf16.Encode([]rune("Images (*.png;*.jpg;*.jpeg;*.webp;*.tif;*.tiff;*.bmp)\x00*.png;*.jpg;*.jpeg;*.webp;*.tif;*.tiff;*.bmp\x00"))
	filter = append(filter, 0)
	title, _ := syscall.UTF16PtrFromString("Select images")
	dialog := openFileName{
		owner: owner, filter: &filter[0], filterIndex: 1, file: &buffer[0], maxFile: uint32(len(buffer)), title: title,
		flags: ofnAllowMultiSelect | ofnExplorer | ofnFileMustExist | ofnPathMustExist | ofnHideReadOnly | ofnDontAddToRecent,
	}
	dialog.structSize = uint32(unsafe.Sizeof(dialog))
	result, _, _ := getOpenFileNameW.Call(uintptr(unsafe.Pointer(&dialog)))
	if result == 0 {
		code, _, _ := commDlgExtendedError.Call()
		if code == 0 {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("open image file dialog failed: Windows error 0x%x", code)
	}
	parts := splitUTF16Buffer(buffer)
	if len(parts) == 1 {
		return []string{parts[0]}, false, nil
	}
	paths := make([]string, 0, len(parts)-1)
	for _, name := range parts[1:] {
		paths = append(paths, filepath.Join(parts[0], name))
	}
	return paths, false, nil
}

func selectBatchImageDirectory(owner uintptr) (string, bool, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := coInitializeExDialog.Call(0, coinitApartment)
	initialized := hr == 0 || hr == 1
	if initialized {
		defer coUninitializeDialog.Call()
	} else if hr != rpcEChangedMode {
		return "", false, fmt.Errorf("initialize folder dialog: HRESULT 0x%x", hr)
	}
	display := make([]uint16, 260)
	title, _ := syscall.UTF16PtrFromString("Select a directory containing images")
	info := browseInfo{owner: owner, displayName: &display[0], title: title, flags: bifReturnOnlyFSDirs | bifNewDialogStyle}
	item, _, _ := shBrowseForFolderW.Call(uintptr(unsafe.Pointer(&info)))
	if item == 0 {
		return "", true, nil
	}
	defer coTaskMemFreeDialog.Call(item)
	path := make([]uint16, 32768)
	ok, _, callErr := shGetPathFromIDListW.Call(item, uintptr(unsafe.Pointer(&path[0])))
	if ok == 0 {
		return "", false, fmt.Errorf("read selected directory: %w", callErr)
	}
	return syscall.UTF16ToString(path), false, nil
}

func splitUTF16Buffer(buffer []uint16) []string {
	parts := make([]string, 0, 4)
	start := 0
	for index, value := range buffer {
		if value != 0 {
			continue
		}
		if index == start {
			break
		}
		parts = append(parts, string(utf16.Decode(buffer[start:index])))
		start = index + 1
	}
	return parts
}
