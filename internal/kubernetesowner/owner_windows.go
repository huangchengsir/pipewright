package kubernetesowner

import (
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"time"
)

func lockFile(f *os.File) error {
	var o windows.Overlapped
	if windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &o) != nil {
		return ErrLocked
	}
	return nil
}
func fileIdentity(f *os.File) (string, error) {
	var i windows.ByHandleFileInformation
	e := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &i)
	if e != nil {
		return "", e
	}
	return fmt.Sprintf("%d:%d:%d", i.VolumeSerialNumber, i.FileIndexHigh, i.FileIndexLow), nil
}
func replaceJournal(a, b string) error {
	src, e := windows.UTF16PtrFromString(a)
	if e != nil {
		return e
	}
	dst, e := windows.UTF16PtrFromString(b)
	if e != nil {
		return e
	}
	// Windows scanners can briefly open the destination without DELETE sharing.
	// Retry only a rejected replacement with the original source still present;
	// never create a new journal, weaken ACLs, or reinterpret a missing source.
	for attempt := 0; attempt < 6; attempt++ {
		e = windows.MoveFileEx(src, dst, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
		if e == nil {
			return nil
		}
		if !errors.Is(e, windows.ERROR_ACCESS_DENIED) && !errors.Is(e, windows.ERROR_SHARING_VIOLATION) {
			return e
		}
		if _, sourceErr := os.Stat(a); sourceErr != nil {
			return e
		}
		if attempt < 5 {
			time.Sleep(20 * time.Millisecond)
		}
	}
	return e
}
func syncDirectory(string) error              { return nil } // MoveFileEx WRITE_THROUGH + file FlushFileBuffers above.
func checkPermissions(string, *os.File) error { return nil } // Windows uses the host data-dir ACL; never alter policy here.
