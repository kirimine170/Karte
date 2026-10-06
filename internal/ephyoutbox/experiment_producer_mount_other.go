//go:build !linux

package ephyoutbox

import "os"

func validateProducerMountAliases(roots ...*os.Root) error { return nil }

func validateProducerWriteTopology(root *os.Root) error { return nil }

func validateProducerWriteRoot(root, target *os.Root) error { return nil }

func validateProducerWriteFile(root *os.Root, file *os.File) error { return nil }
