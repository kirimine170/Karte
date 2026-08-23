//go:build (darwin && universal) || linux

package asr

func validatePlatformConfig(*Config) error {
	return nil
}
