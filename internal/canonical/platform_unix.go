//go:build !windows

package canonical

import (
	"golang.org/x/sys/unix"
	"os"
)

func lock(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_EX) }
func unlock(f *os.File)     { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN) }
func syncDirectory(root *os.Root, name string) error {
	f, e := root.Open(name)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
