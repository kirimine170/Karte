//go:build !linux && !darwin && !windows

package ephyoutbox

import (
	"fmt"
	"os"
)

func lockProducerPublication(file *os.File) error {
	return fmt.Errorf("producer publication locks are unsupported on this platform")
}
func unlockProducerPublication(file *os.File) {}
