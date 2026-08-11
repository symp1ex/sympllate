package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"image/png"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	DefaultTimeout = 60 * time.Second
	maximumStderr  = 64 << 10
	maxDiagnostic  = 512
)

// Runner is intentionally narrow so callers can test exact executable and
// argument lists without starting external processes.
type Runner interface {
	Run(ctx context.Context, executable string, args []string, stdout, stderr io.Writer) error
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, executable string, args []string, stdout, stderr io.Writer) error {
	command := exec.CommandContext(ctx, executable, args...)
	command.Stdout, command.Stderr = stdout, stderr
	configureCommand(command)
	return command.Run()
}

func Run(ctx context.Context, executable string, args []string, timeout time.Duration, runner Runner) error {
	if err := RequireExecutable(executable, "FFmpeg"); err != nil {
		return err
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	processContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stderr := &limitedBuffer{limit: maximumStderr}
	err := runner.Run(processContext, executable, args, io.Discard, stderr)
	if errors.Is(ctx.Err(), context.Canceled) {
		return context.Canceled
	}
	if errors.Is(processContext.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("FFmpeg timed out after %s", timeout)
	}
	if err == nil {
		return nil
	}
	message := strings.TrimSpace(stderr.String())
	message = redactFileArguments(message, args)
	if len(message) > maxDiagnostic {
		message = message[:maxDiagnostic]
	}
	if message != "" {
		return fmt.Errorf("FFmpeg failed: %w: %s", err, message)
	}
	return fmt.Errorf("FFmpeg failed: %w", err)
}

func redactFileArguments(message string, args []string) string {
	for index, argument := range args {
		isInput := index > 0 && args[index-1] == "-i"
		isOutput := index == len(args)-1
		if (isInput || isOutput) && argument != "" {
			message = strings.ReplaceAll(message, argument, "<path>")
			message = strings.ReplaceAll(message, strings.ReplaceAll(argument, `\`, `/`), "<path>")
		}
	}
	return message
}

func RequireExecutable(path, name string) error {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("%s executable was not found at %q", name, path)
	}
	return nil
}

// VerifyPNG requires a regular, non-empty PNG with positive dimensions.
func VerifyPNG(path string) error {
	_, _, err := PNGDimensions(path)
	return err
}

// PNGDimensions verifies the output and returns its decoded header dimensions.
func PNGDimensions(path string) (int, int, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return 0, 0, errors.New("FFmpeg did not create a non-empty PNG")
	}
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, errors.New("FFmpeg did not create a readable PNG")
	}
	defer file.Close()
	configuration, err := png.DecodeConfig(file)
	if err != nil || configuration.Width <= 0 || configuration.Height <= 0 {
		return 0, 0, errors.New("FFmpeg created an invalid PNG")
	}
	return configuration.Width, configuration.Height, nil
}

type limitedBuffer struct {
	data  []byte
	limit int
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	remaining := b.limit - len(b.data)
	if remaining > 0 {
		count := len(value)
		if count > remaining {
			count = remaining
		}
		b.data = append(b.data, value[:count]...)
	}
	return len(value), nil
}

func (b *limitedBuffer) String() string { return string(b.data) }
