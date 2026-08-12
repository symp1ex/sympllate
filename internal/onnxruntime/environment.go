package onnxruntime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

const (
	Version = "1.26.0"
	DLLName = "onnxruntime.dll"
)

var state struct {
	sync.Mutex
	path string
	refs int
}

type Lease struct {
	once sync.Once
}

func DLLPath(executableDir string) string {
	return filepath.Join(executableDir, "runtime", "onnx", DLLName)
}

func Acquire(executableDir string) (*Lease, error) {
	path := DLLPath(executableDir)
	if err := requireRegularFile(path); err != nil {
		return nil, fmt.Errorf("ONNX Runtime missing at %q: %w", path, err)
	}
	state.Lock()
	defer state.Unlock()
	if state.refs > 0 {
		if !samePath(state.path, path) {
			return nil, fmt.Errorf("ONNX Runtime already initialized from a different location")
		}
		state.refs++
		return &Lease{}, nil
	}
	if ort.IsInitialized() {
		return nil, errors.New("ONNX Runtime environment was initialized outside the shared lifecycle")
	}
	ort.SetSharedLibraryPath(path)
	if err := ort.InitializeEnvironment(ort.WithLogLevelError()); err != nil {
		return nil, fmt.Errorf("initialize ONNX Runtime: %w", err)
	}
	if version := ort.GetVersion(); version != Version {
		_ = ort.DestroyEnvironment()
		return nil, fmt.Errorf("ONNX Runtime version unsupported: got %q, expected %s", version, Version)
	}
	state.path, state.refs = path, 1
	return &Lease{}, nil
}

func (l *Lease) Close() (err error) {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		state.Lock()
		defer state.Unlock()
		if state.refs <= 0 {
			return
		}
		state.refs--
		if state.refs == 0 {
			err = ort.DestroyEnvironment()
			state.path = ""
		}
	})
	return err
}

func requireRegularFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("path is not a regular file")
	}
	return nil
}

func samePath(left, right string) bool {
	a, _ := filepath.Abs(left)
	b, _ := filepath.Abs(right)
	return filepath.Clean(a) == filepath.Clean(b)
}
