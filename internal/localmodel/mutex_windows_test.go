//go:build windows

package localmodel

import "testing"

func TestInstanceLockPreventsDuplicate(t *testing.T) {
	directory := t.TempDir()
	first, err := AcquireInstanceLock(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := AcquireInstanceLock(directory); err == nil {
		t.Fatal("second AcquireInstanceLock() expected error")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := AcquireInstanceLock(directory)
	if err != nil {
		t.Fatalf("AcquireInstanceLock() after release: %v", err)
	}
	_ = second.Close()
}
