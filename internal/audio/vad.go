package audio

import "math"

// SimpleVAD is an improved energy-based voice activity detector with:
// - Dynamic threshold based on noise floor estimation
// - EMA smoothing for energy values
// - Hysteresis (separate thresholds for speech start/stop)
type SimpleVAD struct {
	// Static parameters
	EnergyThreshold float32 // <= 0 means dynamic threshold mode
	SilenceFrames   int
	MinSpeechFrames int

	// Dynamic threshold parameters (used when EnergyThreshold <= 0)
	StartFactor float32 // Multiplier for noise floor to determine speech start threshold
	StopFactor  float32 // Multiplier for noise floor to determine speech stop threshold

	// EMA smoothing parameters
	EMAAlpha float32 // EMA smoothing factor (0.0-1.0), 0 means no smoothing

	// Internal state
	consecutiveSilence int
	consecutiveSpeech  int
	inSpeech           bool

	// Dynamic threshold state
	energyEMA     float32   // Smoothed energy value
	noiseFloor    float32   // Estimated noise floor
	energyHist    []float32 // History for noise floor estimation (circular buffer)
	energyScratch []float32 // Preallocated percentile scratch，never grows per frame
	energyHistIdx int       // Current index in circular buffer

	// Computed thresholds (updated dynamically)
	thStart float32 // Threshold for speech start
	thStop  float32 // Threshold for speech stop
}

// NewSimpleVAD constructs a SimpleVAD with the given parameters.
// If threshold <= 0, dynamic threshold mode is enabled (recommended).
func NewSimpleVAD(threshold float32, silenceFrames, minSpeechFrames int) *SimpleVAD {
	if silenceFrames <= 0 {
		silenceFrames = 30
	}
	if minSpeechFrames <= 0 {
		minSpeechFrames = 20 // Default: 200ms for 10ms frames
	}

	vad := &SimpleVAD{
		EnergyThreshold:    threshold,
		SilenceFrames:      silenceFrames,
		MinSpeechFrames:    minSpeechFrames,
		consecutiveSilence: 0,
		consecutiveSpeech:  0,
		inSpeech:           false,
		StartFactor:        2.0,                  // Default: noise floor * 2.0 for speech start
		StopFactor:         1.3,                  // Default: noise floor * 1.3 for speech stop
		EMAAlpha:           0.2,                  // Default: EMA smoothing factor
		energyHist:         make([]float32, 100), // Store last 100 frames (~1 second) for noise floor estimation
		energyScratch:      make([]float32, 100),
		energyHistIdx:      0,
	}

	// Initialize thresholds
	if threshold <= 0 {
		// Dynamic mode: will be computed from noise floor
		vad.thStart = 0.02 // Initial guess, will be updated
		vad.thStop = 0.013
	} else {
		// Static mode: use same threshold for start and stop
		vad.thStart = threshold
		vad.thStop = threshold
	}

	return vad
}

// DefaultSimpleVAD returns a VAD tuned for 10ms frames with improved settings:
// - Dynamic threshold mode (EnergyThreshold = 0)
// - MinSpeechFrames = 20 (200ms minimum segment length)
// - EMA smoothing enabled
// - Hysteresis enabled (separate start/stop thresholds)
func DefaultSimpleVAD() *SimpleVAD {
	return NewSimpleVAD(0, 30, 20) // threshold=0 enables dynamic mode
}

// Process ingests a frame and returns (isSpeech, shouldFlush).
func (v *SimpleVAD) Process(frame []float32) (bool, bool) {
	rawEnergy := v.calculateRMS(frame)

	// Update energy history for noise floor estimation (in dynamic mode)
	if v.EnergyThreshold <= 0 {
		v.updateEnergyHistory(rawEnergy)
	}

	// Apply EMA smoothing if enabled
	energy := rawEnergy
	if v.EMAAlpha > 0 {
		if v.energyEMA == 0 {
			v.energyEMA = rawEnergy // Initialize with first value
		} else {
			v.energyEMA = v.EMAAlpha*rawEnergy + (1-v.EMAAlpha)*v.energyEMA
		}
		energy = v.energyEMA
	}

	// Update dynamic thresholds if in dynamic mode
	if v.EnergyThreshold <= 0 {
		v.updateDynamicThresholds()
	}

	// Determine which threshold to use based on current state
	threshold := v.thStart
	if v.inSpeech {
		threshold = v.thStop // Use lower threshold when already in speech (hysteresis)
	}

	// Speech detection logic
	if energy > threshold {
		v.consecutiveSilence = 0
		v.consecutiveSpeech++
		if !v.inSpeech && v.consecutiveSpeech >= v.MinSpeechFrames {
			v.inSpeech = true
		}
	} else {
		v.consecutiveSpeech = 0
		v.consecutiveSilence++
		if v.inSpeech && v.consecutiveSilence >= v.SilenceFrames {
			v.inSpeech = false
			return false, true
		}
	}

	return v.inSpeech, false
}

