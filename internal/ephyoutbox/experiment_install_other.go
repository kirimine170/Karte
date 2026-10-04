//go:build !linux && !darwin && !windows

package ephyoutbox

import (
	"fmt"
	"os"
)

func installEvidenceDirectory(root *os.Root, stage, candidate string) error {
	return fmt.Errorf("atomic no-replace evidence installation is unsupported on this platform")
}

func syncEvidenceDirectory(root *os.Root) error { return nil }
