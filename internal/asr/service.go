package asr

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"karte/internal/audio"
)

type offlineStreamAdapter interface {
	AcceptWaveform(int, []float32) error
	Close()
}

type offlineRecognizerAdapter interface {
	NewStream() (offlineStreamAdapter, error)
	Decode(offlineStreamAdapter) (string, error)
	Close()
}

var errOfflineStreamUnavailable = errors.New("offline stream unavailable")

// Service wraps a sherpa offline recognizer for one-shot transcriptions.
type Service struct {
	cfg        *Config
	recognizer offlineRecognizerAdapter
	mu         sync.Mutex
}

// NewService constructs a Service from a validated config.
func NewService(cfg *Config) (*Service, error) {
	if err := offlinePlatformError("new"); err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, errors.New("nil config")
	}
	if !cfg.Enabled {
		return nil, errors.New("asr disabled in config")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	recognizer, err := newOfflineRecognizerAdapter(cfg)
	if err != nil {
		return nil, err
	}

	return &Service{
		cfg:        cfg,
		recognizer: recognizer,
	}, nil
}

// Close releases the underlying recognizer.
func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recognizer != nil {
		s.recognizer.Close()
		s.recognizer = nil
	}
}

// CountSegments counts speech segments without decoding them.
// TranscribeFile intentionally does not call this method so its converted WAV
// is traversed only once.
func (s *Service) CountSegments(ctx context.Context, audioPath string) (int, error) {
	if err := offlinePlatformError("count"); err != nil {
		return 0, err
	}
	if s == nil {
		return 0, errors.New("asr service is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	tempWav, cleanup, err := audio.ConvertToPCM16Wav(ctx, audioPath, s.cfg.SampleRate)
	if err != nil {
		return 0, fmt.Errorf("prepare pcm audio: %w", err)
	}
	defer cleanup()

	segmentCount, err := countWavSegments(ctx, tempWav, s.cfg.SampleRate, audio.StreamWavChunks)
	if err != nil {
		return 0, fmt.Errorf("stream audio: %w", err)
	}
	return segmentCount, nil
}

// TranscribeFile decodes a single audio file into plain text.
// Progress is emitted for each non-empty decoded line as soon as its segment is
// decoded. segmentIndex remains 1-based and timestamp is in seconds.
// totalSegments is zero because the final count is unknown until the single
// audio pass finishes.
func (s *Service) TranscribeFile(ctx context.Context, audioPath string, progress func(line string, segmentIndex, totalSegments int, timestamp float64)) (string, error) {
	if err := offlinePlatformError("transcribe"); err != nil {
		return "", err
	}
	if s == nil {
		return "", errors.New("asr service is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	s.mu.Lock()
	recognizerReady := s.recognizer != nil
	s.mu.Unlock()
	if !recognizerReady {
		return "", errors.New("asr recognizer is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	tempWav, cleanup, err := audio.ConvertToPCM16Wav(ctx, audioPath, s.cfg.SampleRate)
	if err != nil {
		return "", fmt.Errorf("prepare pcm audio: %w", err)
	}
	defer cleanup()

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if s.recognizer == nil {
		return "", errors.New("asr recognizer is not initialized")
	}

	result, err := transcribeWavSinglePass(
		ctx,
		tempWav,
		s.cfg.SampleRate,
		segmentDecoderOps[offlineStreamAdapter]{
			newDecoder: func() (offlineStreamAdapter, error) {
				stream, err := s.recognizer.NewStream()
				if errors.Is(err, errOfflineStreamUnavailable) {
					return nil, errors.New("failed to create offline stream")
				}
				return stream, err
			},
			acceptWaveform: func(stream offlineStreamAdapter, sampleRate int, chunk []float32) error {
				return stream.AcceptWaveform(sampleRate, chunk)
			},
			decode: func(stream offlineStreamAdapter) (string, error) {
				return s.recognizer.Decode(stream)
			},
			close: func(stream offlineStreamAdapter) { stream.Close() },
		},
		progress,
		audio.StreamWavChunks,
	)
	if err != nil {
		return "", fmt.Errorf("stream audio: %w", err)
	}
	return result.text, nil
}

// ProcessSamples processes audio samples in chunks and returns transcribed text.
// This is used for real-time transcription from live audio input.
// samples: audio samples to process (float32, mono, at cfg.SampleRate)
// Returns: transcribed text and any error
func (s *Service) ProcessSamples(samples []float32) (string, error) {
	if err := offlinePlatformError("samples"); err != nil {
		return "", err
	}
	if s == nil {
		return "", errors.New("asr service is nil")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recognizer == nil {
		return "", errors.New("asr recognizer is not initialized")
	}
	if len(samples) == 0 {
		return "", nil
	}
	stream, err := s.recognizer.NewStream()
	if err != nil {
		if errors.Is(err, errOfflineStreamUnavailable) {
			return "", fmt.Errorf("failed to allocate offline stream")
		}
		return "", err
	}
	defer stream.Close()

	// Feed samples to stream
	if err := stream.AcceptWaveform(s.cfg.SampleRate, samples); err != nil {
		return "", err
	}

	// Decode
	result, err := s.recognizer.Decode(stream)
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(result)

	return text, nil
}
