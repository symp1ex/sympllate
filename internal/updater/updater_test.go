package updater

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseCheckOutput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		stdout    string
		available bool
		ok        bool
	}{
		{name: "true", stdout: "true", available: true, ok: true},
		{name: "uppercase true", stdout: "TRUE", available: true, ok: true},
		{name: "spaced true", stdout: " true ", available: true, ok: true},
		{name: "false", stdout: "false", ok: true},
		{name: "uppercase false", stdout: "FALSE", ok: true},
		{name: "spaced false", stdout: " false ", ok: true},
		{name: "unknown", stdout: "unknown"},
		{name: "empty", stdout: ""},
		{name: "multiline", stdout: "true\nlog"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			available, ok := ParseCheckOutput(test.stdout)
			if available != test.available || ok != test.ok {
				t.Fatalf("ParseCheckOutput(%q) = (%v, %v), want (%v, %v)", test.stdout, available, ok, test.available, test.ok)
			}
		})
	}
}

func TestBuildUpgradeArgsUsesActualExecutableName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		executable string
		command    string
	}{
		{executable: `C:\Program Files\App\support-launcher.exe`, command: "support-launcher.exe start"},
		{executable: `C:\Program Files\App\renamed-app.exe`, command: "renamed-app.exe start"},
	}
	for _, test := range tests {
		args := buildUpgradeArgs(test.executable)
		want := []string{"--upgrade", "--gui", "--cmd", test.command}
		if !reflect.DeepEqual(args, want) {
			t.Fatalf("buildUpgradeArgs(%q) = %v, want %v", test.executable, args, want)
		}
	}
}

func TestServiceCheckParsesStrictResultsAndErrors(t *testing.T) {
	t.Parallel()
	startErr := errors.New("start failed")
	resolveErr := errors.New("resolve failed")
	tests := []struct {
		name       string
		stdout     string
		runErr     error
		resolveErr error
		want       CheckResult
	}{
		{name: "true", stdout: "true", want: CheckResult{OK: true, UpdateAvailable: true}},
		{name: "false", stdout: "false", want: CheckResult{OK: true}},
		{name: "unknown", stdout: "true\nlog", want: CheckResult{Message: "unknown updater response"}},
		{name: "process error", runErr: startErr, want: CheckResult{Message: startErr.Error()}},
		{name: "resolve error", resolveErr: resolveErr, want: CheckResult{Message: resolveErr.Error()}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := newTestService()
			service.resolvePaths = func() (Paths, error) {
				if test.resolveErr != nil {
					return Paths{}, test.resolveErr
				}
				return testPaths(), nil
			}
			service.runCheck = func(context.Context, Paths, []string) (processOutput, error) {
				return processOutput{Stdout: test.stdout}, test.runErr
			}
			if result := service.Check(nil); result != test.want {
				t.Fatalf("Check() = %+v, want %+v", result, test.want)
			}
		})
	}
}

func TestServiceCheckRejectsParallelCheck(t *testing.T) {
	service := newTestService()
	started := make(chan struct{})
	release := make(chan struct{})
	service.runCheck = func(context.Context, Paths, []string) (processOutput, error) {
		close(started)
		<-release
		return processOutput{Stdout: "false"}, nil
	}

	var first CheckResult
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		first = service.Check(context.Background())
	}()
	<-started
	second := service.Check(context.Background())
	if second.OK || second.Message != "update check is already running" {
		t.Fatalf("parallel Check() = %+v", second)
	}
	close(release)
	wait.Wait()
	if !first.OK || first.UpdateAvailable {
		t.Fatalf("first Check() = %+v", first)
	}
}

func TestServiceCheckTimeout(t *testing.T) {
	t.Parallel()
	service := newTestService()
	service.timeout = 10 * time.Millisecond
	service.runCheck = func(ctx context.Context, _ Paths, _ []string) (processOutput, error) {
		<-ctx.Done()
		return processOutput{}, ctx.Err()
	}
	result := service.Check(context.Background())
	if result.OK || result.Message != "update check timed out" {
		t.Fatalf("Check() = %+v", result)
	}
}

func TestServiceInstallUsesUpdaterPathAndDynamicCommand(t *testing.T) {
	t.Parallel()
	service := newTestService()
	process := &fakeProcess{pid: 42}
	var gotPaths Paths
	var gotArgs []string
	var scheduled []time.Duration
	service.startUpgrade = func(paths Paths, args []string) (processHandle, error) {
		gotPaths = paths
		gotArgs = append([]string(nil), args...)
		return process, nil
	}
	service.scheduleExit = func(after time.Duration) { scheduled = append(scheduled, after) }

	result := service.Install()
	if !result.OK || result.Message != "" {
		t.Fatalf("Install() = %+v", result)
	}
	if filepath.Base(gotPaths.UpdaterExe) != executableName || gotPaths.UpdaterDir != filepath.Dir(gotPaths.UpdaterExe) {
		t.Fatalf("start paths = %+v", gotPaths)
	}
	if !containsSequence(gotArgs, []string{"--cmd", "renamed-app.exe start"}) {
		t.Fatalf("upgrade args do not contain dynamic restart command: %v", gotArgs)
	}
	if !process.released {
		t.Fatal("process handle was not released")
	}
	if !reflect.DeepEqual(scheduled, []time.Duration{exitDelay}) {
		t.Fatalf("scheduled exits = %v, want %v", scheduled, []time.Duration{exitDelay})
	}
}

func TestServiceInstallDoesNotExitAfterStartOrReleaseFailure(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		startErr   error
		releaseErr error
	}{
		{name: "start", startErr: errors.New("start failed")},
		{name: "release", releaseErr: errors.New("release failed")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := newTestService()
			process := &fakeProcess{pid: 42, releaseErr: test.releaseErr}
			service.startUpgrade = func(Paths, []string) (processHandle, error) {
				if test.startErr != nil {
					return nil, test.startErr
				}
				return process, nil
			}
			scheduled := false
			service.scheduleExit = func(time.Duration) { scheduled = true }
			result := service.Install()
			if result.OK || !strings.Contains(result.Message, "failed") {
				t.Fatalf("Install() = %+v", result)
			}
			if scheduled {
				t.Fatal("application exit was scheduled after a process failure")
			}
			if test.startErr != nil && process.released {
				t.Fatal("Release called when process start failed")
			}
		})
	}
}

func newTestService() *Service {
	service := NewService()
	service.resolvePaths = func() (Paths, error) { return testPaths(), nil }
	service.runCheck = func(context.Context, Paths, []string) (processOutput, error) {
		return processOutput{Stdout: "false"}, nil
	}
	service.scheduleExit = func(time.Duration) {}
	return service
}

func testPaths() Paths {
	appDir := filepath.Join(`C:\Program Files`, "App")
	updaterDir := filepath.Join(appDir, "updater")
	return Paths{
		AppDir:             appDir,
		ApplicationExe:     filepath.Join(appDir, "renamed-app.exe"),
		ApplicationExeName: "renamed-app.exe",
		UpdaterDir:         updaterDir,
		UpdaterExe:         filepath.Join(updaterDir, executableName),
	}
}

func containsSequence(values, sequence []string) bool {
	for index := 0; index+len(sequence) <= len(values); index++ {
		if reflect.DeepEqual(values[index:index+len(sequence)], sequence) {
			return true
		}
	}
	return false
}

type fakeProcess struct {
	pid        int
	released   bool
	releaseErr error
}

func (p *fakeProcess) PID() int { return p.pid }

func (p *fakeProcess) Release() error {
	p.released = true
	return p.releaseErr
}
