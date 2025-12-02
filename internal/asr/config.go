//go:build darwin

package asr

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	sherpa "github.com/k2-fsa/sherpa-onnx-go-macos"
)

// Config models the JSON file stored under karte_data/data/asr/config.json.
type Config struct {
	Enabled    bool        `json:"enabled"`
	SampleRate int         `json:"sampleRate"`
	Model      ModelSpec   `json:"model"`
	Decoding   DecodeSpec  `json:"decoding"`
	Runtime    RuntimeSpec `json:"runtime"`
}

// ModelSpec describes the ONNX model files.
type ModelSpec struct {
	Encoder      string `json:"encoder,omitempty"`
	Decoder      string `json:"decoder,omitempty"`
	Joiner       string `json:"joiner,omitempty"`
	ZipformerCTC string `json:"zipformerCtc,omitempty"`
	Tokens       string `json:"tokens"`
}

// DecodeSpec adjusts the recognizer behavior.
type DecodeSpec struct {
	Method string `json:"method"`
}

// RuntimeSpec captures runtime hints (threads/provider).
type RuntimeSpec struct {
	Threads  int    `json:"threads"`
	Provider string `json:"provider"`
}

// LoadConfigFromFile reads the JSON config if it exists.
func LoadConfigFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read ASR config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse ASR config: %w", err)
	}
	cfg.applyDefaults()
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.SampleRate == 0 {
		c.SampleRate = 16000
	}
	if c.Decoding.Method == "" {
		c.Decoding.Method = "greedy_search"
	}
	if c.Runtime.Threads == 0 {
		c.Runtime.Threads = 4
	}
	if c.Runtime.Provider == "" {
		c.Runtime.Provider = "cpu"
	}
}

// Validate ensures the config is usable when enabled.
func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	if !c.Enabled {
		return nil
	}
	if c.Model.Tokens == "" {
		return fmt.Errorf("model.tokens must be set")
	}
	if c.Model.ZipformerCTC == "" {
		if c.Model.Encoder == "" || c.Model.Decoder == "" || c.Model.Joiner == "" {
			return fmt.Errorf("encoder/decoder/joiner paths are required for transducer models")
		}
	}
	return nil
}

func (c *Config) offlineRecognizerConfig() *sherpa.OfflineRecognizerConfig {
	modelCfg := sherpa.OfflineModelConfig{
		Tokens:     c.Model.Tokens,
		NumThreads: c.Runtime.Threads,
		Provider:   c.Runtime.Provider,
	}

	if c.Model.ZipformerCTC != "" {
		modelCfg.ZipformerCtc = sherpa.OfflineZipformerCtcModelConfig{
			Model: c.Model.ZipformerCTC,
		}
	} else {
		modelCfg.Transducer = sherpa.OfflineTransducerModelConfig{
			Encoder: c.Model.Encoder,
			Decoder: c.Model.Decoder,
			Joiner:  c.Model.Joiner,
		}
	}

	return &sherpa.OfflineRecognizerConfig{
		FeatConfig: sherpa.FeatureConfig{
			SampleRate: c.SampleRate,
			FeatureDim: 80,
		},
		ModelConfig:    modelCfg,
		DecodingMethod: c.Decoding.Method,
		MaxActivePaths: 4,
		BlankPenalty:   0.0,
	}
}

// EnsureModelPathsAbsolute rewrites model paths to be absolute relative to baseDir.
func (c *Config) EnsureModelPathsAbsolute(baseDir string) {
	resolve := func(p string) string {
		if p == "" {
			return ""
		}
		if filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(baseDir, p)
	}
	c.Model.Tokens = resolve(c.Model.Tokens)
	c.Model.Encoder = resolve(c.Model.Encoder)
	c.Model.Decoder = resolve(c.Model.Decoder)
	c.Model.Joiner = resolve(c.Model.Joiner)
	c.Model.ZipformerCTC = resolve(c.Model.ZipformerCTC)
}
