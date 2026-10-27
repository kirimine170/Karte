//go:build !windows

package main

import (
	"os/exec"
	"runtime"
)

func openPDFWithDefaultApp(path string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	return exec.Command("open", path).Run()
}
