//go:build !windows

package kubernetesowner

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
)

func lockFile(f *os.File) error {
	if unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) != nil {
		return ErrLocked
	}
	return nil
}
func fileIdentity(f *os.File) (string, error) {
	var st unix.Stat_t
	if e := unix.Fstat(int(f.Fd()), &st); e != nil {
		return "", e
	}
	return fmt.Sprintf("%d:%d", st.Dev, st.Ino), nil
}
func replaceJournal(a, b string) error { return os.Rename(a, b) }
func syncDirectory(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func checkPermissions(path string, f *os.File) error {
	d, e := os.Stat(path)
	if e != nil {
		return ErrJournal
	}
	s, e := f.Stat()
	if e != nil || d.Mode().Perm()&0077 != 0 || s.Mode().Perm()&0077 != 0 {
		return ErrJournal
	}
	return nil
}
