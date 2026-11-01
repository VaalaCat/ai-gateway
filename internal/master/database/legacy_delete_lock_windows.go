package database

import (
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"os"
)

func openLegacyDeleteCoordinator(path string) (*os.File, bool, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, false, err
	}
	// Sharing read access lets diagnostics inspect the marker. Denying write and
	// delete sharing makes this handle a cross-process, nonblocking writer lock,
	// released by CloseHandle or process exit. Never follow a marker reparse point.
	open := func(disposition uint32) (windows.Handle, error) {
		return windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE,
			windows.FILE_SHARE_READ, nil, disposition,
			windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	}
	handle, err := open(windows.CREATE_NEW)
	created := err == nil
	if errors.Is(err, windows.ERROR_FILE_EXISTS) || errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		handle, err = open(windows.OPEN_EXISTING)
	}
	if errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		return nil, false, ErrLegacyDeleteInProgress
	}
	if err != nil {
		return nil, false, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, false, err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		_ = windows.CloseHandle(handle)
		return nil, false, fmt.Errorf("coordinator must be a regular file without a reparse point")
	}
	return os.NewFile(uintptr(handle), path), created, nil
}

// Closing the handle releases the Windows sharing lock.
func unlockLegacyDeleteCoordinator(*os.File) error { return nil }
