//go:build !linux && !darwin && !windows

package selfupdate

import (
	"errors"
	"os"
)

func lockFile(f *os.File) error { return errors.New("unsupported platform") }
func unlockFile(f *os.File)     {}
