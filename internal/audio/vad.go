package audio

import "math"

// SimpleVAD is a basic energy-based voice activity detector.
type SimpleVAD struct {
	EnergyThreshold    float32
	SilenceFrames      int
	MinSpeechFrames    int
	consecutiveSilence int
	consecutiveSpeech  int
	inSpeech           bool
}

// NewSimpleVAD constructs a SimpleVAD with the given parameters.
func NewSimpleVAD(threshold float32, silenceFrames, minSpeechFrames int) *SimpleVAD {
	if silenceFrames <= 0 {
		silenceFrames = 30
	}
	if minSpeechFrames <= 0 {
		minSpeechFrames = 3
	}
	return &SimpleVAD{
		EnergyThreshold:    threshold,
		SilenceFrames:      silenceFrames,
		MinSpeechFrames:    minSpeechFrames,
		consecutiveSilence: 0,
		consecutiveSpeech:  0,
		inSpeech:           false,
	}
}

// DefaultSimpleVAD returns a VAD tuned for 10ms frames.
func DefaultSimpleVAD() *SimpleVAD {
	return NewSimpleVAD(0.01, 30, 3)
}

// Process ingests a frame and returns (isSpeech, shouldFlush).
func (v *SimpleVAD) Process(frame []float32) (bool, bool) {
	energy := v.calculateRMS(frame)

	if energy > v.EnergyThreshold {
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

// Reset clears internal counters.
func (v *SimpleVAD) Reset() {
	v.consecutiveSilence = 0
	v.consecutiveSpeech = 0
	v.inSpeech = false
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
