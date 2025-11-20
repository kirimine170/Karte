package asr

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	sherpa "github.com/k2-fsa/sherpa-onnx-go-macos"

	"karte/internal/audio"
)

// Service wraps a sherpa offline recognizer for one-shot transcriptions.
type Service struct {
	cfg        *Config
	recognizer *sherpa.OfflineRecognizer
	mu         sync.Mutex
}

// NewService constructs a Service from a validated config.
func NewService(cfg *Config) (*Service, error) {
	if cfg == nil {
		return nil, errors.New("nil config")
	}
	if !cfg.Enabled {
		return nil, errors.New("asr disabled in config")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	rec := sherpa.NewOfflineRecognizer(cfg.offlineRecognizerConfig())
	if rec == nil {
		return nil, fmt.Errorf("failed to initialize offline recognizer")
	}

	return &Service{
		cfg:        cfg,
		recognizer: rec,
	}, nil
}

// Close releases the underlying recognizer.
func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recognizer != nil {
		sherpa.DeleteOfflineRecognizer(s.recognizer)
		s.recognizer = nil
	}
}

// TranscribeFile decodes a single audio file into plain text.
func (s *Service) TranscribeFile(ctx context.Context, audioPath string) (string, error) {
	if s == nil {
		return "", errors.New("asr service is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	if s.recognizer == nil {
		return "", errors.New("asr recognizer is not initialized")
	}

	sampleRate, samples, err := audio.DecodeToPCM(ctx, audioPath, s.cfg.SampleRate)
	if err != nil {
		return "", fmt.Errorf("decode audio: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	stream := sherpa.NewOfflineStream(s.recognizer)
	if stream == nil {
		return "", fmt.Errorf("failed to allocate offline stream")
	}
	defer sherpa.DeleteOfflineStream(stream)

	stream.AcceptWaveform(sampleRate, samples)
	s.recognizer.Decode(stream)
	result := stream.GetResult()

	return strings.TrimSpace(result.Text), nil
}
