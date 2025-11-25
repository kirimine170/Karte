package audio

import (
	"fmt"
	"sync"

	"github.com/gordonklaus/portaudio"
)

const (
	// RecordingSampleRate is the sample rate for recording (16kHz)
	RecordingSampleRate = 16000
	// RecordingFrameSize is the frame size for recording (10ms @ 16kHz = 160 samples)
	RecordingFrameSize = 160
)

var (
	portAudioInitialized bool
	portAudioMu          sync.Mutex
)

// Recorder handles microphone input and audio recording
type Recorder struct {
	stream     *portaudio.Stream
	running    bool
	stopCh     chan struct{}
	stopChMu   sync.Mutex // Protects stopCh from concurrent close
	callback   func([]float32)
	sampleCh   chan []float32 // Channel for passing samples to callback goroutine
	wg         sync.WaitGroup
	mu         sync.Mutex
	samples    []float32      // Buffer for recorded samples
	callbackWg sync.WaitGroup // WaitGroup for callback goroutine
}

// NewRecorder creates a new recorder instance
func NewRecorder() (*Recorder, error) {
	// Initialize PortAudio only once
	portAudioMu.Lock()
	defer portAudioMu.Unlock()

	if !portAudioInitialized {
		if err := portaudio.Initialize(); err != nil {
			return nil, fmt.Errorf("failed to initialize PortAudio: %w", err)
		}
		portAudioInitialized = true
		fmt.Printf("[Recorder] PortAudio initialized\n")
	} else {
		fmt.Printf("[Recorder] PortAudio already initialized, reusing\n")
	}

	fmt.Printf("[Recorder] Creating new recorder instance\n")
	// Use buffered channel to avoid blocking audio callback
	// Buffer size: 10 frames (100ms @ 10ms per frame) to handle temporary delays
	return &Recorder{
		stopCh:   make(chan struct{}),
		sampleCh: make(chan []float32, 10),
		samples:  make([]float32, 0),
	}, nil
}

// Start begins recording from the microphone
func (r *Recorder) Start(callback func(samples []float32)) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.running {
		fmt.Printf("[Recorder] Start called but already running\n")
		return fmt.Errorf("recorder already running")
	}

	fmt.Printf("[Recorder] Starting recording...\n")
	r.callback = callback
	r.running = true
	r.samples = make([]float32, 0) // Reset samples buffer

	// Start callback goroutine to process samples from channel
	// This separates audio thread from callback execution to avoid CGO issues
	callbackStarted := false
	if callback != nil {
		r.callbackWg.Add(1)
		go r.callbackLoop()
		callbackStarted = true
	}

	// Get default input device
	fmt.Printf("[Recorder] Getting default input device...\n")
	inputDevice, err := portaudio.DefaultInputDevice()
	if err != nil {
		fmt.Printf("[Recorder] ERROR: Failed to get default input device: %v\n", err)
		errMsg := fmt.Errorf("failed to get default input device: %w", err)
		// Check if this might be a permission issue
		if err.Error() != "" {
			fmt.Printf("[Recorder] NOTE: If this is a permission error, please grant microphone access in System Settings > Privacy & Security > Microphone\n")
		}
		// Cleanup callback goroutine if started
		if callbackStarted {
			r.stopChMu.Lock()
			select {
			case <-r.stopCh:
				// Already closed
			default:
				close(r.stopCh)
			}
			r.stopChMu.Unlock()
			r.callbackWg.Wait()
			// Recreate stopCh for next attempt
			r.stopCh = make(chan struct{})
		}
		r.running = false
		return errMsg
	}
	fmt.Printf("[Recorder] Default input device: %s (latency: %v)\n", inputDevice.Name, inputDevice.DefaultLowInputLatency)

	// Set stream parameters
	params := portaudio.StreamParameters{
		Input: portaudio.StreamDeviceParameters{
			Device:   inputDevice,
			Channels: 1, // Mono
			Latency:  inputDevice.DefaultLowInputLatency,
		},
		SampleRate:      RecordingSampleRate,
		FramesPerBuffer: RecordingFrameSize,
	}
	fmt.Printf("[Recorder] Stream parameters: sampleRate=%d, framesPerBuffer=%d, channels=1\n", RecordingSampleRate, RecordingFrameSize)

	// Open stream (input only)
	fmt.Printf("[Recorder] Opening audio stream...\n")
	stream, err := portaudio.OpenStream(params, func(in []float32) {
		r.processAudio(in)
	})
	if err != nil {
		fmt.Printf("[Recorder] ERROR: Failed to open stream: %v\n", err)
		errMsg := fmt.Errorf("failed to open stream: %w", err)
		// Check if this might be a permission issue
		if err.Error() != "" {
			fmt.Printf("[Recorder] NOTE: If this is a permission error, please grant microphone access in System Settings > Privacy & Security > Microphone\n")
			fmt.Printf("[Recorder] NOTE: Make sure NSMicrophoneUsageDescription is set in Info.plist\n")
		}
		// Cleanup callback goroutine if started
		if callbackStarted {
			r.stopChMu.Lock()
			select {
			case <-r.stopCh:
				// Already closed
			default:
				close(r.stopCh)
			}
			r.stopChMu.Unlock()
			r.callbackWg.Wait()
			// Recreate stopCh for next attempt
			r.stopCh = make(chan struct{})
		}
		r.running = false
		return errMsg
	}
	fmt.Printf("[Recorder] Audio stream opened successfully\n")

	r.stream = stream

	// Start stream
	fmt.Printf("[Recorder] Starting audio stream...\n")
	if err := r.stream.Start(); err != nil {
		fmt.Printf("[Recorder] ERROR: Failed to start stream: %v\n", err)
		r.stream.Close()
		// Cleanup callback goroutine if started
		if callbackStarted {
			r.stopChMu.Lock()
			select {
			case <-r.stopCh:
				// Already closed
			default:
				close(r.stopCh)
			}
			r.stopChMu.Unlock()
			r.callbackWg.Wait()
			// Recreate stopCh for next attempt
			r.stopCh = make(chan struct{})
		}
		r.running = false
		return fmt.Errorf("failed to start stream: %w", err)
	}
	fmt.Printf("[Recorder] Audio stream started successfully, recording active\n")

	// Wait for stop in a goroutine
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		<-r.stopCh
		fmt.Printf("[Recorder] Stop signal received in goroutine\n")
	}()

	return nil
}

