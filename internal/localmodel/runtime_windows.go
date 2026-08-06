//go:build windows

package localmodel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/sympllate/translator/internal/ocr"
)

type RuntimeConfig struct {
	Layout             Layout
	ExecutableDir      string
	StartupTimeout     time.Duration
	RequestTimeout     time.Duration
	NumCtx             int
	NumPredict         int
	Temperature        float64
	FitTargetMiB       int
	MaxInputCharacters int
}

type managedProcess interface {
	Wait() error
	Stop() error
}

type processStarter func(executable string, args []string, directory string, output io.Writer) (managedProcess, error)

type Runtime struct {
	client  *Client
	process managedProcess
	done    chan struct{}

	waitMu  sync.Mutex
	waitErr error

	closeOnce sync.Once
	closeErr  error
}

func Start(ctx context.Context, cfg RuntimeConfig, output io.Writer) (*Runtime, error) {
	return startWith(ctx, cfg, output, startProcess)
}

func startWith(ctx context.Context, cfg RuntimeConfig, output io.Writer, starter processStarter) (*Runtime, error) {
	if cfg.StartupTimeout <= 0 || cfg.RequestTimeout <= 0 || cfg.NumCtx <= 0 || cfg.NumPredict <= 0 || cfg.FitTargetMiB <= 0 || cfg.MaxInputCharacters <= 0 {
		return nil, errors.New("invalid local model configuration")
	}
	port, err := freeLoopbackPort()
	if err != nil {
		return nil, err
	}
	apiKey, err := randomAPIKey()
	if err != nil {
		return nil, err
	}
	args := BuildArguments(cfg.Layout, port, apiKey, cfg.NumCtx, cfg.FitTargetMiB)
	process, err := starter(cfg.Layout.ServerPath, args, cfg.Layout.RuntimeDir, output)
	if err != nil {
		return nil, fmt.Errorf("start llama-server: %w", err)
	}
	runtime := newRuntime(process)
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	healthClient := &http.Client{Timeout: 2 * time.Second}
	err = waitForReady(ctx, healthClient, baseURL+"/health", apiKey, runtime.done, runtime.processError, cfg.StartupTimeout, 100*time.Millisecond)
	if err != nil {
		closeErr := runtime.Close()
		if closeErr != nil {
			return nil, fmt.Errorf("llama-server is not ready: %w (shutdown error: %v)", err, closeErr)
		}
		return nil, fmt.Errorf("llama-server is not ready: %w", err)
	}
	var extractor ImageTextExtractor
	if cfg.ExecutableDir != "" {
		extractor = ocr.New(cfg.ExecutableDir, ocr.DefaultTimeout)
	}
	runtime.client = NewClientWithImageTextExtractor(
		baseURL, apiKey, cfg.NumPredict, cfg.Temperature, cfg.MaxInputCharacters, cfg.RequestTimeout, extractor,
	)
	return runtime, nil
}

func newRuntime(process managedProcess) *Runtime {
	runtime := &Runtime{process: process, done: make(chan struct{})}
	go func() {
		err := process.Wait()
		runtime.waitMu.Lock()
		runtime.waitErr = err
		runtime.waitMu.Unlock()
		close(runtime.done)
	}()
	return runtime
}

func (r *Runtime) Client() *Client { return r.client }

func (r *Runtime) Close() error {
	r.closeOnce.Do(func() {
		stopErr := r.process.Stop()
		select {
		case <-r.done:
			if stopErr != nil && !errors.Is(stopErr, os.ErrProcessDone) {
				r.closeErr = stopErr
			}
		case <-time.After(10 * time.Second):
			r.closeErr = errors.New("llama-server did not exit after shutdown")
		}
	})
	return r.closeErr
}

func (r *Runtime) processError() error {
	r.waitMu.Lock()
	defer r.waitMu.Unlock()
	return r.waitErr
}

func waitForReady(ctx context.Context, client *http.Client, healthURL, apiKey string, processDone <-chan struct{}, processError func() error, timeout, interval time.Duration) error {
	startupContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		request, err := http.NewRequestWithContext(startupContext, http.MethodGet, healthURL, nil)
		if err != nil {
			return fmt.Errorf("create health request: %w", err)
		}
		request.Header.Set("Authorization", "Bearer "+apiKey)
		response, requestErr := client.Do(request)
		if requestErr == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
			_ = response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				return nil
			}
		}

		select {
		case <-startupContext.Done():
			if errors.Is(startupContext.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
				return fmt.Errorf("startup timed out after %s", timeout)
			}
			return startupContext.Err()
		case <-processDone:
			if err := processError(); err != nil {
				return fmt.Errorf("process exited before becoming ready: %w", err)
			}
			return errors.New("process exited before becoming ready")
		case <-ticker.C:
		}
	}
}

func freeLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("select loopback port: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		return 0, fmt.Errorf("release loopback port: %w", err)
	}
	return port, nil
}

func randomAPIKey() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("create local server API key: %w", err)
	}
	return hex.EncodeToString(value), nil
}

type windowsProcess struct {
	cmd  *exec.Cmd
	job  syscall.Handle
	once sync.Once
	err  error
}

func startProcess(executable string, args []string, directory string, output io.Writer) (managedProcess, error) {
	cmd := exec.Command(executable, args...)
	cmd.Dir = directory
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	job, err := createKillOnCloseJob(cmd.Process.Pid)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}
	return &windowsProcess{cmd: cmd, job: job}, nil
}

func (p *windowsProcess) Wait() error { return p.cmd.Wait() }

func (p *windowsProcess) Stop() error {
	p.once.Do(func() {
		if p.job != 0 {
			p.err = syscall.CloseHandle(p.job)
			p.job = 0
		}
		if p.err != nil {
			p.err = p.cmd.Process.Kill()
		}
	})
	return p.err
}

const (
	jobObjectExtendedLimitInformation = 9
	jobObjectLimitKillOnJobClose      = 0x00002000
	processSetQuota                   = 0x0100
	processTerminate                  = 0x0001
)

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	createJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	setInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	assignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
)

type jobObjectBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobObjectExtendedLimitInformationStruct struct {
	BasicLimitInformation jobObjectBasicLimitInformation
	IOInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

func createKillOnCloseJob(processID int) (syscall.Handle, error) {
	jobResult, _, callErr := createJobObjectW.Call(0, 0)
	if jobResult == 0 {
		return 0, fmt.Errorf("create Windows Job Object: %w", callErr)
	}
	job := syscall.Handle(jobResult)
	info := jobObjectExtendedLimitInformationStruct{}
	info.BasicLimitInformation.LimitFlags = jobObjectLimitKillOnJobClose
	result, _, callErr := setInformationJobObject.Call(
		uintptr(job),
		jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
	)
	if result == 0 {
		_ = syscall.CloseHandle(job)
		return 0, fmt.Errorf("configure Windows Job Object: %w", callErr)
	}
	process, err := syscall.OpenProcess(processSetQuota|processTerminate, false, uint32(processID))
	if err != nil {
		_ = syscall.CloseHandle(job)
		return 0, fmt.Errorf("open llama-server for the Job Object: %w", err)
	}
	defer syscall.CloseHandle(process)
	result, _, callErr = assignProcessToJobObject.Call(uintptr(job), uintptr(process))
	if result == 0 {
		_ = syscall.CloseHandle(job)
		return 0, fmt.Errorf("add llama-server to the Windows Job Object: %w", callErr)
	}
	return job, nil
}
