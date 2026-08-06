//go:build !windows

package ocr

import "os/exec"

func configureCommand(*exec.Cmd) {}
