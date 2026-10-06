//go:build linux || darwin

package ephyoutbox

import (
	"golang.org/x/sys/unix"
	"os"
)

func lockProducerPublication(file *os.File) error { return unix.Flock(int(file.Fd()), unix.LOCK_EX) }
func unlockProducerPublication(file *os.File)     { _ = unix.Flock(int(file.Fd()), unix.LOCK_UN) }
