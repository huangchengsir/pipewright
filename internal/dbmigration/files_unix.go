//go:build linux || darwin

package dbmigration

import (
	"fmt"
	"os"
	"syscall"
)

func fileIdentity(f *os.File) (string, error) {
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", ErrIdentity
	}
	return fmt.Sprintf("%d:%d", st.Dev, st.Ino), nil
}
func lockFile(f *os.File) error         { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) }
func unlockFile(f *os.File)             { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
func replaceFile(src, dst string) error { return os.Rename(src, dst) }
func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func secureDirectory(path string, created bool) error { return checkPrivate(path) }
func checkPrivate(path string) error {
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		return ErrJournal
	}
	return nil
}
