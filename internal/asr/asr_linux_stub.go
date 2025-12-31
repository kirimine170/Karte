//go:build linux

package asr

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config mirrors the ASR configuration but is stubbed on Linux.
type Config struct {
	Enabled    bool        `json:"enabled"`
	SampleRate int         `json:"sampleRate"`
	Model      ModelSpec   `json:"model"`
	Decoding   DecodeSpec  `json:"decoding"`
	Runtime    RuntimeSpec `json:"runtime"`
}

type ModelSpec struct {
	Encoder      string `json:"encoder,omitempty"`
	Decoder      string `json:"decoder,omitempty"`
	Joiner       string `json:"joiner,omitempty"`
	ZipformerCTC string `json:"zipformerCtc,omitempty"`
	Tokens       string `json:"tokens"`
}

type DecodeSpec struct {
	Method string `json:"method"`
}

type RuntimeSpec struct {
	Threads  int    `json:"threads"`
	Provider string `json:"provider"`
}

// LogFunc represents a logging hook for realtime service creation.
type LogFunc func(format string, args ...interface{})

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

func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	return nil
}

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

// Service stubs ASR functionality on Linux builds used for tests.
type Service struct{}

func NewService(cfg *Config) (*Service, error) {
	return nil, fmt.Errorf("ASR is not supported on Linux test build")
}

func (s *Service) Close() {}

func (s *Service) TranscribeFile(_ interface{}, _ string, _ func(string, int, int, float64)) (string, error) {
	return "", fmt.Errorf("ASR transcription is not supported on Linux test build")
}

func (s *Service) ProcessSamples(_ []float32) (string, error) {
	return "", fmt.Errorf("ASR process samples is not supported on Linux test build")
}

type RealtimeService struct{}

func NewRealtimeService(_ *Config) (*RealtimeService, error) {
	return nil, fmt.Errorf("Realtime ASR is not supported on Linux test build")
}

func NewRealtimeServiceWithLogger(_ *Config, _ LogFunc) (*RealtimeService, error) {
	return nil, fmt.Errorf("Realtime ASR is not supported on Linux test build")
}

func (s *RealtimeService) Close() {}

func (s *RealtimeService) ProcessAudio(_ []float32) (string, string, bool) {
	return "", "", false
}

func (s *RealtimeService) Flush() string { return "" }

func (s *RealtimeService) Reset() {}
