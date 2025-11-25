package asr

import (
	"errors"
	"fmt"
	"os"
	"sync"

	sherpa "github.com/k2-fsa/sherpa-onnx-go-macos"
)

// RealtimeService provides real-time ASR using OnlineRecognizer
type RealtimeService struct {
	cfg        *Config
	recognizer *sherpa.OnlineRecognizer
	stream     *sherpa.OnlineStream
	mu         sync.Mutex
	sampleRate int
}

// NewRealtimeService creates a new real-time ASR service
func NewRealtimeService(cfg *Config) (*RealtimeService, error) {
	fmt.Printf("[RealtimeASR] Creating new real-time ASR service...\n")
	if cfg == nil {
		fmt.Printf("[RealtimeASR] ERROR: nil config\n")
		return nil, errors.New("nil config")
	}
	if !cfg.Enabled {
		fmt.Printf("[RealtimeASR] ERROR: ASR disabled in config\n")
		return nil, errors.New("asr disabled in config")
	}
	if err := cfg.Validate(); err != nil {
		fmt.Printf("[RealtimeASR] ERROR: Config validation failed: %v\n", err)
		return nil, err
	}

	// Verify model files exist
	fmt.Printf("[RealtimeASR] Verifying model files exist...\n")
	if err := verifyModelFiles(cfg); err != nil {
		fmt.Printf("[RealtimeASR] ERROR: Model file verification failed: %v\n", err)
		return nil, fmt.Errorf("model file verification failed: %w", err)
	}
	fmt.Printf("[RealtimeASR] Model files verified\n")

	fmt.Printf("[RealtimeASR] Building online recognizer config...\n")
	onlineCfg := cfg.onlineRecognizerConfig()
	if onlineCfg == nil {
		fmt.Printf("[RealtimeASR] ERROR: Failed to build online recognizer config\n")
		return nil, fmt.Errorf("failed to build online recognizer config")
	}
	fmt.Printf("[RealtimeASR] Online recognizer config built\n")

	fmt.Printf("[RealtimeASR] Initializing online recognizer (this may take a moment)...\n")
	rec := sherpa.NewOnlineRecognizer(onlineCfg)
	if rec == nil {
		fmt.Printf("[RealtimeASR] ERROR: Failed to initialize online recognizer\n")
		return nil, fmt.Errorf("failed to initialize online recognizer")
	}
	fmt.Printf("[RealtimeASR] Online recognizer initialized\n")

	fmt.Printf("[RealtimeASR] Allocating online stream...\n")
	stream := sherpa.NewOnlineStream(rec)
	if stream == nil {
		fmt.Printf("[RealtimeASR] ERROR: Failed to allocate online stream\n")
		sherpa.DeleteOnlineRecognizer(rec)
		return nil, fmt.Errorf("failed to allocate online stream")
	}
	fmt.Printf("[RealtimeASR] Online stream allocated (sampleRate=%d)\n", cfg.SampleRate)

	fmt.Printf("[RealtimeASR] Real-time ASR service created successfully\n")
	return &RealtimeService{
		cfg:        cfg,
		recognizer: rec,
		stream:     stream,
		sampleRate: cfg.SampleRate,
	}, nil
}

// verifyModelFiles checks that all required model files exist
func verifyModelFiles(cfg *Config) error {
	if cfg.Model.Tokens == "" {
		return fmt.Errorf("tokens file path is empty")
	}
	if _, err := os.Stat(cfg.Model.Tokens); err != nil {
		return fmt.Errorf("tokens file not found: %s (%w)", cfg.Model.Tokens, err)
	}
	
	if cfg.Model.ZipformerCTC != "" {
		if _, err := os.Stat(cfg.Model.ZipformerCTC); err != nil {
			return fmt.Errorf("zipformerCTC model file not found: %s (%w)", cfg.Model.ZipformerCTC, err)
		}
	} else {
		if cfg.Model.Encoder == "" {
			return fmt.Errorf("encoder file path is empty")
		}
		if _, err := os.Stat(cfg.Model.Encoder); err != nil {
			return fmt.Errorf("encoder file not found: %s (%w)", cfg.Model.Encoder, err)
		}
		
		if cfg.Model.Decoder == "" {
			return fmt.Errorf("decoder file path is empty")
		}
		if _, err := os.Stat(cfg.Model.Decoder); err != nil {
			return fmt.Errorf("decoder file not found: %s (%w)", cfg.Model.Decoder, err)
		}
		
		if cfg.Model.Joiner == "" {
			return fmt.Errorf("joiner file path is empty")
		}
		if _, err := os.Stat(cfg.Model.Joiner); err != nil {
			return fmt.Errorf("joiner file not found: %s (%w)", cfg.Model.Joiner, err)
		}
	}
	
	return nil
}

