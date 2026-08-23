//go:build !windows

package main

import "os"

func syncTranscriptDirectory(directory *os.File) error {
	return directory.Sync()
}
