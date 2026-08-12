package onnxruntime

import (
	"os"
	"path/filepath"
	"testing"

	ort "github.com/yalue/onnxruntime_go"
)

func TestSharedEnvironmentReferenceCounting(t *testing.T) {
	root := filepath.Join("..", "..", "dist", "portable")
	if _, err := os.Stat(DLLPath(root)); err != nil {
		t.Skipf("local ONNX Runtime unavailable: %v", err)
	}
	first, err := Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Acquire(root)
	if err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if !ort.IsInitialized() {
		t.Fatal("closing one lease destroyed the shared environment")
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if ort.IsInitialized() {
		t.Fatal("last lease did not destroy the shared environment")
	}
}
