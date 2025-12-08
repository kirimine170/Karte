//go:build darwin && arm64 && !universal

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

// LogFunc is a function type for logging (can be nil)
type LogFunc func(format string, args ...interface{})

// NewRealtimeService creates a new real-time ASR service
// logFunc is optional - if provided, logs will be written via this function
// If nil, logs will only go to fmt.Printf (stdout)
func NewRealtimeService(cfg *Config) (*RealtimeService, error) {
	return NewRealtimeServiceWithLogger(cfg, nil)
}

// NewRealtimeServiceWithLogger creates a new real-time ASR service with custom logger
func NewRealtimeServiceWithLogger(cfg *Config, logFunc LogFunc) (*RealtimeService, error) {
	log := func(format string, args ...interface{}) {
		msg := fmt.Sprintf("[RealtimeASR] "+format, args...)
		fmt.Printf(msg + "\n")
		if logFunc != nil {
			logFunc(format, args...)
		}
	}

	log("Creating new real-time ASR service...")
	if cfg == nil {
		log("ERROR: nil config")
		return nil, errors.New("nil config")
	}
	log("Config is not nil, checking enabled flag...")
	if !cfg.Enabled {
		log("ERROR: ASR disabled in config")
		return nil, errors.New("asr disabled in config")
	}
	log("ASR is enabled, validating config...")
	if err := cfg.Validate(); err != nil {
		log("ERROR: Config validation failed: %v", err)
		return nil, err
	}
	log("Config validation passed")

	// Verify model files exist
	log("Verifying model files exist...")
	if err := verifyModelFiles(cfg); err != nil {
		log("ERROR: Model file verification failed: %v", err)
		return nil, fmt.Errorf("model file verification failed: %w", err)
	}
	log("Model files verified")

	log("Building online recognizer config...")
	onlineCfg := cfg.onlineRecognizerConfig()
	if onlineCfg == nil {
		log("ERROR: Failed to build online recognizer config")
		return nil, fmt.Errorf("failed to build online recognizer config")
	}
	log("Online recognizer config built successfully")
	log("Config details: SampleRate=%d, DecodingMethod=%s, MaxActivePaths=%d",
		onlineCfg.FeatConfig.SampleRate, onlineCfg.DecodingMethod, onlineCfg.MaxActivePaths)

	log("About to call sherpa.NewOnlineRecognizer (this may take a moment and may crash)...")
	rec := sherpa.NewOnlineRecognizer(onlineCfg)
	if rec == nil {
		log("ERROR: Failed to initialize online recognizer (returned nil)")
		return nil, fmt.Errorf("failed to initialize online recognizer")
	}
	log("Online recognizer initialized successfully")

	log("About to call sherpa.NewOnlineStream...")
	stream := sherpa.NewOnlineStream(rec)
	if stream == nil {
		log("ERROR: Failed to allocate online stream (returned nil)")
		if rec != nil {
			log("Cleaning up recognizer...")
			sherpa.DeleteOnlineRecognizer(rec)
		}
		return nil, fmt.Errorf("failed to allocate online stream")
	}
	log("Online stream allocated successfully (sampleRate=%d)", cfg.SampleRate)

	log("Real-time ASR service created successfully")
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
	fmt.Printf("[RealtimeASR] onlineRecognizerConfig: Building config with Tokens=%s, Encoder=%s, Decoder=%s, Joiner=%s\n",
		c.Model.Tokens, c.Model.Encoder, c.Model.Decoder, c.Model.Joiner)

	modelCfg := sherpa.OnlineModelConfig{
		Tokens:     c.Model.Tokens,
		NumThreads: c.Runtime.Threads,
		Provider:   c.Runtime.Provider,
	}
	fmt.Printf("[RealtimeASR] onlineRecognizerConfig: Model config created (Threads=%d, Provider=%s)\n",
		c.Runtime.Threads, c.Runtime.Provider)

	if c.Model.ZipformerCTC != "" {
		// CTC model
		fmt.Printf("[RealtimeASR] onlineRecognizerConfig: Using Zipformer2Ctc model\n")
		modelCfg.Zipformer2Ctc = sherpa.OnlineZipformer2CtcModelConfig{
			Model: c.Model.ZipformerCTC,
		}
	} else {
		// Transducer model
		fmt.Printf("[RealtimeASR] onlineRecognizerConfig: Using Transducer model\n")
		modelCfg.Transducer = sherpa.OnlineTransducerModelConfig{
			Encoder: c.Model.Encoder,
			Decoder: c.Model.Decoder,
			Joiner:  c.Model.Joiner,
		}
		modelCfg.ModelType = "" // Let sherpa-onnx auto-detect
		fmt.Printf("[RealtimeASR] onlineRecognizerConfig: Transducer config set (ModelType=auto-detect)\n")
	}

	fmt.Printf("[RealtimeASR] onlineRecognizerConfig: Creating OnlineRecognizerConfig...\n")
	cfg := &sherpa.OnlineRecognizerConfig{
		FeatConfig: sherpa.FeatureConfig{
			SampleRate: c.SampleRate,
			FeatureDim: 80,
		},
		ModelConfig:             modelCfg,
		DecodingMethod:          c.Decoding.Method,
		MaxActivePaths:          4,
		EnableEndpoint:          1,
		Rule1MinTrailingSilence: 0.8, // 0.8 seconds of silence triggers endpoint
		Rule2MinTrailingSilence: 1.2, // 1.2 seconds of silence triggers endpoint
		Rule3MinUtteranceLength: 0.5, // Minimum utterance length
		BlankPenalty:            0.0,
	}
	fmt.Printf("[RealtimeASR] onlineRecognizerConfig: Config created successfully\n")
	return cfg
}
