//go:build linux

package ephyoutbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// /proc reports the retained directory object's current namespace path. Root.Name
// is only a display name (and Go1.25 child roots can name just ".").
func producerRootPath(root *os.Root) (string, error) {
	file, err := root.Open(".")
	if err != nil {
		return "", err
	}
	defer file.Close()
	resolved, err := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", file.Fd()))
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(resolved) || strings.HasSuffix(resolved, " (deleted)") {
		return "", fmt.Errorf("producer root no longer has a linked absolute handle path")
	}
	return filepath.Clean(resolved), nil
}
