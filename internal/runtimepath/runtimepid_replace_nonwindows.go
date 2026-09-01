//go:build !windows

package runtimepath

import "os"

func replaceRuntimePID(source, destination string) error {
	return os.Rename(source, destination)
}
