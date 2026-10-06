//go:build !windows && !linux && !darwin

package ephyoutbox

import (
	"fmt"
	"os"
)

func producerRootPath(root *os.Root) (string, error) {
	return "", fmt.Errorf("producer root handle paths are unsupported on this platform")
}
