package inpaint

import (
	"context"
	"image"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeSessionRunner struct {
	runs       atomic.Int32
	active     atomic.Int32
	maximum    atomic.Int32
	destroyed  atomic.Int32
	runLatency time.Duration
}

func (f *fakeSessionRunner) Run(context.Context) error {
	f.runs.Add(1)
	active := f.active.Add(1)
	for maximum := f.maximum.Load(); active > maximum && !f.maximum.CompareAndSwap(maximum, active); maximum = f.maximum.Load() {
	}
	time.Sleep(f.runLatency)
	f.active.Add(-1)
	return nil
}

func (f *fakeSessionRunner) Destroy() error { f.destroyed.Add(1); return nil }

func TestNewEngineReportsMissingRuntimeAndModelPaths(t *testing.T) {
	directory := t.TempDir()
	_, err := NewEngine(directory)
	if err == nil || !strings.Contains(err.Error(), filepath.Join("bin", "inpaint", runtimeName)) {
		t.Fatalf("runtime error=%v", err)
	}
	inpaintDirectory := filepath.Join(directory, "bin", "inpaint")
	if err := os.MkdirAll(inpaintDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inpaintDirectory, runtimeName), []byte("not loaded"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = NewEngine(directory)
	if err == nil || !strings.Contains(err.Error(), filepath.Join("bin", "inpaint", modelName)) {
		t.Fatalf("model error=%v", err)
	}
}

func TestCancellationBeforeInferenceDoesNotRunSession(t *testing.T) {
	session := &fakeSessionRunner{}
	engine := &runtimeEngine{
		gate:       make(chan struct{}, 1),
		session:    session,
		imageData:  make([]float32, modelSize*modelSize*3),
		maskData:   make([]float32, modelSize*modelSize),
		outputData: make([]float32, modelSize*modelSize*3),
	}
	engine.gate <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := engine.Inpaint(ctx, image.NewNRGBA(image.Rect(0, 0, 4, 4)), image.NewGray(image.Rect(0, 0, 4, 4)))
	if err == nil || session.runs.Load() != 0 {
		t.Fatalf("err=%v runs=%d", err, session.runs.Load())
	}
}

func TestSharedSessionSerializesInferenceAndClosesOnce(t *testing.T) {
	session := &fakeSessionRunner{runLatency: 10 * time.Millisecond}
	engine := &runtimeEngine{
		gate:       make(chan struct{}, 1),
		session:    session,
		imageData:  make([]float32, modelSize*modelSize*3),
		maskData:   make([]float32, modelSize*modelSize),
		outputData: make([]float32, modelSize*modelSize*3),
	}
	engine.gate <- struct{}{}
	source := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	mask := image.NewGray(source.Bounds())
	var wait sync.WaitGroup
	errors := make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := engine.Inpaint(context.Background(), source, mask)
			errors <- err
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if session.runs.Load() != 2 || session.maximum.Load() != 1 {
		t.Fatalf("runs=%d maximum_concurrent=%d", session.runs.Load(), session.maximum.Load())
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
	if session.destroyed.Load() != 1 {
		t.Fatalf("session destroyed %d times", session.destroyed.Load())
	}
}
