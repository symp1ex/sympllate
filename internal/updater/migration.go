package updater

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const migrationVersionFileName = "migration.version"

type migrationAction func(appDir string) error

type migration struct {
	version int
	action  migrationAction
}

type cleanupPlan struct {
	directories []string
	files       []string
}

var applicationMigrations = []migration{
	{
		version: 1,
		action: cleanupAction(cleanupPlan{
			directories: []string{
				filepath.Join("bin", "tesseract"),
			},
			files: []string{
				filepath.Join("bin", "inpaint", "onnxruntime.dll"),
				filepath.Join("bin", "inpaint", "legal", "ONNX-Runtime-LICENSE.md"),
				filepath.Join("bin", "inpaint", "legal", "ONNX-Runtime-ThirdPartyNotices.md"),
			},
		}),
	},
}

// RunMigrations applies all pending cleanup migrations for the installed application.
func RunMigrations() error {
	applicationExecutable, err := resolveApplicationExecutable()
	if err != nil {
		return err
	}
	stateFile, err := migrationStateFile()
	if err != nil {
		logSink.Errorf("[Updater] Migration state path is unavailable: %v", err)
		return err
	}

	return runMigrations(filepath.Dir(applicationExecutable), stateFile, applicationMigrations)
}

func migrationStateFile() (string, error) {
	programData, ok := os.LookupEnv("ProgramData")
	if !ok || strings.TrimSpace(programData) == "" {
		return "", errors.New("ProgramData environment variable is not set")
	}
	return filepath.Join(programData, "Sympllate", migrationVersionFileName), nil
}

func runMigrations(appDir, stateFile string, migrations []migration) error {
	if strings.TrimSpace(appDir) == "" {
		return errors.New("application directory is empty")
	}
	if strings.TrimSpace(stateFile) == "" {
		return errors.New("migration state file path is empty")
	}
	if err := validateMigrations(migrations); err != nil {
		return err
	}

	storedVersion := readMigrationVersion(stateFile)
	lastCompletedVersion := storedVersion
	logSink.Infof("[Updater] Migration stored version: %d", storedVersion)

	var migrationErr error
	for _, current := range migrations {
		if storedVersion >= current.version {
			continue
		}

		logSink.Infof("[Updater] Migration %d starting", current.version)
		if err := current.action(appDir); err != nil {
			migrationErr = fmt.Errorf("migration %d failed: %w", current.version, err)
			logSink.Errorf("[Updater] Migration %d failed: %v", current.version, err)
			break
		}
		lastCompletedVersion = current.version
		logSink.Infof("[Updater] Migration %d completed successfully", current.version)
	}

	if lastCompletedVersion > storedVersion {
		if err := writeMigrationVersion(stateFile, lastCompletedVersion); err != nil {
			writeErr := fmt.Errorf("write migration version %d: %w", lastCompletedVersion, err)
			logSink.Errorf("[Updater] Failed to write migration version: file=%s version=%d error=%v", stateFile, lastCompletedVersion, err)
			if migrationErr != nil {
				return errors.Join(migrationErr, writeErr)
			}
			return writeErr
		}
		logSink.Infof("[Updater] Migration version written successfully: file=%s version=%d", stateFile, lastCompletedVersion)
	}

	return migrationErr
}

func validateMigrations(migrations []migration) error {
	for index, current := range migrations {
		expectedVersion := index + 1
		if current.version != expectedVersion {
			return fmt.Errorf("migration at index %d has version %d, want %d", index, current.version, expectedVersion)
		}
		if current.action == nil {
			return fmt.Errorf("migration %d has no action", current.version)
		}
	}
	return nil
}

func readMigrationVersion(stateFile string) int {
	data, err := os.ReadFile(stateFile)
	if err != nil {
		if !os.IsNotExist(err) {
			logSink.Warnf("[Updater] Failed to read migration version; using version 0: file=%s error=%v", stateFile, err)
		}
		return 0
	}

	version, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || version < 0 {
		logSink.Warnf("[Updater] Invalid migration version; using version 0: file=%s value=%q", stateFile, strings.TrimSpace(string(data)))
		return 0
	}
	return version
}

func writeMigrationVersion(stateFile string, version int) error {
	if err := os.MkdirAll(filepath.Dir(stateFile), 0o755); err != nil {
		return fmt.Errorf("create migration state directory: %w", err)
	}
	if err := os.WriteFile(stateFile, []byte(strconv.Itoa(version)), 0o644); err != nil {
		return fmt.Errorf("write migration state file: %w", err)
	}
	return nil
}

func cleanupAction(plan cleanupPlan) migrationAction {
	return func(appDir string) error {
		for _, relativePath := range plan.directories {
			target, err := migrationTarget(appDir, relativePath)
			if err != nil {
				return err
			}
			logSink.Debugf("[Updater] Migration removing directory: %s", target)
			if err := os.RemoveAll(target); err != nil {
				logSink.Errorf("[Updater] Migration failed to remove directory: path=%s error=%v", target, err)
				return fmt.Errorf("remove directory %s: %w", target, err)
			}
		}

		for _, relativePath := range plan.files {
			target, err := migrationTarget(appDir, relativePath)
			if err != nil {
				return err
			}
			logSink.Debugf("[Updater] Migration removing file: %s", target)
			if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
				logSink.Errorf("[Updater] Migration failed to remove file: path=%s error=%v", target, err)
				return fmt.Errorf("remove file %s: %w", target, err)
			}
		}
		return nil
	}
}

func migrationTarget(appDir, relativePath string) (string, error) {
	if !filepath.IsLocal(relativePath) || relativePath == "." {
		return "", fmt.Errorf("migration path must be local to the application directory: %q", relativePath)
	}
	return filepath.Join(appDir, relativePath), nil
}
