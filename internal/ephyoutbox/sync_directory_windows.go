//go:build windows

package ephyoutbox

// Go's os.File.Sync cannot flush directory handles on Windows. The outbox
// still syncs the temporary file before the atomic rename; skip only the
// unsupported directory flush so reviewed proposals remain usable on Windows.
func syncDirectory(string) error {
	return nil
}
