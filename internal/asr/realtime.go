package asr

import (
	"errors"
	"fmt"
	"os"
	"sync"
)

type onlineStreamAdapter interface {
	AcceptWaveform(int, []float32)
	InputFinished()
	Close()
}

type onlineRecognizerAdapter interface {
	NewStream() (onlineStreamAdapter, error)
	IsReady(onlineStreamAdapter) bool
	Decode(onlineStreamAdapter)
	Result(onlineStreamAdapter) string
	IsEndpoint(onlineStreamAdapter) bool
	Reset(onlineStreamAdapter)
	Close()
}

type onlineRecognizerSpec struct {
	SampleRate int
	Model      ModelSpec
	Decoding   DecodeSpec
	Runtime    RuntimeSpec
}

var (
	errOnlineRecognizerUnavailable = errors.New("online recognizer unavailable")
	errOnlineStreamUnavailable     = errors.New("online stream unavailable")
)

// RealtimeService provides real-time ASR using a platform recognizer adapter．
type RealtimeService struct {
	cfg        *Config
	recognizer onlineRecognizerAdapter
	stream     onlineStreamAdapter
	mu         sync.Mutex
	sampleRate int
}

// LogFunc is a function type for logging．
type LogFunc func(format string, args ...interface{})

func NewRealtimeService(config *Config) (*RealtimeService, error) {
	return NewRealtimeServiceWithLogger(config, nil)
}

func NewRealtimeServiceWithLogger(config *Config, logFunc LogFunc) (*RealtimeService, error) {
	if err := realtimePlatformError(); err != nil {
		return nil, err
	}
	log := func(format string, args ...interface{}) {
		message := fmt.Sprintf("[RealtimeASR] "+format, args...)
		fmt.Printf("%s\n", message)
		if logFunc != nil {
			logFunc(format, args...)
		}
	}

	log("Creating new real-time ASR service...")
	if config == nil {
		log("ERROR: nil config")
		return nil, errors.New("nil config")
	}
	log("Config is not nil, checking enabled flag...")
	if !config.Enabled {
		log("ERROR: ASR disabled in config")
		return nil, errors.New("asr disabled in config")
	}
	log("ASR is enabled, validating config...")
	if err := config.Validate(); err != nil {
		log("ERROR: Config validation failed: %v", err)
		return nil, err
	}
	log("Config validation passed")

	log("Verifying model files exist...")
	if err := verifyModelFiles(config); err != nil {
		log("ERROR: Model file verification failed: %v", err)
		return nil, fmt.Errorf("model file verification failed: %w", err)
	}
	log("Model files verified")

	spec := onlineRecognizerSpec{
		SampleRate: config.SampleRate,
		Model:      config.Model,
		Decoding:   config.Decoding,
		Runtime:    config.Runtime,
	}
	log("Building online recognizer config...")
	log("Online recognizer config built successfully")
	log("Config details: SampleRate=%d, DecodingMethod=%s, MaxActivePaths=%d", spec.SampleRate, spec.Decoding.Method, 4)
	log("About to call sherpa.NewOnlineRecognizer (this may take a moment and may crash)...")
	recognizer, err := newOnlineRecognizerAdapter(spec)
	if err != nil {
		if errors.Is(err, errOnlineRecognizerUnavailable) {
			log("ERROR: Failed to initialize online recognizer (returned nil)")
			return nil, fmt.Errorf("failed to initialize online recognizer")
		}
		return nil, err
	}
	log("Online recognizer initialized successfully")

	log("About to call sherpa.NewOnlineStream...")
	stream, err := recognizer.NewStream()
	if err != nil {
		log("ERROR: Failed to allocate online stream (returned nil)")
		log("Cleaning up recognizer...")
		recognizer.Close()
		if errors.Is(err, errOnlineStreamUnavailable) {
			return nil, fmt.Errorf("failed to allocate online stream")
		}
		return nil, err
	}
	log("Online stream allocated successfully (sampleRate=%d)", config.SampleRate)
	log("Real-time ASR service created successfully")
	return &RealtimeService{
		cfg:        config,
		recognizer: recognizer,
		stream:     stream,
		sampleRate: config.SampleRate,
	}, nil
}