// processAudio is called by PortAudio for each audio frame
// This runs in the audio thread, so we must avoid CGO calls here.
// Instead, we send samples to a channel and process them in a separate goroutine.
func (r *Recorder) processAudio(in []float32) {
	select {
	case <-r.stopCh:
		return
	default:
	}

	r.mu.Lock()
	// Append to samples buffer
	frame := make([]float32, len(in))
	copy(frame, in)
	r.samples = append(r.samples, frame...)
	r.mu.Unlock()

	// Send samples to channel for processing in separate goroutine
	// Use non-blocking send to avoid blocking audio thread
	if r.callback != nil {
		frameCopy := make([]float32, len(in))
		copy(frameCopy, in)
		select {
		case r.sampleCh <- frameCopy:
			// Successfully sent
		default:
			// Channel full, skip this frame (shouldn't happen with buffer size 10)
			fmt.Printf("[Recorder] WARNING: Sample channel full, dropping frame\n")
		}
	}
}

// callbackLoop processes samples from the channel in a separate goroutine
// This allows CGO calls (like ASR processing) to run safely outside the audio thread
func (r *Recorder) callbackLoop() {
	defer r.callbackWg.Done()
	for {
		select {
		case <-r.stopCh:
			// Drain remaining samples before exiting
			for {
				select {
				case samples := <-r.sampleCh:
					if r.callback != nil {
						r.callback(samples)
					}
				default:
					// No more samples, exit
					return
				}
			}
		case samples := <-r.sampleCh:
			if r.callback != nil {
				r.callback(samples)
			}
		}
	}
}

// Stop stops recording
func (r *Recorder) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.running {
		fmt.Printf("[Recorder] Stop called but not running\n")
		return nil
	}

	fmt.Printf("[Recorder] Stopping recording...\n")
	// Safely close stopCh (only once)
	r.stopChMu.Lock()
	select {
	case <-r.stopCh:
		// Already closed
		r.stopChMu.Unlock()
	default:
		close(r.stopCh)
		r.stopChMu.Unlock()
	}
	r.running = false

	if r.stream != nil {
		fmt.Printf("[Recorder] Stopping audio stream...\n")
		if err := r.stream.Stop(); err != nil {
			fmt.Printf("[Recorder] ERROR: Failed to stop stream: %v\n", err)
			return fmt.Errorf("failed to stop stream: %w", err)
		}
		fmt.Printf("[Recorder] Closing audio stream...\n")
		if err := r.stream.Close(); err != nil {
			fmt.Printf("[Recorder] ERROR: Failed to close stream: %v\n", err)
			return fmt.Errorf("failed to close stream: %w", err)
		}
		r.stream = nil
		fmt.Printf("[Recorder] Audio stream closed\n")
	}

	fmt.Printf("[Recorder] Waiting for goroutines to finish...\n")
	r.wg.Wait()
	if r.callback != nil {
		r.callbackWg.Wait()
	}

	sampleCount := len(r.samples)
	duration := float64(sampleCount) / float64(RecordingSampleRate)
	fmt.Printf("[Recorder] Recording stopped: %d samples (%.2f seconds)\n", sampleCount, duration)
	return nil
}

// GetSamples returns all recorded samples
func (r *Recorder) GetSamples() []float32 {
	r.mu.Lock()
	defer r.mu.Unlock()

	samples := make([]float32, len(r.samples))
	copy(samples, r.samples)
	return samples
}

// GetDuration returns the duration of recorded audio in seconds
func (r *Recorder) GetDuration() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return float64(len(r.samples)) / float64(RecordingSampleRate)
}

// Close releases resources
// Note: PortAudio is not terminated here to allow reuse.
// It should be terminated only when the application exits.
func (r *Recorder) Close() error {
	return r.Stop()
}

// IsRunning returns whether the recorder is currently running
func (r *Recorder) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}
