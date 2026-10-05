//go:build darwin

package ephyoutbox

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"unsafe"
)

// F_GETPATH derives the current path from the retained directory handle, even
// after a rename; it never reopens a potentially replaced display pathname.
func producerRootPath(root *os.Root) (string, error) {
	file, err := root.Open(".")
	if err != nil {
		return "", err
	}
	defer file.Close()
	var buffer [1024]byte // Darwin MAXPATHLEN, the F_GETPATH buffer contract.
	_, _, errno := unix.Syscall(unix.SYS_FCNTL, file.Fd(), uintptr(unix.F_GETPATH), uintptr(unsafe.Pointer(&buffer[0])))
	if errno != 0 {
		return "", errno
	}
	end := 0
	for end < len(buffer) && buffer[end] != 0 {
		end++
	}
	if end == 0 || end == len(buffer) || !filepath.IsAbs(string(buffer[:end])) {
		return "", fmt.Errorf("invalid producer root handle path")
	}
	return filepath.Clean(string(buffer[:end])), nil
}
