//go:build windows

package main

import "os"

func syncTranscriptDirectory(_ *os.File) error {
	// Windows does not expose a portable fsync operation for directory
	// handles．The temporary file itself is flushed before no-replace install．
	return nil
}
