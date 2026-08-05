//go:build !windows

package updater

import "os/exec"

func setDetachedProcessAttributes(_ *exec.Cmd) {}
