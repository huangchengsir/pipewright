//go:build !windows && !linux && !darwin

package selfupdate

import (
	"os"
)

func singleLink(f *os.File, info os.FileInfo) bool { return false }