// Close releases the underlying recognizer and stream
func (s *RealtimeService) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Printf("[RealtimeASR] Closing real-time ASR service...\n")
	if s.stream != nil {
		sherpa.DeleteOnlineStream(s.stream)
		s.stream = nil
		fmt.Printf("[RealtimeASR] Online stream deleted\n")
	}
	if s.recognizer != nil {
		sherpa.DeleteOnlineRecognizer(s.recognizer)
		s.recognizer = nil
		fmt.Printf("[RealtimeASR] Online recognizer deleted\n")
	}
	fmt.Printf("[RealtimeASR] Real-time ASR service closed\n")
}

// ProcessAudio processes a chunk of audio samples and returns partial/final results
// Returns (partialText, finalText, isFinal)
func (s *RealtimeService) ProcessAudio(samples []float32) (string, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.recognizer == nil || s.stream == nil {
		return "", "", false
	}

	if len(samples) == 0 {
		return "", "", false
	}

	// Feed samples to the stream
	s.stream.AcceptWaveform(s.sampleRate, samples)

	// Decode if ready
	for s.recognizer.IsReady(s.stream) {
		s.recognizer.Decode(s.stream)
	}

	// Get partial result
	res := s.recognizer.GetResult(s.stream)
	partialText := res.Text

	// Check if endpoint reached
	isFinal := s.recognizer.IsEndpoint(s.stream)
	finalText := ""
	if isFinal && partialText != "" {
		finalText = partialText
		// Reset stream for next utterance
		s.recognizer.Reset(s.stream)
	}

	return partialText, finalText, isFinal
}

// Flush processes any remaining audio and returns final result
func (s *RealtimeService) Flush() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	fmt.Printf("[RealtimeASR] Flushing remaining audio...\n")
	if s.recognizer == nil || s.stream == nil {
		fmt.Printf("[RealtimeASR] ERROR: Recognizer or stream is nil\n")
		return ""
	}

	// Signal end of input
	s.stream.InputFinished()
	fmt.Printf("[RealtimeASR] Input finished signal sent\n")

	// Decode remaining
	decodeCount := 0
	for s.recognizer.IsReady(s.stream) {
		s.recognizer.Decode(s.stream)
		decodeCount++
	}
	fmt.Printf("[RealtimeASR] Decoded %d times during flush\n", decodeCount)

	res := s.recognizer.GetResult(s.stream)
	finalText := res.Text
	fmt.Printf("[RealtimeASR] Flush result: %q\n", finalText)

	// Reset for next use
	s.recognizer.Reset(s.stream)
	fmt.Printf("[RealtimeASR] Stream reset after flush\n")

	return finalText
}

// Reset resets the stream state
func (s *RealtimeService) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.recognizer != nil && s.stream != nil {
		s.recognizer.Reset(s.stream)
	}
}

// onlineRecognizerConfig builds OnlineRecognizerConfig from Config
func (c *Config) onlineRecognizerConfig() *sherpa.OnlineRecognizerConfig {
	modelCfg := sherpa.OnlineModelConfig{
		Tokens:     c.Model.Tokens,
		NumThreads: c.Runtime.Threads,
		Provider:   c.Runtime.Provider,
	}

	if c.Model.ZipformerCTC != "" {
		// CTC model
		modelCfg.Zipformer2Ctc = sherpa.OnlineZipformer2CtcModelConfig{
			Model: c.Model.ZipformerCTC,
		}
	} else {
		// Transducer model
		modelCfg.Transducer = sherpa.OnlineTransducerModelConfig{
			Encoder: c.Model.Encoder,
			Decoder: c.Model.Decoder,
			Joiner:  c.Model.Joiner,
		}
		modelCfg.ModelType = "" // Let sherpa-onnx auto-detect
	}

	return &sherpa.OnlineRecognizerConfig{
		FeatConfig: sherpa.FeatureConfig{
			SampleRate: c.SampleRate,
			FeatureDim: 80,
		},
		ModelConfig:              modelCfg,
		DecodingMethod:           c.Decoding.Method,
		MaxActivePaths:           4,
		EnableEndpoint:          1,
		Rule1MinTrailingSilence:  0.8, // 0.8 seconds of silence triggers endpoint
		Rule2MinTrailingSilence:  1.2, // 1.2 seconds of silence triggers endpoint
		Rule3MinUtteranceLength: 0.5, // Minimum utterance length
		BlankPenalty:             0.0,
	}
}

