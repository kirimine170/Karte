//go:build windows

package main

// Windows does not provide a portable directory fsync through os.File.  The
// marker contents are flushed before the atomic hard-link install.
func syncStartupSmokeDirectory(string) error {
	return nil
}
