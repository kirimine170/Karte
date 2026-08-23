//go:build windows

package audio

import (
	"fmt"

	"golang.org/x/sys/windows"
)

func atomicPublishRecordingFile(sourcePath, destinationPath string) error {
	source, err := windows.UTF16PtrFromString(sourcePath)
	if err != nil {
		return fmt.Errorf("invalid recording publish source path: %w", err)
	}
	destination, err := windows.UTF16PtrFromString(destinationPath)
	if err != nil {
		return fmt.Errorf("invalid recording publish destination path: %w", err)
	}
	if err := windows.MoveFileEx(
		source,
		destination,
		windows.MOVEFILE_WRITE_THROUGH,
	); err != nil {
		return fmt.Errorf("MoveFileEx recording publish failed: %w", err)
	}
	return nil
}
