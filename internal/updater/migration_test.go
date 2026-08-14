package updater

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRunMigrationsWithoutVersionFileRunsMigrationOne(t *testing.T) {
	appDir, stateFile := migrationTestPaths(t)
	targets := createMigrationOneTargets(t, appDir)

	if err := runMigrations(appDir, stateFile, applicationMigrations); err != nil {
		t.Fatalf("runMigrations() error = %v", err)
	}

	for _, target := range targets {
		assertPathDoesNotExist(t, target)
	}
	assertMigrationVersion(t, stateFile, "1")
}

func TestRunMigrationsFromVersionZero(t *testing.T) {
	appDir, stateFile := migrationTestPaths(t)
	writeTestFile(t, stateFile, " 0\n")
	target := filepath.Join(appDir, "bin", "inpaint", "onnxruntime.dll")
	writeTestFile(t, target, "obsolete")

	if err := runMigrations(appDir, stateFile, applicationMigrations); err != nil {
		t.Fatalf("runMigrations() error = %v", err)
	}

	assertPathDoesNotExist(t, target)
	assertMigrationVersion(t, stateFile, "1")
}

func TestRunMigrationsAtCurrentVersionSkipsMigrationOne(t *testing.T) {
	appDir, stateFile := migrationTestPaths(t)
	writeTestFile(t, stateFile, "1")
	targets := createMigrationOneTargets(t, appDir)

	if err := runMigrations(appDir, stateFile, applicationMigrations); err != nil {
		t.Fatalf("runMigrations() error = %v", err)
	}

	for _, target := range targets {
		if _, err := os.Stat(target); err != nil {
			t.Fatalf("already-migrated target %s was changed: %v", target, err)
		}
	}
	assertMigrationVersion(t, stateFile, "1")
}

func TestRunMigrationsTreatsInvalidVersionAsZero(t *testing.T) {
	appDir, stateFile := migrationTestPaths(t)
	writeTestFile(t, stateFile, "not-a-version\n")
	target := filepath.Join(appDir, "bin", "inpaint", "onnxruntime.dll")
	writeTestFile(t, target, "obsolete")

	if err := runMigrations(appDir, stateFile, applicationMigrations); err != nil {
		t.Fatalf("runMigrations() error = %v", err)
	}

	assertPathDoesNotExist(t, target)
	assertMigrationVersion(t, stateFile, "1")
}

func TestReadMigrationVersionFallsBackToZero(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "negative", content: "-1"},
		{name: "empty", content: " \r\n"},
		{name: "overflow", content: "999999999999999999999999999999999999"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stateFile := filepath.Join(t.TempDir(), migrationVersionFileName)
			writeTestFile(t, stateFile, test.content)
			if got := readMigrationVersion(stateFile); got != 0 {
				t.Fatalf("readMigrationVersion() = %d, want 0", got)
			}
		})
	}

	if got := readMigrationVersion(t.TempDir()); got != 0 {
		t.Fatalf("readMigrationVersion(directory) = %d, want 0", got)
	}
}

func TestRunMigrationsSucceedsWhenCleanupTargetsAreAbsent(t *testing.T) {
	appDir, stateFile := migrationTestPaths(t)

	if err := runMigrations(appDir, stateFile, applicationMigrations); err != nil {
		t.Fatalf("runMigrations() error = %v", err)
	}

	assertMigrationVersion(t, stateFile, "1")
}

func TestMigrationOneRemovesTesseractRecursively(t *testing.T) {
	appDir, stateFile := migrationTestPaths(t)
	tesseractDir := filepath.Join(appDir, "bin", "tesseract")
	writeTestFile(t, filepath.Join(tesseractDir, "nested", "deeper", "model.dat"), "obsolete")
	writeTestFile(t, filepath.Join(tesseractDir, "root.txt"), "obsolete")

	if err := runMigrations(appDir, stateFile, applicationMigrations); err != nil {
		t.Fatalf("runMigrations() error = %v", err)
	}

	assertPathDoesNotExist(t, tesseractDir)
}

func TestRunMigrationsUsesAppDirectoryInsteadOfWorkingDirectory(t *testing.T) {
	appDir, stateFile := migrationTestPaths(t)
	workingDir := t.TempDir()
	relativeTarget := filepath.Join("bin", "inpaint", "onnxruntime.dll")
	appTarget := filepath.Join(appDir, relativeTarget)
	workingTarget := filepath.Join(workingDir, relativeTarget)
	writeTestFile(t, appTarget, "remove me")
	writeTestFile(t, workingTarget, "keep me")

	originalWorkingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd() error = %v", err)
	}
	if err := os.Chdir(workingDir); err != nil {
		t.Fatalf("os.Chdir() error = %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalWorkingDir); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	if err := runMigrations(appDir, stateFile, applicationMigrations); err != nil {
		t.Fatalf("runMigrations() error = %v", err)
	}

	assertPathDoesNotExist(t, appTarget)
	if data, err := os.ReadFile(workingTarget); err != nil || string(data) != "keep me" {
		t.Fatalf("working-directory target changed: data=%q error=%v", data, err)
	}
}

func TestRunMigrationsExecutesPendingStepsInOrder(t *testing.T) {
	appDir, stateFile := migrationTestPaths(t)
	writeTestFile(t, stateFile, "1")
	var executed []int
	steps := []migration{
		{version: 1, action: recordingMigration(&executed, 1, nil)},
		{version: 2, action: recordingMigration(&executed, 2, nil)},
		{version: 3, action: recordingMigration(&executed, 3, nil)},
	}

	if err := runMigrations(appDir, stateFile, steps); err != nil {
		t.Fatalf("runMigrations() error = %v", err)
	}

	if want := []int{2, 3}; !reflect.DeepEqual(executed, want) {
		t.Fatalf("executed migrations = %v, want %v", executed, want)
	}
	assertMigrationVersion(t, stateFile, "3")
}

