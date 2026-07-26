//go:build !unix

package main

import "os"

func requireSingleLink(_ os.FileInfo, _ string) error {
	return nil
}
