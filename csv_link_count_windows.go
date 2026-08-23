//go:build windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

func csvOpenedFileHasMultipleLinks(file *os.File, _ os.FileInfo) (bool, error) {
	if file == nil {
		return false, fmt.Errorf("inspect csv hard-link count: file is unavailable")
	}
	var information syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(file.Fd()), &information); err != nil {
		return false, fmt.Errorf("inspect csv hard-link count: %w", err)
	}
	return information.NumberOfLinks > 1, nil
}
