//go:build windows

package main

import "os"

func syncMediaImportDirectory(_ *os.File) error {
	// Windows does not expose a portable fsync operation for directory
	// handles. The staged file itself is still flushed before publication.
	return nil
}
