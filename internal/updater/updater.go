package updater

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sympllate/translator/internal/config"
)

const (
	executableName = "updater-sl.exe"
	defaultTimeout = 2 * time.Minute
	exitDelay      = 2 * time.Second
)

var (
	checkArgs      = []string{"--check"}
	DefaultService = NewService()
)

type Paths struct {
	AppDir             string
	ApplicationExe     string
	ApplicationExeName string
	UpdaterDir         string
	UpdaterExe         string
}

type CheckResult struct {
	OK              bool   `json:"ok"`
	UpdateAvailable bool   `json:"updateAvailable"`
	Message         string `json:"message,omitempty"`
}

type InstallResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

type processOutput struct {
	Stdout string
	Stderr string
}

type processHandle interface {
	PID() int
	Release() error
}

type Logger interface {
	Debugf(format string, args ...any)
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
}

type noopLogger struct{}

func (noopLogger) Debugf(string, ...any) {}
func (noopLogger) Infof(string, ...any)  {}
func (noopLogger) Warnf(string, ...any)  {}
func (noopLogger) Errorf(string, ...any) {}

type Service struct {
	mu       sync.Mutex
	checking bool

	timeout      time.Duration
	resolvePaths func() (Paths, error)
	runCheck     func(context.Context, Paths, []string) (processOutput, error)
	startUpgrade func(Paths, []string) (processHandle, error)
	scheduleExit func(time.Duration)
}

var logSink Logger = noopLogger{}

func NewService() *Service {
	return &Service{
		timeout:      defaultTimeout,
		resolvePaths: ResolvePaths,
		runCheck:     runCheckProcess,
		startUpgrade: startUpgradeProcess,
		scheduleExit: scheduleApplicationExit,
	}
}

func SetLogger(logger Logger) {
	if logger == nil {
		logSink = noopLogger{}
		return
	}
	logSink = logger
}

func ResolvePaths() (Paths, error) {
	applicationExecutable, err := os.Executable()
	if err != nil {
		logSink.Errorf("[Updater] Failed to resolve application executable path: %v", err)
		return Paths{}, fmt.Errorf("resolve application executable: %w", err)
	}

	// A broken or inaccessible symlink must not make a valid os.Executable path
	// unusable. Keep the original absolute path and record the normalization error.
	if resolvedExecutable, resolveErr := filepath.EvalSymlinks(applicationExecutable); resolveErr == nil {
		applicationExecutable = resolvedExecutable
	} else {
		logSink.Warnf("[Updater] Failed to resolve application executable symlinks; using original path: exe=%s error=%v", applicationExecutable, resolveErr)
	}

	appDir := filepath.Dir(applicationExecutable)
	applicationExeName := filepath.Base(applicationExecutable)
	updaterDir := filepath.Join(appDir, "updater")
	updaterExe := filepath.Join(updaterDir, executableName)

	logSink.Debugf("[Updater] Application executable: %s", applicationExecutable)
	logSink.Debugf("[Updater] Application executable name: %s", applicationExeName)
	logSink.Debugf("[Updater] Updater directory: %s", updaterDir)
	logSink.Debugf("[Updater] Updater executable: %s", updaterExe)

	if info, statErr := os.Stat(updaterDir); statErr != nil {
		logSink.Errorf("[Updater] Updater directory is unavailable: dir=%s error=%v", updaterDir, statErr)
		return Paths{}, fmt.Errorf("check updater directory %s: %w", updaterDir, statErr)
	} else if !info.IsDir() {
		logSink.Errorf("[Updater] Updater path is not a directory: dir=%s", updaterDir)
		return Paths{}, fmt.Errorf("updater path is not a directory: %s", updaterDir)
	}

	if info, statErr := os.Stat(updaterExe); statErr != nil {
		logSink.Errorf("[Updater] Updater executable is unavailable: exe=%s error=%v", updaterExe, statErr)
		return Paths{}, fmt.Errorf("check updater executable %s: %w", updaterExe, statErr)
	} else if info.IsDir() {
		logSink.Errorf("[Updater] Updater executable path is a directory: exe=%s", updaterExe)
		return Paths{}, fmt.Errorf("updater executable path is a directory: %s", updaterExe)
	}

	return Paths{
		AppDir:             appDir,
		ApplicationExe:     applicationExecutable,
		ApplicationExeName: applicationExeName,
		UpdaterDir:         updaterDir,
		UpdaterExe:         updaterExe,
	}, nil
}

func (s *Service) Check(ctx context.Context) CheckResult {
	if ctx == nil {
		ctx = context.Background()
	}
	if !s.beginCheck() {
		logSink.Warnf("[Updater] Update check skipped because another check is already running")
		return CheckResult{Message: "update check is already running"}
	}
	defer s.endCheck()

	paths, err := s.resolvePaths()
	if err != nil {
		return CheckResult{Message: err.Error()}
	}

	args := buildUpdaterArgs(checkArgs)
	logSink.Infof("[Updater] Starting update check")
	logSink.Debugf("[Updater] Updater executable: %s", paths.UpdaterExe)
	logSink.Debugf("[Updater] Updater working directory: %s", paths.UpdaterDir)
	logSink.Debugf("[Updater] Check arguments: %v", args)

	checkCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	output, err := s.runCheck(checkCtx, paths, args)
	logProcessOutput(output)

	if errors.Is(checkCtx.Err(), context.DeadlineExceeded) {
		logSink.Errorf("[Updater] Update check timed out after %s", s.timeout)
		return CheckResult{Message: "update check timed out"}
	}
	if err != nil {
		logSink.Errorf("[Updater] Update check failed: %v", err)
		return CheckResult{Message: err.Error()}
	}

	available, ok := ParseCheckOutput(output.Stdout)
	if !ok {
		logSink.Warnf("[Updater] Unknown update check response: stdout=%q", output.Stdout)
		return CheckResult{Message: "unknown updater response"}
	}
	logSink.Infof("[Updater] Update check completed: update_available=%t", available)
	return CheckResult{OK: true, UpdateAvailable: available}
}

