//go:build !windows

package main

import "os"

func csvOpenedFileHasMultipleLinks(_ *os.File, info os.FileInfo) (bool, error) {
	return csvFileHasMultipleLinks(info), nil
}
