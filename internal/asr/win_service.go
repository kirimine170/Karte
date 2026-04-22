//go:build windows

package asr

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	sherpa "github.com/k2-fsa/sherpa-onnx-go-windows" // とりあえずwindows向けsherpaを配置

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

// CountSegments counts the number of speech segments in an audio file using VAD.
// This is used to estimate progress during transcription.
func (s *Service) CountSegments(ctx context.Context, audioPath string) (int, error) {
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

	chunkSamples := s.cfg.SampleRate / 100 // 10ms フレーム
	if chunkSamples < 160 {
		chunkSamples = 160
	}

	segmentCount := 0
	vad := audio.DefaultSimpleVAD()
	inSegment := false
	maxSegmentSamples := s.cfg.SampleRate * 15 // 15秒で強制フラッシュ
	currentSegmentSamples := 0

	if err := audio.StreamWavChunks(tempWav, chunkSamples, func(sampleRate int, chunk []float32) error {
		isSpeech, flush := vad.Process(chunk)
		if isSpeech {
			if !inSegment {
				inSegment = true
				segmentCount++
			}
			currentSegmentSamples += len(chunk)
			if currentSegmentSamples >= maxSegmentSamples {
				// Force segment end
				inSegment = false
				currentSegmentSamples = 0
				vad.Reset()
			}
		}
		if flush {
			inSegment = false
			currentSegmentSamples = 0
			vad.Reset()
		}
		return nil
	}); err != nil {
		return 0, fmt.Errorf("stream audio: %w", err)
	}

	// Count final segment if still in one
	if inSegment {
		segmentCount++
	}

	return segmentCount, nil
}

// TranscribeFile decodes a single audio file into plain text.
// progress callback receives (line, segmentIndex, totalSegments, timestamp) where segmentIndex is 1-based and timestamp is in seconds.
func (s *Service) TranscribeFile(ctx context.Context, audioPath string, progress func(line string, segmentIndex, totalSegments int, timestamp float64)) (string, error) {
	if s == nil {
		return "", errors.New("asr service is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	if s.recognizer == nil {
		return "", errors.New("asr recognizer is not initialized")
	}

	tempWav, cleanup, err := audio.ConvertToPCM16Wav(ctx, audioPath, s.cfg.SampleRate)
	if err != nil {
		return "", fmt.Errorf("prepare pcm audio: %w", err)
	}
	defer cleanup()

	s.mu.Lock()
	defer s.mu.Unlock()

	stream := sherpa.NewOfflineStream(s.recognizer)
	if stream == nil {
		return "", fmt.Errorf("failed to allocate offline stream")
	}
	defer sherpa.DeleteOfflineStream(stream)

	chunkSize := s.cfg.SampleRate / 2
	if chunkSize < 4000 {
		chunkSize = s.cfg.SampleRate
	}
	err = audio.StreamWavChunks(tempWav, chunkSize, func(sampleRate int, chunk []float32) error {
		stream.AcceptWaveform(sampleRate, chunk)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("stream audio: %w", err)
	}

	var transcript strings.Builder
	vad := audio.DefaultSimpleVAD()
	var segmentStream *sherpa.OfflineStream
	segmentSamples := 0
	maxSegmentSamples := s.cfg.SampleRate * 15 // 15秒で強制フラッシュ
	segmentIndex := 0
	totalSegments := 0
	processedSamples := 0    // Total processed samples for timestamp calculation
	segmentStartSamples := 0 // Samples at the start of current segment

	// Count segments first to get total count
	totalSegments, err = s.CountSegments(ctx, audioPath)
	if err != nil {
		// If counting fails, continue without progress info
		totalSegments = 0
	}

	finalizeSegment := func() {
		if segmentStream == nil {
			return
		}
		defer func() {
			sherpa.DeleteOfflineStream(segmentStream)
			segmentStream = nil
			segmentSamples = 0
		}()

		s.recognizer.Decode(segmentStream)
		text := strings.TrimSpace(segmentStream.GetResult().Text)
		if text != "" {
			segmentIndex++
			// Calculate timestamp from segment start samples
			timestamp := float64(segmentStartSamples) / float64(s.cfg.SampleRate)
			appendLines(&transcript, text, func(line string) {
				if progress != nil {
					progress(line, segmentIndex, totalSegments, timestamp)
				}
			})
		}
	}

	chunkSamples := s.cfg.SampleRate / 100 // 10ms フレーム
	if chunkSamples < 160 {
		chunkSamples = 160
	}

	if err := audio.StreamWavChunks(tempWav, chunkSamples, func(sampleRate int, chunk []float32) error {
		isSpeech, flush := vad.Process(chunk)
		chunkSize := len(chunk)

		if isSpeech {
			if segmentStream == nil {
				// New segment starts - record the timestamp
				segmentStartSamples = processedSamples
				segmentStream = sherpa.NewOfflineStream(s.recognizer)
				if segmentStream == nil {
					return fmt.Errorf("failed to create offline stream")
				}
			}
			segmentSamples += chunkSize
			segmentStream.AcceptWaveform(sampleRate, chunk)
			if segmentSamples >= maxSegmentSamples {
				finalizeSegment()
				vad.Reset()
			}
		}
		if flush {
			finalizeSegment()
			vad.Reset()
		}

		// Update total processed samples
		processedSamples += chunkSize
		return nil
	}); err != nil {
		return "", fmt.Errorf("stream audio: %w", err)
	}

	finalizeSegment()

	return strings.TrimSpace(transcript.String()), nil
}

// ProcessSamples processes audio samples in chunks and returns transcribed text.
// This is used for real-time transcription from live audio input.
// samples: audio samples to process (float32, mono, at cfg.SampleRate)
// Returns: transcribed text and any error
func (s *Service) ProcessSamples(samples []float32) (string, error) {
	if s == nil {
		return "", errors.New("asr service is nil")
	}
	if s.recognizer == nil {
		return "", errors.New("asr recognizer is not initialized")
	}
	if len(samples) == 0 {
		return "", nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	stream := sherpa.NewOfflineStream(s.recognizer)
	if stream == nil {
		return "", fmt.Errorf("failed to allocate offline stream")
	}
	defer sherpa.DeleteOfflineStream(stream)

	// Feed samples to stream
	stream.AcceptWaveform(s.cfg.SampleRate, samples)

	// Decode
	s.recognizer.Decode(stream)

	// Get result
	result := stream.GetResult()
	text := strings.TrimSpace(result.Text)

	return text, nil
}

func appendLines(buf *strings.Builder, portion string, progress func(line string)) {
	for _, line := range strings.Split(portion, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		buf.WriteString(line)
		buf.WriteRune('\n')
		if progress != nil {
			progress(line)
		}
	}
}
