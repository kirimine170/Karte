//go:build windows

package ephyoutbox

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
)

// Derive volume/path identity from the already-open directory. A GUID volume
// names the same local filesystem even through drive-letter or junction aliases.
// Remote shares may lack GUID names; use their normalized DOS/UNC handle path.
func producerRootPath(root *os.Root) (string, error) {
	file, err := root.Open(".")
	if err != nil {
		return "", err
	}
	defer file.Close()
	for _, flags := range []uint32{1, 0} { // VOLUME_NAME_GUID, then VOLUME_NAME_DOS
		buffer := make([]uint16, 32768)
		n, err := windows.GetFinalPathNameByHandle(windows.Handle(file.Fd()), &buffer[0], uint32(len(buffer)), flags)
		if err != nil {
			continue
		}
		if n == 0 || n >= uint32(len(buffer)) {
			return "", fmt.Errorf("invalid producer root handle path")
		}
		return filepath.Clean(windows.UTF16ToString(buffer[:n])), nil
	}
	return "", fmt.Errorf("cannot resolve producer root handle path")
}
