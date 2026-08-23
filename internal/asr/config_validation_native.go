//go:build (darwin && !universal) || windows

package asr

import "fmt"

func validatePlatformConfig(config *Config) error {
	if !config.Enabled {
		return nil
	}
	if config.Model.Tokens == "" {
		return fmt.Errorf("model.tokens must be set")
	}
	if config.Model.ZipformerCTC == "" {
		if config.Model.Encoder == "" || config.Model.Decoder == "" || config.Model.Joiner == "" {
			return fmt.Errorf("encoder/decoder/joiner paths are required for transducer models")
		}
	}
	return nil
}
