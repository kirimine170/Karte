//go:build !windows

package canonical

import (
	"fmt"
	"os"
)

func SecureDirectory(path string) error       { return os.Chmod(path, 0700) }
func CheckPrivateFile(path string) error      { return checkPrivateMode(path, false) }
func CheckPrivateDirectory(path string) error { return checkPrivateMode(path, true) }
func checkPrivateMode(path string, directory bool) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if st.IsDir() != directory || (!directory && !st.Mode().IsRegular()) || st.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("unsafe_credentials")
	}
	return nil
}
func preparePermissions(root *os.Root, temp, target string, mode os.FileMode) error { return nil }