// Reset clears internal counters and resets state.
func (v *SimpleVAD) Reset() {
	v.consecutiveSilence = 0
	v.consecutiveSpeech = 0
	v.inSpeech = false
	v.energyEMA = 0
	// Reset energy history for noise floor estimation
	if v.energyHist != nil {
		for i := range v.energyHist {
			v.energyHist[i] = 0
		}
		v.energyHistIdx = 0
	}
	v.noiseFloor = 0
	// Reset thresholds to initial values
	if v.EnergyThreshold <= 0 {
		v.thStart = 0.02
		v.thStop = 0.013
	}
}

func (v *SimpleVAD) calculateRMS(frame []float32) float32 {
	if len(frame) == 0 {
		return 0
	}
	var sum float64
	for _, s := range frame {
		sum += float64(s * s)
	}
	return float32(math.Sqrt(sum / float64(len(frame))))
}

// updateEnergyHistory adds the current energy to the history buffer.
func (v *SimpleVAD) updateEnergyHistory(energy float32) {
	if v.energyHist == nil || len(v.energyHist) == 0 {
		return
	}
	v.energyHist[v.energyHistIdx] = energy
	v.energyHistIdx = (v.energyHistIdx + 1) % len(v.energyHist)
}

// updateDynamicThresholds estimates the noise floor and updates thresholds.
func (v *SimpleVAD) updateDynamicThresholds() {
	if v.energyHist == nil || len(v.energyHist) == 0 {
		return
	}

	if len(v.energyScratch) < len(v.energyHist) {
		// Custom callers may replace energyHist after construction．Grow once to
		// match configuration，never as a consequence of frame content．
		v.energyScratch = make([]float32, len(v.energyHist))
	}
	energyCount := 0
	for _, e := range v.energyHist {
		if e > 0 {
			v.energyScratch[energyCount] = e
			energyCount++
		}
	}

	if energyCount < 10 {
		// Not enough data yet, use default thresholds
		return
	}

	// Sort to find the noise floor (lower 10-20% of energy distribution)
	// Simple approach: use the minimum of recent values as noise floor estimate
	// More sophisticated: use percentile of sorted values
	sorted := v.energyScratch[:energyCount]
	// Insertion sort avoids a closure and heap allocation on this small fixed
	// history while retaining deterministic percentile semantics．
	for i := 1; i < len(sorted); i++ {
		value := sorted[i]
		j := i - 1
		for j >= 0 && sorted[j] > value {
			sorted[j+1] = sorted[j]
			j--
		}
		sorted[j+1] = value
	}

	// Use the 10th percentile as noise floor estimate
	percentileIdx := len(sorted) / 10
	if percentileIdx < 1 {
		percentileIdx = 1
	}
	estimatedNoiseFloor := sorted[percentileIdx]

	// Update noise floor with EMA to avoid sudden jumps
	if v.noiseFloor == 0 {
		v.noiseFloor = estimatedNoiseFloor
	} else {
		// Use slower EMA for noise floor (alpha = 0.1) to be more stable
		v.noiseFloor = 0.1*estimatedNoiseFloor + 0.9*v.noiseFloor
	}

	// Ensure minimum noise floor to avoid division by zero or too low thresholds
	if v.noiseFloor < 0.001 {
		v.noiseFloor = 0.001
	}

	// Update thresholds based on noise floor
	v.thStart = v.noiseFloor * v.StartFactor
	v.thStop = v.noiseFloor * v.StopFactor

	// Ensure thresholds are reasonable (not too low or too high)
	if v.thStart < 0.005 {
		v.thStart = 0.005
	}
	if v.thStop < 0.003 {
		v.thStop = 0.003
	}
	if v.thStart > 0.1 {
		v.thStart = 0.1
	}
	if v.thStop > 0.08 {
		v.thStop = 0.08
	}
}
