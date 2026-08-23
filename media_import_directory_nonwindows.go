//go:build !windows

package main

import "os"

func syncMediaImportDirectory(directory *os.File) error {
	return directory.Sync()
}
