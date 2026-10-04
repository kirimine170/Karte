//go:build darwin

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
	return unix.RenameatxNp(int(dir.Fd()), stage, int(dir.Fd()), candidate, unix.RENAME_EXCL)
}

func syncEvidenceDirectory(root *os.Root) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
