package asr

import (
	"path/filepath"
	"strings"
)

// IsStreamingModel reports whether the configured model files describe an
// online/streaming recognizer. Streaming transducer models cannot be passed to
// sherpa-onnx's offline recognizer: doing so terminates the whole process from
// native code instead of returning a Go error.
func (c *Config) IsStreamingModel() bool {
	if c == nil {
		return false
	}

	for _, modelPath := range []string{
		c.Model.Encoder,
		c.Model.Decoder,
		c.Model.Joiner,
		c.Model.ZipformerCTC,
	} {
		normalizedPath := strings.ToLower(filepath.ToSlash(modelPath))
		name := strings.ToLower(filepath.Base(modelPath))
		if strings.Contains(normalizedPath, "streaming") ||
			strings.Contains(name, "chunk") ||
			strings.Contains(name, "left") ||
			strings.Contains(name, "right") {
			return true
		}
	}

	return false
}
