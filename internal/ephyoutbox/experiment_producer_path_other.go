//go:build !windows

package ephyoutbox

import (
	"os"
	"path/filepath"
)

func producerRootPath(root *os.Root) (string, error) {
	resolved, err := filepath.EvalSymlinks(root.Name())
	if err != nil {
		return "", err
	}
	return filepath.Abs(resolved)
}
