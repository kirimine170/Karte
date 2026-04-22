//go:build not_implemented

package asr

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	sherpa "github.com/k2-fsa/sherpa-onnx-go-windows"
)

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

// Windows 向けの簡易 Config 定義（sherpa-onnx には依存しない）

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

// LoadConfigFromFile は JSON を読み込むが、Windows では ASR を実装しない
func LoadConfigFromFile(path string) (*Config, error) { // config.goの機能の一部、未実装OS向けのスタブのため配置されている
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

// Windows では ASR をサポートしないため、Validate は最低限のチェックのみ行う
func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	return nil
}

// EnsureModelPathsAbsolute はパスを絶対パスに解決する（エディタ機能などで利用される可能性があるため）
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

// Windows 用の簡易 Service / RealtimeService スタブ

type Service struct{}

func NewService(cfg *Config) (*Service, error) {
	return nil, fmt.Errorf("ASR is not supported on Windows build")
}

func (s *Service) Close() {}

// TranscribeFile は Windows では未サポート
func (s *Service) TranscribeFile(_ interface{}, _ string, _ func(string, int, int, float64)) (string, error) {
	return "", fmt.Errorf("ASR transcription is not supported on Windows build")
}

// ProcessSamples も Windows では未サポート
func (s *Service) ProcessSamples(_ []float32) (string, error) {
	return "", fmt.Errorf("ASR process samples is not supported on Windows build")
}

// RealtimeService 向けのスタブ

type RealtimeService struct{}

type LogFunc func(format string, args ...interface{})

func NewRealtimeService(cfg *Config) (*RealtimeService, error) {
	return nil, fmt.Errorf("Realtime ASR is not supported on Windows build")
}

func NewRealtimeServiceWithLogger(cfg *Config, logFunc LogFunc) (*RealtimeService, error) {
	return nil, fmt.Errorf("Realtime ASR is not supported on Windows build")
}

func (s *RealtimeService) Close() {}

func (s *RealtimeService) ProcessAudio(_ []float32) (string, string, bool) {
	return "", "", false
}

func (s *RealtimeService) Flush() string {
	return ""
}

func (s *RealtimeService) Reset() {}
