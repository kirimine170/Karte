//go:build !linux

package ephyoutbox

import "os"

func validateProducerMountAliases(roots ...*os.Root) error { return nil }
