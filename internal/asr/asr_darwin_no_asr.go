//go:build darwin && (universal || amd64)

package asr

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Universal Binary / Intel Mac 向けの簡易 Config 定義（sherpa-onnx には依存しない）

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

// LoadConfigFromFile は JSON を読み込むが、Universal Binary / Intel Mac では ASR を実装しない
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

func (c *Config) offlineRecognizerConfig() interface{} {
	return nil
}

func (c *Config) onlineRecognizerConfig() interface{} {
	return nil
}

// Universal Binary / Intel Mac 用の簡易 Service / RealtimeService スタブ

type Service struct{}

func NewService(cfg *Config) (*Service, error) {
	return nil, fmt.Errorf("ASR is not supported on Universal Binary or Intel Mac builds")
}

func (s *Service) Close() {}

func (s *Service) CountSegments(_ context.Context, _ string) (int, error) {
	return 0, fmt.Errorf("ASR is not supported on Universal Binary or Intel Mac builds")
}

func (s *Service) TranscribeFile(_ context.Context, _ string, _ func(string, int, int, float64)) (string, error) {
	return "", fmt.Errorf("ASR transcription is not supported on Universal Binary or Intel Mac builds")
}

func (s *Service) ProcessSamples(_ []float32) (string, error) {
	return "", fmt.Errorf("ASR process samples is not supported on Universal Binary or Intel Mac builds")
}

// RealtimeService 向けのスタブ

type RealtimeService struct{}

type LogFunc func(format string, args ...interface{})

func NewRealtimeService(cfg *Config) (*RealtimeService, error) {
	return nil, fmt.Errorf("Realtime ASR is not supported on Universal Binary or Intel Mac builds")
}

func NewRealtimeServiceWithLogger(cfg *Config, logFunc LogFunc) (*RealtimeService, error) {
	return nil, fmt.Errorf("Realtime ASR is not supported on Universal Binary or Intel Mac builds")
}

func (s *RealtimeService) Close() {}

func (s *RealtimeService) ProcessAudio(_ []float32) (string, string, bool) {
	return "", "", false
}

func (s *RealtimeService) Flush() string {
	return ""
}

func (s *RealtimeService) Reset() {}

