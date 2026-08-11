package inpaint

import (
	"context"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"
)

func TestLaMaIntegration(t *testing.T) {
	if os.Getenv("SYMPLLATE_RUN_INPAINT_INTEGRATION") != "1" {
		t.Skip("set SYMPLLATE_RUN_INPAINT_INTEGRATION=1 to run the local LaMa smoke test")
	}
	executableDir := os.Getenv("SYMPLLATE_INPAINT_EXECUTABLE_DIR")
	if executableDir == "" {
		executableDir = filepath.Join("..", "..", "dist", "portable")
	}
	if _, err := os.Stat(filepath.Join(executableDir, "bin", "inpaint", modelName)); err != nil {
		t.Skipf("local LaMa model is unavailable: %v", err)
	}
	engine, err := NewEngine(executableDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := engine.Close(); err != nil {
			t.Errorf("close engine: %v", err)
		}
	}()
	source := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	mask := image.NewGray(source.Bounds())
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			source.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 7), G: uint8(y * 7), B: 100, A: 255})
		}
	}
	for y := 12; y < 20; y++ {
		for x := 12; x < 20; x++ {
			mask.SetGray(x, y, color.Gray{Y: 255})
		}
	}
	result, err := engine.Inpaint(context.Background(), source, mask)
	if err != nil {
		t.Fatal(err)
	}
	if result.Image.Bounds() != source.Bounds() || result.Image.NRGBAAt(0, 0) != source.NRGBAAt(0, 0) {
		t.Fatalf("invalid result bounds=%v outside=%+v", result.Image.Bounds(), result.Image.NRGBAAt(0, 0))
	}
}
