//go:build !windows

package imagebatch

import (
	"os"
	"os/exec"
)

func fileIsHidden(os.FileInfo) bool { return false }
func configureProcess(*exec.Cmd)    {}
