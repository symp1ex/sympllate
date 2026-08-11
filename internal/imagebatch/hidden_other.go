//go:build !windows

package imagebatch

import "os"

func fileIsHidden(os.FileInfo) bool { return false }