func verifyModelFiles(config *Config) error {
	if config.Model.Tokens == "" {
		return fmt.Errorf("tokens file path is empty")
	}
	if _, err := os.Stat(config.Model.Tokens); err != nil {
		return fmt.Errorf("tokens file not found: %s (%w)", config.Model.Tokens, err)
	}

	if config.Model.ZipformerCTC != "" {
		if _, err := os.Stat(config.Model.ZipformerCTC); err != nil {
			return fmt.Errorf("zipformerCTC model file not found: %s (%w)", config.Model.ZipformerCTC, err)
		}
		return nil
	}
	if config.Model.Encoder == "" {
		return fmt.Errorf("encoder file path is empty")
	}
	if _, err := os.Stat(config.Model.Encoder); err != nil {
		return fmt.Errorf("encoder file not found: %s (%w)", config.Model.Encoder, err)
	}
	if config.Model.Decoder == "" {
		return fmt.Errorf("decoder file path is empty")
	}
	if _, err := os.Stat(config.Model.Decoder); err != nil {
		return fmt.Errorf("decoder file not found: %s (%w)", config.Model.Decoder, err)
	}
	if config.Model.Joiner == "" {
		return fmt.Errorf("joiner file path is empty")
	}
	if _, err := os.Stat(config.Model.Joiner); err != nil {
		return fmt.Errorf("joiner file not found: %s (%w)", config.Model.Joiner, err)
	}
	return nil
}

func (service *RealtimeService) Close() {
	if service == nil || realtimePlatformError() != nil {
		return
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	fmt.Printf("[RealtimeASR] Closing real-time ASR service...\n")
	if service.stream != nil {
		service.stream.Close()
		service.stream = nil
		fmt.Printf("[RealtimeASR] Online stream deleted\n")
	}
	if service.recognizer != nil {
		service.recognizer.Close()
		service.recognizer = nil
		fmt.Printf("[RealtimeASR] Online recognizer deleted\n")
	}
	fmt.Printf("[RealtimeASR] Real-time ASR service closed\n")
}

func (service *RealtimeService) ProcessAudio(samples []float32) (string, string, bool) {
	if service == nil || realtimePlatformError() != nil {
		return "", "", false
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.recognizer == nil || service.stream == nil || len(samples) == 0 {
		return "", "", false
	}
	service.stream.AcceptWaveform(service.sampleRate, samples)
	for service.recognizer.IsReady(service.stream) {
		service.recognizer.Decode(service.stream)
	}
	partialText := service.recognizer.Result(service.stream)
	isFinal := service.recognizer.IsEndpoint(service.stream)
	finalText := ""
	if isFinal && partialText != "" {
		finalText = partialText
		service.recognizer.Reset(service.stream)
	}
	return partialText, finalText, isFinal
}

func (service *RealtimeService) Flush() string {
	if service == nil || realtimePlatformError() != nil {
		return ""
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	fmt.Printf("[RealtimeASR] Flushing remaining audio...\n")
	if service.recognizer == nil || service.stream == nil {
		fmt.Printf("[RealtimeASR] ERROR: Recognizer or stream is nil\n")
		return ""
	}
	service.stream.InputFinished()
	fmt.Printf("[RealtimeASR] Input finished signal sent\n")
	decodeCount := 0
	for service.recognizer.IsReady(service.stream) {
		service.recognizer.Decode(service.stream)
		decodeCount++
	}
	fmt.Printf("[RealtimeASR] Decoded %d times during flush\n", decodeCount)
	finalText := service.recognizer.Result(service.stream)
	fmt.Printf("[RealtimeASR] Flush result: %q\n", finalText)
	service.recognizer.Reset(service.stream)
	fmt.Printf("[RealtimeASR] Stream reset after flush\n")
	return finalText
}

func (service *RealtimeService) Reset() {
	if service == nil || realtimePlatformError() != nil {
		return
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.recognizer != nil && service.stream != nil {
		service.recognizer.Reset(service.stream)
	}
}
