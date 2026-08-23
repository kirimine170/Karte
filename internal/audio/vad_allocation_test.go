package audio

import "testing"

func TestSimpleVADProcessReusesDynamicThresholdScratch(t *testing.T) {
	vad := DefaultSimpleVAD()
	frame := make([]float32, RecordingFrameSize)
	for index := range frame {
		frame[index] = 0.01
	}
	for range 200 {
		vad.Process(frame)
	}
	initialLength := len(vad.energyScratch)
	initialCapacity := cap(vad.energyScratch)
	allocations := testing.AllocsPerRun(1_000, func() {
		vad.Process(frame)
	})
	if allocations != 0 {
		t.Fatalf("VAD frame allocations = %.2f，want 0", allocations)
	}
	if len(vad.energyScratch) != initialLength || cap(vad.energyScratch) != initialCapacity {
		t.Fatalf("VAD scratch changed from %d/%d to %d/%d", initialLength, initialCapacity, len(vad.energyScratch), cap(vad.energyScratch))
	}
}
