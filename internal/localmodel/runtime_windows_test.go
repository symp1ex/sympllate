//go:build windows

package localmodel

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sympllate/translator/internal/translation"
)

type configuredImageExtractor struct{}

func (configuredImageExtractor) Capability() translation.ImageCapability {
	return translation.ImageCapability{Supported: true}
}
func (configuredImageExtractor) Recognize(context.Context, translation.ValidatedImage, string) (string, error) {
	return "configured", nil
}

func TestWaitForReady(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer key" {
			t.Errorf("missing health authorization")
		}
		if requests.Add(1) < 3 {
			http.Error(w, "loading", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	done := make(chan struct{})
	err := waitForReady(t.Context(), server.Client(), server.URL, "key", done, func() error { return nil }, time.Second, time.Millisecond)
	if err != nil || requests.Load() < 3 {
		t.Fatalf("waitForReady() requests=%d, err=%v", requests.Load(), err)
	}
}

func TestWaitForReadyDetectsEarlyExit(t *testing.T) {
	t.Parallel()
	done := make(chan struct{})
	close(done)
	err := waitForReady(t.Context(), &http.Client{Timeout: time.Millisecond}, "http://127.0.0.1:1/health", "key", done, func() error { return errors.New("exit 2") }, time.Second, time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "exit 2") {
		t.Fatalf("waitForReady() error = %v", err)
	}
}

func TestWaitForReadyTimeout(t *testing.T) {
	t.Parallel()
	done := make(chan struct{})
	err := waitForReady(t.Context(), &http.Client{Timeout: time.Millisecond}, "http://127.0.0.1:1/health", "key", done, func() error { return nil }, 20*time.Millisecond, time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("waitForReady() error = %v", err)
	}
}

type fakeProcess struct {
	done      chan struct{}
	closeOnce sync.Once
	stops     atomic.Int32
}

func (p *fakeProcess) Wait() error {
	<-p.done
	return nil
}

func (p *fakeProcess) Stop() error {
	p.stops.Add(1)
	p.closeOnce.Do(func() { close(p.done) })
	return nil
}

func TestRuntimeCloseIsIdempotent(t *testing.T) {
	t.Parallel()
	process := &fakeProcess{done: make(chan struct{})}
	runtime := newRuntime(process)
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if process.stops.Load() != 1 {
		t.Fatalf("Stop calls = %d, want 1", process.stops.Load())
	}
}

func TestRuntimeUsesConfiguredImageExtractor(t *testing.T) {
	t.Parallel()
	process := &fakeProcess{done: make(chan struct{})}
	runtime := newRuntime(process)
	runtime.client = NewClientWithImageTextExtractor("http://127.0.0.1", "key", 10, 0, 100, time.Second, configuredImageExtractor{})
	if !runtime.client.ImageCapability().Supported {
		t.Fatal("configured OCR extractor was not attached to local image client")
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
}
