//go:build windows

package clipboard

import "testing"

func TestLstrlenWResolves(t *testing.T) {
	t.Parallel()
	if err := lstrlenW.Find(); err != nil {
		t.Fatalf("resolve kernel32.dll lstrlenW: %v", err)
	}
}
