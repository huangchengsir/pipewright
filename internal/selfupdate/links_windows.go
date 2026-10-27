//go:build windows

package selfupdate

import (
	"golang.org/x/sys/windows"
	"os"
)

func singleLink(f *os.File, info os.FileInfo) bool {
	var v windows.ByHandleFileInformation
	if windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &v) != nil {
		return false
	}
	return v.NumberOfLinks == 1 && v.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0
}