func (s *Service) Install() InstallResult {
	paths, err := s.resolvePaths()
	if err != nil {
		return InstallResult{Message: err.Error()}
	}

	restartCommand := paths.ApplicationExeName + " start"
	args := buildUpdaterArgs(buildUpgradeArgs(paths.ApplicationExeName))
	logSink.Infof("[Updater] Starting update installation")
	logSink.Debugf("[Updater] Updater executable: %s", paths.UpdaterExe)
	logSink.Debugf("[Updater] Updater working directory: %s", paths.UpdaterDir)
	logSink.Debugf("[Updater] Restart command: %s", restartCommand)
	logSink.Debugf("[Updater] Upgrade arguments: %v", args)

	process, err := s.startUpgrade(paths, args)
	if err != nil {
		logSink.Errorf("[Updater] Failed to start update installation: %v", err)
		return InstallResult{Message: err.Error()}
	}
	pid := process.PID()
	logSink.Debugf("[Updater] Update installation process started: pid=%d", pid)
	if err := process.Release(); err != nil {
		logSink.Errorf("[Updater] Failed to release update installation process handle: pid=%d error=%v", pid, err)
		return InstallResult{Message: err.Error()}
	}

	logSink.Infof("[Updater] Update installation started successfully: pid=%d", pid)
	logSink.Infof("[Updater] Application exit scheduled after %s", exitDelay)
	s.scheduleExit(exitDelay)
	return InstallResult{OK: true}
}

func ParseCheckOutput(stdout string) (available bool, ok bool) {
	switch strings.ToLower(strings.TrimSpace(stdout)) {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}

func buildUpgradeArgs(applicationExecutable string) []string {
	normalizedExecutable := strings.ReplaceAll(applicationExecutable, `\`, string(os.PathSeparator))
	return []string{
		"--upgrade",
		"--gui",
		"--cmd",
		filepath.Base(normalizedExecutable) + " start",
	}
}

func updaterLoggingArgs() []string {
	return []string{
		"--logs-dir",
		filepath.Join(config.WorkDir(), "logs"),
		"--logs-level",
		config.Cfg.Logs.LogLevel.Active,
		"--logs-clear",
		strconv.Itoa(config.Cfg.Logs.StoreDays),
	}
}

func buildUpdaterArgs(base []string) []string {
	args := make([]string, 0, len(base)+6)
	args = append(args, base...)
	return append(args, updaterLoggingArgs()...)
}

func (s *Service) beginCheck() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.checking {
		return false
	}
	s.checking = true
	return true
}

func (s *Service) endCheck() {
	s.mu.Lock()
	s.checking = false
	s.mu.Unlock()
}

func runCheckProcess(ctx context.Context, paths Paths, args []string) (processOutput, error) {
	cmd := exec.CommandContext(ctx, paths.UpdaterExe, args...)
	cmd.Dir = paths.UpdaterDir
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		logSink.Errorf("[Updater] Failed to start check process: exe=%s dir=%s args=%v error=%v", paths.UpdaterExe, paths.UpdaterDir, args, err)
		return processOutput{}, err
	}
	pid := cmd.Process.Pid
	logSink.Debugf("[Updater] Check process started: pid=%d", pid)
	err := cmd.Wait()
	output := processOutput{Stdout: stdout.String(), Stderr: stderr.String()}
	if err != nil {
		logSink.Errorf("[Updater] Check process completed with error: pid=%d error=%v", pid, err)
		return output, err
	}
	logSink.Debugf("[Updater] Check process completed successfully: pid=%d", pid)
	return output, nil
}

func startUpgradeProcess(paths Paths, args []string) (processHandle, error) {
	cmd := exec.Command(paths.UpdaterExe, args...)
	cmd.Dir = paths.UpdaterDir
	setDetachedProcessAttributes(cmd)
	if err := cmd.Start(); err != nil {
		logSink.Errorf("[Updater] Failed to start upgrade process: exe=%s dir=%s args=%v error=%v", paths.UpdaterExe, paths.UpdaterDir, args, err)
		return nil, err
	}
	return commandProcess{process: cmd.Process}, nil
}

func logProcessOutput(output processOutput) {
	logSink.Debugf("[Updater] Check stdout: %q", output.Stdout)
	if strings.TrimSpace(output.Stderr) != "" {
		logSink.Warnf("[Updater] Check stderr: %q", output.Stderr)
	}
}

func scheduleApplicationExit(after time.Duration) {
	go func() {
		time.Sleep(after)
		logSink.Infof("[Updater] Exiting application after update installation start")
		os.Exit(0)
	}()
}

type commandProcess struct {
	process *os.Process
}

func (p commandProcess) PID() int {
	if p.process == nil {
		return 0
	}
	return p.process.Pid
}

func (p commandProcess) Release() error {
	if p.process == nil {
		return nil
	}
	return p.process.Release()
}
