//go:build windows

package main

import (
	"fmt"

	"golang.org/x/sys/windows"
)

func atomicReplaceFile(sourcePath, destinationPath string) error {
	source, err := windows.UTF16PtrFromString(sourcePath)
	if err != nil {
		return fmt.Errorf("invalid atomic replace source path: %w", err)
	}
	destination, err := windows.UTF16PtrFromString(destinationPath)
	if err != nil {
		return fmt.Errorf("invalid atomic replace destination path: %w", err)
	}
	if err := windows.MoveFileEx(
		source,
		destination,
		windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH,
	); err != nil {
		return fmt.Errorf("MoveFileEx failed: %w", err)
	}
	return nil
}
