//go:build windows

package canonical

import (
	"golang.org/x/sys/windows"
	"os"
)

func lock(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &windows.Overlapped{})
}
func unlock(f *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &windows.Overlapped{})
}

// Windows cannot flush directory handles through os.File.Sync．The file is
// flushed before os.Root.Rename；restart recovery verifies the resulting bytes．
func syncDirectory(root *os.Root, name string) error { return nil }
