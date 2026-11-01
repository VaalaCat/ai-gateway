//go:build unix

package database

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

func openLegacyDeleteCoordinator(path string) (*os.File, bool, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	created := err == nil
	if errors.Is(err, os.ErrExist) {
		fd, err = unix.Open(path, unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	}
	if err != nil {
		return nil, false, err
	}
	file := os.NewFile(uintptr(fd), path)
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, false, ErrLegacyDeleteInProgress
		}
		return nil, false, err
	}
	return file, created, nil
}

func unlockLegacyDeleteCoordinator(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
