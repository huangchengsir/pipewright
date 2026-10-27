//go:build linux || darwin

package selfupdate

import (
	"os"
	"syscall"
)

func singleLink(f *os.File, info os.FileInfo) bool {
	v, ok := info.Sys().(*syscall.Stat_t)
	return ok && v.Nlink == 1
}
