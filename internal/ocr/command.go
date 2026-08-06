package ocr

import (
	"context"
	"io"
	"os/exec"
)

func runCommand(ctx context.Context, executable string, args []string, stdout, stderr io.Writer) error {
	command := exec.CommandContext(ctx, executable, args...)
	command.Stdout = stdout
	command.Stderr = stderr
	configureCommand(command)
	return command.Run()
}
