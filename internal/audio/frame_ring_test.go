package audio

import (
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"
)

func TestRecordingFrameRingIsFixedAndDrainsEveryAcceptedFrame(t *testing.T) {
	const (
		slots       = 8
		slotSamples = 160
		extraFrames = 10_000
	)
	ring := NewRecordingFrameRing(slots, slotSamples)
	frame := make([]float32, slotSamples)
	for index := range frame {
		frame[index] = float32(index)
	}
	for index := 0; index < slots+extraFrames; index++ {
		ring.TrySubmit(frame)
	}
	before := ring.Stats()
	if before.PoolSlots != slots || before.SlotSamples != slotSamples {
		t.Fatalf("retained pool changed: %+v", before)
	}
	if before.AcceptedFrames != slots || before.DroppedFrames != extraFrames {
		t.Fatalf("unexpected admission stats: %+v", before)
	}

	var consumed atomic.Uint64
	var overflow atomic.Uint64
	done := make(chan struct{})
	go func() {
		ring.Run(func(samples []float32) {
			if len(samples) != slotSamples || samples[37] != 37 {
				t.Errorf("corrupt pooled frame: len=%d sample=%v", len(samples), samples[37])
			}
			consumed.Add(1)
		}, func(stats RecorderStats) {
			overflow.Store(stats.DroppedFrames)
		})
		close(done)
	}()
	ring.StopAccepting()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("frame consumer did not drain")
	}
	if got := consumed.Load(); got != slots {
		t.Fatalf("consumed frames = %d，want %d", got, slots)
	}
	if got := overflow.Load(); got != extraFrames {
		t.Fatalf("observable dropped frames = %d，want %d", got, extraFrames)
	}
	after := ring.Stats()
	if after.PoolSlots != slots || after.SlotSamples != slotSamples || after.PeakQueued > slots {
		t.Fatalf("long burst grew retained buffers: %+v", after)
	}
}

func TestRecordingFrameRingReusesSlotsAndAcceptsVariableFrameSizes(t *testing.T) {
	ring := NewRecordingFrameRing(2, 160)
	lengths := make(chan int, 4)
	addresses := make(chan uintptr, 4)
	done := make(chan struct{})
	go func() {
		ring.Run(func(samples []float32) {
			lengths <- len(samples)
			addresses <- uintptr(unsafe.Pointer(&samples[0]))
		}, nil)
		close(done)
	}()

	for _, length := range []int{80, 160, 40, 160} {
		frame := make([]float32, length)
		if !ring.TrySubmit(frame) {
			t.Fatalf("frame length %d was unexpectedly dropped", length)
		}
		select {
		case got := <-lengths:
			if got != length {
				t.Fatalf("consumed length = %d，want %d", got, length)
			}
		case <-time.After(time.Second):
			t.Fatal("consumer did not receive frame")
		}
	}
	if ring.TrySubmit(make([]float32, 161)) {
		t.Fatal("oversized frame was accepted")
	}
	ring.StopAccepting()
	<-done
	close(addresses)
	unique := map[uintptr]struct{}{}
	for address := range addresses {
		unique[address] = struct{}{}
	}
	if len(unique) > 2 {
		t.Fatalf("pool exposed %d slot addresses，want at most 2", len(unique))
	}
	if stats := ring.Stats(); stats.DroppedFrames != 1 {
		t.Fatalf("oversized frame drop was not observable: %+v", stats)
	}
}

func TestRecordingFrameRingSubmitAndRecycleAllocateNothing(t *testing.T) {
	ring := NewRecordingFrameRing(1, 160)
	frame := make([]float32, 160)
	var failed atomic.Bool
	allocations := testing.AllocsPerRun(1_000, func() {
		if !ring.TrySubmit(frame) {
			failed.Store(true)
			return
		}
		index := <-ring.ready
		ring.consumeSlot(index, nil)
	})
	if failed.Load() {
		t.Fatal("fixed ring unexpectedly rejected recycled slot")
	}
	if allocations != 0 {
		t.Fatalf("submit/recycle allocations = %.2f，want 0", allocations)
	}
}

func TestRecordingFrameRingConcurrentProducerConsumerIsBounded(t *testing.T) {
	ring := NewRecordingFrameRing(16, 160)
	frame := make([]float32, 160)
	var consumed atomic.Uint64
	done := make(chan struct{})
	go func() {
		ring.Run(func([]float32) { consumed.Add(1) }, nil)
		close(done)
	}()
	var producers sync.WaitGroup
	producers.Add(4)
	for range 4 {
		go func() {
			defer producers.Done()
			for range 2_500 {
				ring.TrySubmit(frame)
			}
		}()
	}
	producers.Wait()
	ring.StopAccepting()
	<-done
	stats := ring.Stats()
	if consumed.Load() != stats.AcceptedFrames {
		t.Fatalf("consumer=%d accepted=%d", consumed.Load(), stats.AcceptedFrames)
	}
	if stats.AcceptedFrames+stats.DroppedFrames != 10_000 {
		t.Fatalf("frames were neither accepted nor dropped: %+v", stats)
	}
	if stats.PeakQueued > 16 {
		t.Fatalf("peak queue exceeded capacity: %+v", stats)
	}
}

func TestRecordingFrameRingPreservesAbsoluteOffsetsAcrossDrops(t *testing.T) {
	ring := NewRecordingFrameRing(2, 160)
	if !ring.TrySubmitAt(make([]float32, 80), 0) || !ring.TrySubmitAt(make([]float32, 160), 80) {
		t.Fatal("initial offset frames were not accepted")
	}
	if ring.TrySubmitAt(make([]float32, 40), 240) {
		t.Fatal("full ring accepted a frame")
	}
	var offsets []uint64
	done := make(chan struct{})
	go func() {
		ring.RunWithOffsets(func(_ []float32, offset uint64) {
			offsets = append(offsets, offset)
		}, nil)
		close(done)
	}()
	ring.StopAccepting()
	<-done
	if !reflect.DeepEqual(offsets, []uint64{0, 80}) {
		t.Fatalf("offsets = %v", offsets)
	}
	stats := ring.Stats()
	if stats.DroppedFrames != 1 || stats.DroppedSamples != 40 {
		t.Fatalf("drop stats = %+v", stats)
	}
}

func TestRecordingFrameRingStopWaitsForInFlightAdmissionBeforeFinalDrain(t *testing.T) {
	ring := NewRecordingFrameRing(1, 160)
	entered := make(chan struct{})
	release := make(chan struct{})
	ring.testBeforeSecondAdmission = func() {
		close(entered)
		<-release
	}
	submitDone := make(chan bool, 1)
	go func() {
		submitDone <- ring.TrySubmit(make([]float32, 160))
	}()
	<-entered

	stopDone := make(chan struct{})
	go func() {
		ring.StopAccepting()
		close(stopDone)
	}()
	select {
	case <-stopDone:
		t.Fatal("StopAccepting returned while a submit was in flight")
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	if accepted := <-submitDone; accepted {
		t.Fatal("submit crossed the stop admission boundary")
	}
	select {
	case <-stopDone:
	case <-time.After(time.Second):
		t.Fatal("StopAccepting did not finish after submit left")
	}

	consumerDone := make(chan struct{})
	go func() {
		ring.Run(func([]float32) { t.Error("stopped submit was consumed") }, nil)
		close(consumerDone)
	}()
	select {
	case <-consumerDone:
	case <-time.After(time.Second):
		t.Fatal("consumer did not observe stopped ring")
	}
}
