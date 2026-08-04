//go:build windows

package clipboard

import (
	"runtime"
	"testing"
	"unsafe"
)

func TestLstrlenWResolves(t *testing.T) {
	t.Parallel()
	if err := lstrlenW.Find(); err != nil {
		t.Fatalf("resolve kernel32.dll lstrlenW: %v", err)
	}
}

func TestInputLayoutAMD64(t *testing.T) {
	t.Parallel()
	if runtime.GOARCH != "amd64" {
		t.Skip("layout assertion is specific to Windows amd64")
	}
	if got := unsafe.Sizeof(input{}); got != 40 {
		t.Fatalf("sizeof(INPUT) = %d, want 40", got)
	}
	if got := unsafe.Offsetof(input{}.Data); got != 8 {
		t.Fatalf("offsetof(INPUT union) = %d, want 8", got)
	}
	if got := unsafe.Sizeof(keyboardInput{}); got != 24 {
		t.Fatalf("sizeof(KEYBDINPUT) = %d, want 24", got)
	}
}