func TestRunMigrationsStopsAtFailedStepAndPersistsPreviousSuccess(t *testing.T) {
	appDir, stateFile := migrationTestPaths(t)
	failure := errors.New("cleanup failed")
	var executed []int
	steps := []migration{
		{version: 1, action: recordingMigration(&executed, 1, nil)},
		{version: 2, action: recordingMigration(&executed, 2, failure)},
		{version: 3, action: recordingMigration(&executed, 3, nil)},
	}

	err := runMigrations(appDir, stateFile, steps)
	if !errors.Is(err, failure) {
		t.Fatalf("runMigrations() error = %v, want wrapped %v", err, failure)
	}
	if want := []int{1, 2}; !reflect.DeepEqual(executed, want) {
		t.Fatalf("executed migrations = %v, want %v", executed, want)
	}
	assertMigrationVersion(t, stateFile, "1")
}

func TestRunMigrationsReturnsCleanupErrorWithoutAdvancingVersion(t *testing.T) {
	appDir, stateFile := migrationTestPaths(t)
	targetDirectory := filepath.Join(appDir, "cannot-remove-as-file")
	writeTestFile(t, filepath.Join(targetDirectory, "child.txt"), "content")
	steps := []migration{
		{
			version: 1,
			action: cleanupAction(cleanupPlan{
				files: []string{"cannot-remove-as-file"},
			}),
		},
	}

	if err := runMigrations(appDir, stateFile, steps); err == nil {
		t.Fatal("runMigrations() error = nil, want cleanup error")
	}
	if _, err := os.Stat(stateFile); !os.IsNotExist(err) {
		t.Fatalf("migration version was written after failed cleanup: %v", err)
	}
}

func TestRunMigrationsReturnsVersionWriteError(t *testing.T) {
	previousLogger := logSink
	logger := &recordingMigrationLogger{}
	SetLogger(logger)
	t.Cleanup(func() { SetLogger(previousLogger) })

	appDir := t.TempDir()
	stateParent := filepath.Join(t.TempDir(), "not-a-directory")
	writeTestFile(t, stateParent, "block directory creation")
	stateFile := filepath.Join(stateParent, migrationVersionFileName)
	steps := []migration{
		{version: 1, action: func(string) error { return nil }},
	}

	err := runMigrations(appDir, stateFile, steps)
	if err == nil || !strings.Contains(err.Error(), "write migration version 1") {
		t.Fatalf("runMigrations() error = %v, want version write error", err)
	}
	if !containsLogMessage(logger.errors, "Failed to write migration version") {
		t.Fatalf("version write error was not logged: %v", logger.errors)
	}
}

func TestMigrationStateFileUsesProgramData(t *testing.T) {
	programData := t.TempDir()
	t.Setenv("ProgramData", programData)

	got, err := migrationStateFile()
	if err != nil {
		t.Fatalf("migrationStateFile() error = %v", err)
	}
	want := filepath.Join(programData, "Sympllate", migrationVersionFileName)
	if got != want {
		t.Fatalf("migrationStateFile() = %q, want %q", got, want)
	}
}

func migrationTestPaths(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	appDir := filepath.Join(root, "application")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatalf("create app directory: %v", err)
	}
	return appDir, filepath.Join(root, "state", migrationVersionFileName)
}

func createMigrationOneTargets(t *testing.T, appDir string) []string {
	t.Helper()
	tesseractDir := filepath.Join(appDir, "bin", "tesseract")
	writeTestFile(t, filepath.Join(tesseractDir, "nested", "model.dat"), "obsolete")
	targets := []string{
		tesseractDir,
		filepath.Join(appDir, "bin", "inpaint", "onnxruntime.dll"),
		filepath.Join(appDir, "bin", "inpaint", "legal", "ONNX-Runtime-LICENSE.md"),
		filepath.Join(appDir, "bin", "inpaint", "legal", "ONNX-Runtime-ThirdPartyNotices.md"),
	}
	for _, target := range targets[1:] {
		writeTestFile(t, target, "obsolete")
	}
	return targets
}

func recordingMigration(executed *[]int, version int, result error) migrationAction {
	return func(string) error {
		*executed = append(*executed, version)
		return result
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create parent directory for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func assertPathDoesNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("path still exists or stat failed: path=%s error=%v", path, err)
	}
}

func assertMigrationVersion(t *testing.T, stateFile, want string) {
	t.Helper()
	data, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("read migration version: %v", err)
	}
	if got := string(data); got != want {
		t.Fatalf("migration version = %q, want %q", got, want)
	}
}

type recordingMigrationLogger struct {
	errors []string
}

func (*recordingMigrationLogger) Debugf(string, ...any) {}
func (*recordingMigrationLogger) Infof(string, ...any)  {}
func (*recordingMigrationLogger) Warnf(string, ...any)  {}

func (l *recordingMigrationLogger) Errorf(format string, args ...any) {
	l.errors = append(l.errors, fmt.Sprintf(format, args...))
}

func containsLogMessage(messages []string, substring string) bool {
	for _, message := range messages {
		if strings.Contains(message, substring) {
			return true
		}
	}
	return false
}
