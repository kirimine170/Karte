package audio

import (
	"runtime"
	"sync"
	"sync/atomic"
)

const (
	DefaultRecordingFrameSlots = 64
	MaxRecordingFrameSamples   = RecordingFrameSize
)

// RecorderStats exposes fixed-buffer pressure without retaining recorded
// audio．AcceptedSamples is the exact number delivered to the consumer．
type RecorderStats struct {
	PoolSlots       int    `json:"poolSlots"`
	SlotSamples     int    `json:"slotSamples"`
	AcceptedFrames  uint64 `json:"acceptedFrames"`
	AcceptedSamples uint64 `json:"acceptedSamples"`
	DroppedFrames   uint64 `json:"droppedFrames"`
	DroppedSamples  uint64 `json:"droppedSamples"`
	OversizedFrames uint64 `json:"oversizedFrames"`
	PeakQueued      uint64 `json:"peakQueued"`
}

type RecordingFrameRing struct {
	slots   [][]float32
	lengths []int
	offsets []uint64
	free    chan int
	ready   chan int
	stop    chan struct{}

	stopOnce        sync.Once
	accepting       atomic.Bool
	inFlightSubmits atomic.Int64
	acceptedFrames  atomic.Uint64
	acceptedSamples atomic.Uint64
	droppedFrames   atomic.Uint64
	droppedSamples  atomic.Uint64
	oversizedFrames atomic.Uint64
	queued          atomic.Uint64
	peakQueued      atomic.Uint64
	overflowSignal  chan struct{}

	testBeforeSecondAdmission func()
}

func NewRecordingFrameRing(slotCount, slotSamples int) *RecordingFrameRing {
	if slotCount <= 0 {
		slotCount = DefaultRecordingFrameSlots
	}
	if slotSamples <= 0 {
		slotSamples = MaxRecordingFrameSamples
	}
	ring := &RecordingFrameRing{
		slots:          make([][]float32, slotCount),
		lengths:        make([]int, slotCount),
		offsets:        make([]uint64, slotCount),
		free:           make(chan int, slotCount),
		ready:          make(chan int, slotCount),
		stop:           make(chan struct{}),
		overflowSignal: make(chan struct{}, 1),
	}
	for index := range ring.slots {
		ring.slots[index] = make([]float32, slotSamples)
		ring.free <- index
	}
	ring.accepting.Store(true)
	return ring
}

// submit is audio-thread safe and nonblocking．It performs exactly one copy
// into a preallocated slot and never grows retained memory．
func (ring *RecordingFrameRing) TrySubmit(samples []float32) bool {
	return ring.TrySubmitAt(samples, 0)
}

// TrySubmitAt preserves a caller-defined absolute sample offset alongside the
// pooled frame．The offset lets downstream processing detect an exact gap
// without allocating metadata objects per callback．
func (ring *RecordingFrameRing) TrySubmitAt(samples []float32, offset uint64) bool {
	if ring == nil || !ring.accepting.Load() || len(samples) == 0 {
		return false
	}
	// StopAccepting waits for every submit that crossed the first admission
	// check before it closes stop．A submit that loses the second check never
	// touches a slot，which prevents a ready frame from arriving after Run has
	// completed its final drain．
	ring.inFlightSubmits.Add(1)
	defer ring.inFlightSubmits.Add(-1)
	if ring.testBeforeSecondAdmission != nil {
		ring.testBeforeSecondAdmission()
	}
	if !ring.accepting.Load() {
		return false
	}
	if len(samples) > len(ring.slots[0]) {
		ring.oversizedFrames.Add(1)
		ring.recordDrop(len(samples))
		return false
	}
	var index int
	select {
	case index = <-ring.free:
	default:
		ring.recordDrop(len(samples))
		return false
	}
	copy(ring.slots[index][:len(samples)], samples)
	ring.lengths[index] = len(samples)
	ring.offsets[index] = offset
	queued := ring.queued.Add(1)
	updateAtomicMaximum(&ring.peakQueued, queued)
	select {
	case ring.ready <- index:
		ring.acceptedFrames.Add(1)
		ring.acceptedSamples.Add(uint64(len(samples)))
		return true
	default:
		// The ready/free channels have the same capacity，so this is only a
		// defensive fallback for invariant violations．The slot is returned．
		ring.queued.Add(^uint64(0))
		ring.free <- index
		ring.recordDrop(len(samples))
		return false
	}
}

func (ring *RecordingFrameRing) recordDrop(sampleCount int) {
	ring.droppedFrames.Add(1)
	ring.droppedSamples.Add(uint64(sampleCount))
	select {
	case ring.overflowSignal <- struct{}{}:
	default:
	}
}

func updateAtomicMaximum(value *atomic.Uint64, candidate uint64) {
	for {
		current := value.Load()
		if candidate <= current || value.CompareAndSwap(current, candidate) {
			return
		}
	}
}

func (ring *RecordingFrameRing) StopAccepting() {
	if ring == nil {
		return
	}
	ring.accepting.Store(false)
	// Producers execute only bounded copies and nonblocking channel operations，
	// so this wait is finite without ever blocking the realtime submit path．
	for ring.inFlightSubmits.Load() != 0 {
		runtime.Gosched()
	}
	ring.stopOnce.Do(func() { close(ring.stop) })
}

func (ring *RecordingFrameRing) Run(consume func([]float32), overflow func(RecorderStats)) {
	ring.run(func(samples []float32, _ uint64) {
		if consume != nil {
			consume(samples)
		}
	}, overflow)
}

func (ring *RecordingFrameRing) RunWithOffsets(consume func([]float32, uint64), overflow func(RecorderStats)) {
	ring.run(consume, overflow)
}

func (ring *RecordingFrameRing) run(consume func([]float32, uint64), overflow func(RecorderStats)) {
	if ring == nil {
		return
	}
	for {
		select {
		case index := <-ring.ready:
			ring.consumeSlot(index, consume)
		case <-ring.overflowSignal:
			if overflow != nil {
				overflow(ring.Stats())
			}
		case <-ring.stop:
			for {
				select {
				case index := <-ring.ready:
					ring.consumeSlot(index, consume)
				default:
					if overflow != nil && ring.droppedFrames.Load() > 0 {
						overflow(ring.Stats())
					}
					return
				}
			}
		}
	}
}

func (ring *RecordingFrameRing) consumeSlot(index int, consume func([]float32, uint64)) {
	length := ring.lengths[index]
	if consume != nil {
		consume(ring.slots[index][:length], ring.offsets[index])
	}
	ring.lengths[index] = 0
	ring.offsets[index] = 0
	ring.queued.Add(^uint64(0))
	ring.free <- index
}

func (ring *RecordingFrameRing) Stats() RecorderStats {
	if ring == nil {
		return RecorderStats{}
	}
	slotSamples := 0
	if len(ring.slots) > 0 {
		slotSamples = len(ring.slots[0])
	}
	return RecorderStats{
		PoolSlots:       len(ring.slots),
		SlotSamples:     slotSamples,
		AcceptedFrames:  ring.acceptedFrames.Load(),
		AcceptedSamples: ring.acceptedSamples.Load(),
		DroppedFrames:   ring.droppedFrames.Load(),
		DroppedSamples:  ring.droppedSamples.Load(),
		OversizedFrames: ring.oversizedFrames.Load(),
		PeakQueued:      ring.peakQueued.Load(),
	}
}
