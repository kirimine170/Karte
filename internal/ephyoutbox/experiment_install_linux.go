//go:build linux

package ephyoutbox

import (
	"golang.org/x/sys/unix"
	"os"
)

func installEvidenceDirectory(root *os.Root, stage, candidate string) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return unix.Renameat2(int(dir.Fd()), stage, int(dir.Fd()), candidate, unix.RENAME_NOREPLACE)
}

func syncEvidenceDirectory(root *os.Root) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
