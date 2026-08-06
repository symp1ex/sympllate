//go:build windows

package imagebatch

import (
	"fmt"
	"os/exec"
)

func openExplorer(directory string) error {
	command := exec.Command("explorer.exe", directory)
	if err := command.Start(); err != nil {
		return fmt.Errorf("open Explorer: %w", err)
	}
	if err := command.Process.Release(); err != nil {
		return fmt.Errorf("release Explorer process: %w", err)
	}
	return nil
}
