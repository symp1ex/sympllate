//go:build windows

package tray

import (
	"syscall"
	"testing"
)

func TestNotificationDataUsesTooltip(t *testing.T) {
	tray := &Tray{tooltip: "Sympllate v0.1.1.1"}

	data, err := tray.notificationData()
	if err != nil {
		t.Fatalf("notificationData() error = %v", err)
	}
	if got := syscall.UTF16ToString(data.tip[:]); got != tray.tooltip {
		t.Fatalf("notificationData() tooltip = %q, want %q", got, tray.tooltip)
	}
}
