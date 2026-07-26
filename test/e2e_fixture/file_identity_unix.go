//go:build unix

package main

import (
	"fmt"
	"os"
	"syscall"
)

func requireSingleLink(info os.FileInfo, path string) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("inspect legacy master.db hardlink count: unsupported file metadata for %q", path)
	}
	if stat.Nlink != 1 {
		return fmt.Errorf("legacy master.db must not be a hardlink: %q has %d links", path, stat.Nlink)
	}
	return nil
}
