//go:build !windows

package main

import "os"

func atomicReplaceFile(sourcePath, destinationPath string) error {
	return os.Rename(sourcePath, destinationPath)
}
