package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"karte/internal/resourcebudget"
)

const resourceBudgetBaselinePath = "resource-budget/baseline.json"

func TestResourceBudgetGate(t *testing.T) {
	baseline, err := resourcebudget.LoadBaseline(resourceBudgetBaselinePath)
	if err != nil {
		t.Fatal(err)
	}

	previousGOMAXPROCS := runtime.GOMAXPROCS(baseline.Policy.GOMAXPROCS)
	t.Cleanup(func() { runtime.GOMAXPROCS(previousGOMAXPROCS) })

	measurements := make([]resourcebudget.Measurement, 0)
	measurements = append(measurements, measureResourceScenario(t, baseline.Policy, measureIdleResources)...)
	measurements = append(measurements, measureResourceScenario(t, baseline.Policy, measureContinuousInputResources)...)
	measurements = append(measurements, measureResourceScenario(t, baseline.Policy, func() (map[string]float64, error) {
		return measureGraphResources(t)
	})...)
	measurements = append(measurements, measureResourceScenario(t, baseline.Policy, measureConcurrentHeavyResources)...)
	sort.Slice(measurements, func(i, j int) bool { return measurements[i].ID < measurements[j].ID })

	report := resourcebudget.Report{
		SchemaVersion: resourcebudget.SchemaVersion,
		Suite:         baseline.Suite,
		Source:        "backend",
		Policy:        baseline.Policy,
		Environment: map[string]string{
			"go":     runtime.Version(),
			"goarch": runtime.GOARCH,
			"goos":   runtime.GOOS,
		},
		Measurements: measurements,
	}
	if reportPath := os.Getenv("KARTE_RESOURCE_REPORT"); reportPath != "" {
		if err := resourcebudget.WriteReport(reportPath, report); err != nil {
			t.Fatalf("write backend resource report: %v", err)
		}
	}
	backendBaseline := baselineForResourceSource(baseline, "backend")
	evaluation := resourcebudget.Evaluate(backendBaseline, report)
	if !evaluation.Pass {
		t.Fatalf("backend resource budget failed:\n%s", resourcebudget.MarkdownSummary(backendBaseline, evaluation))
	}
}

func measureResourceScenario(
	t *testing.T,
	policy resourcebudget.SamplingPolicy,
	measure func() (map[string]float64, error),
) []resourcebudget.Measurement {
	t.Helper()
	samplesByID := make(map[string][]float64)
	unitsByID := make(map[string]string)
	for iteration := 0; iteration < policy.Warmup+policy.Samples; iteration++ {
		values, err := measure()
		if err != nil {
			t.Fatalf("resource scenario iteration %d: %v", iteration+1, err)
		}
		if len(values) == 0 {
			t.Fatalf("resource scenario iteration %d produced no metrics", iteration+1)
		}
		if iteration < policy.Warmup {
			continue
		}
		for encodedID, value := range values {
			id, unit, ok := splitResourceMetric(encodedID)
			if !ok {
				t.Fatalf("resource metric %q must end in #unit", encodedID)
			}
			if existing, found := unitsByID[id]; found && existing != unit {
				t.Fatalf("resource metric %s changed unit from %s to %s", id, existing, unit)
			}
			unitsByID[id] = unit
			samplesByID[id] = append(samplesByID[id], value)
		}
	}

	measurements := make([]resourcebudget.Measurement, 0, len(samplesByID))
	for id, samples := range samplesByID {
		if len(samples) != policy.Samples {
			t.Fatalf("resource metric %s has %d samples，want %d", id, len(samples), policy.Samples)
		}
		measurements = append(measurements, resourcebudget.Measurement{
			ID:        id,
			Unit:      unitsByID[id],
			Statistic: "median",
			Value:     resourcebudget.Median(samples),
			Samples:   samples,
		})
	}
	return measurements
}

func measureIdleResources() (map[string]float64, error) {
	before := runtime.NumGoroutine()
	manager, cancel, err := startResourceJobManager(jobManagerConfig{Workers: 4, MaxPending: 8})
	if err != nil {
		return nil, err
	}
	stats := manager.Stats()
	activeDelta := positiveResourceDelta(runtime.NumGoroutine(), before)
	if err := shutdownResourceJobManager(manager, cancel); err != nil {
		return nil, err
	}
	runtime.Gosched()
	retainedDelta := positiveResourceDelta(runtime.NumGoroutine(), before)
	return map[string]float64{
		"backend.idle.live_workers#count":             float64(stats.LiveWorkers),
		"backend.idle.goroutine_delta#count":          float64(activeDelta),
		"backend.idle.retained_goroutine_delta#count": float64(retainedDelta),
	}, nil
}

func measureContinuousInputResources() (map[string]float64, error) {
	manager, cancel, err := startResourceJobManager(jobManagerConfig{Workers: 1, MaxPending: 4})
	if err != nil {
		return nil, err
	}
	defer func() { _ = shutdownResourceJobManager(manager, cancel) }()

	blockerStarted := make(chan struct{})
	releaseBlocker := make(chan struct{})
	blocker := manager.Submit(resourceManagedJob("resource-blocker", "running", func(ctx context.Context) error {
		close(blockerStarted)
		select {
		case <-releaseBlocker:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}))
	if blocker.Err != nil || blocker.Handle == nil {
		return nil, fmt.Errorf("submit continuous-input blocker: %v", blocker.Err)
	}
	if err := waitResourceSignal(blockerStarted); err != nil {
		return nil, err
	}

	beforeGoroutines := runtime.NumGoroutine()
	var beforeMemory runtime.MemStats
	runtime.ReadMemStats(&beforeMemory)
	startedAt := time.Now()
	var final jobSubmission
	var latestRan atomic.Int64
	for index := 0; index < 10_000; index++ {
		value := index
		spec := resourceManagedJob("resource-input", "latest", func(context.Context) error {
			latestRan.Store(int64(value))
			return nil
		})
		spec.Coalesce = jobReplacePending
		final = manager.Submit(spec)
		if final.Err != nil || final.Handle == nil {
			return nil, fmt.Errorf("submit continuous input %d: %v", index, final.Err)
		}
	}
	latency := time.Since(startedAt)
	var afterMemory runtime.MemStats
	runtime.ReadMemStats(&afterMemory)
	stats := manager.Stats()
	goroutineDelta := positiveResourceDelta(runtime.NumGoroutine(), beforeGoroutines)
	close(releaseBlocker)
	if err := waitResourceHandle(final.Handle); err != nil {
		return nil, fmt.Errorf("wait latest continuous input: %w", err)
	}
	if err := waitResourceHandle(blocker.Handle); err != nil {
		return nil, fmt.Errorf("wait continuous-input blocker: %w", err)
	}
	if latestRan.Load() != 9_999 {
		return nil, fmt.Errorf("latest continuous input value=%d，want 9999", latestRan.Load())
	}
	if err := shutdownResourceJobManager(manager, cancel); err != nil {
		return nil, err
	}

	return map[string]float64{
		"backend.continuous_input.submissions#operations":  10_000,
		"backend.continuous_input.peak_pending#count":      float64(stats.PeakPending),
		"backend.continuous_input.goroutine_delta#count":   float64(goroutineDelta),
		"backend.continuous_input.alloc_bytes#bytes":       float64(afterMemory.TotalAlloc - beforeMemory.TotalAlloc),
		"backend.continuous_input.latency_ms#milliseconds": float64(latency.Microseconds()) / 1_000,
	}, nil
}

func measureGraphResources(t *testing.T) (map[string]float64, error) {
	t.Helper()
	graph := GraphData{
		Nodes: make([]GraphNode, 1_000),
		Edges: make([]GraphEdge, 999),
		Meta:  GraphMeta{Directed: true},
	}
	for index := range graph.Nodes {
		graph.Nodes[index] = GraphNode{
			ID:     fmt.Sprintf("doc:/fixture/%04d.md", index),
			Label:  fmt.Sprintf("Fixture %04d", index),
			Kind:   "note",
			Exists: true,
			Tags:   []string{"resource-fixture"},
		}
		if index > 0 {
			graph.Edges[index-1] = GraphEdge{
				ID:     fmt.Sprintf("edge-%04d", index-1),
				Source: graph.Nodes[index-1].ID,
				Target: graph.Nodes[index].ID,
				Kind:   "wikilink",
				Weight: 1,
			}
		}
	}
	app := &App{
		graphCacheLoaded: true,
		graphCacheState:  graphCache{Snapshot: graph},
	}

	allocations := testing.AllocsPerRun(3, func() {
		if _, err := app.GetGraphData(); err != nil {
			panic(err)
		}
	})
	var beforeMemory runtime.MemStats
	runtime.ReadMemStats(&beforeMemory)
	startedAt := time.Now()
	snapshot, err := app.GetGraphData()
	latency := time.Since(startedAt)
	var afterMemory runtime.MemStats
	runtime.ReadMemStats(&afterMemory)
	if err != nil {
		return nil, err
	}
	return map[string]float64{
		"backend.graph_1000.nodes#count":                    float64(len(snapshot.Nodes)),
		"backend.graph_1000.edges#count":                    float64(len(snapshot.Edges)),
		"backend.graph_1000.allocations_per_op#allocations": allocations,
		"backend.graph_1000.alloc_bytes_per_op#bytes":       float64(afterMemory.TotalAlloc - beforeMemory.TotalAlloc),
		"backend.graph_1000.latency_ms#milliseconds":        float64(latency.Microseconds()) / 1_000,
	}, nil
}

func measureConcurrentHeavyResources() (map[string]float64, error) {
	beforeGoroutines := runtime.NumGoroutine()
	manager, cancel, err := startResourceJobManager(jobManagerConfig{
		Workers:    4,
		MaxPending: 8,
		CategoryLimits: map[string]int{
			"synthetic-asr": 1,
			"synthetic-llm": 1,
		},
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = shutdownResourceJobManager(manager, cancel) }()

	release := make(chan struct{})
	asrStarted := make(chan struct{}, 2)
	llmStarted := make(chan struct{}, 2)
	const workPerJob = 100_000
	var checksum atomic.Uint64
	makeRun := func(started chan<- struct{}) func(context.Context) error {
		return func(ctx context.Context) error {
			started <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			var local uint64
			for index := uint64(0); index < workPerJob; index++ {
				local = local*33 + index + 17
			}
			checksum.Add(local)
			return nil
		}
	}
	handles := make([]*jobHandle, 0, 4)
	startedAt := time.Now()
	for index := 0; index < 2; index++ {
		asr := manager.Submit(resourceManagedJob("synthetic-asr", fmt.Sprintf("asr-%d", index), makeRun(asrStarted)))
		llm := manager.Submit(resourceManagedJob("synthetic-llm", fmt.Sprintf("llm-%d", index), makeRun(llmStarted)))
		if asr.Err != nil || asr.Handle == nil || llm.Err != nil || llm.Handle == nil {
			return nil, fmt.Errorf("submit concurrent synthetic jobs: asr=%v，llm=%v", asr.Err, llm.Err)
		}
		handles = append(handles, asr.Handle, llm.Handle)
	}
	if err := waitResourceSignal(asrStarted); err != nil {
		return nil, fmt.Errorf("wait synthetic ASR: %w", err)
	}
	if err := waitResourceSignal(llmStarted); err != nil {
		return nil, fmt.Errorf("wait synthetic LLM: %w", err)
	}
	stats := manager.Stats()
	goroutineDelta := positiveResourceDelta(runtime.NumGoroutine(), beforeGoroutines)
	close(release)
	for _, handle := range handles {
		if err := waitResourceHandle(handle); err != nil {
			return nil, fmt.Errorf("wait concurrent synthetic job: %w", err)
		}
	}
	latency := time.Since(startedAt)
	if checksum.Load() == 0 {
		return nil, fmt.Errorf("synthetic work was optimized away")
	}
	if err := shutdownResourceJobManager(manager, cancel); err != nil {
		return nil, err
	}
	return map[string]float64{
		"backend.concurrent_heavy.running#count":           float64(stats.Running),
		"backend.concurrent_heavy.pending#count":           float64(stats.Pending),
		"backend.concurrent_heavy.goroutine_delta#count":   float64(goroutineDelta),
		"backend.concurrent_heavy.work_units#operations":   4 * workPerJob,
		"backend.concurrent_heavy.latency_ms#milliseconds": float64(latency.Microseconds()) / 1_000,
	}, nil
}

func resourceManagedJob(category, key string, run func(context.Context) error) managedJob {
	return managedJob{
		Category: category,
		Key:      key,
		Priority: jobPriorityNormal,
		Coalesce: jobKeepExisting,
		Context:  context.Background(),
		Run:      run,
	}
}

func startResourceJobManager(config jobManagerConfig) (*jobManager, context.CancelFunc, error) {
	parent, cancel := context.WithCancel(context.Background())
	manager := newJobManager(config)
	workerStarted := make(chan struct{}, config.Workers)
	if !manager.Start(parent, func(worker func(context.Context)) bool {
		go func() {
			workerStarted <- struct{}{}
			worker(parent)
		}()
		return true
	}) {
		cancel()
		return nil, nil, fmt.Errorf("start resource job manager")
	}
	for index := 0; index < manager.Stats().Workers; index++ {
		if err := waitResourceSignal(workerStarted); err != nil {
			cancel()
			return nil, nil, err
		}
	}
	return manager, cancel, nil
}

func shutdownResourceJobManager(manager *jobManager, cancel context.CancelFunc) error {
	ctx, cancelShutdown := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelShutdown()
	if !manager.Shutdown(ctx) {
		cancel()
		return fmt.Errorf("resource job manager shutdown timed out")
	}
	cancel()
	return nil
}

func waitResourceSignal(signal <-chan struct{}) error {
	select {
	case <-signal:
		return nil
	case <-time.After(2 * time.Second):
		return fmt.Errorf("resource scenario signal timed out")
	}
}

func waitResourceHandle(handle *jobHandle) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return handle.Wait(ctx)
}

func positiveResourceDelta(after, before int) int {
	if after <= before {
		return 0
	}
	return after - before
}

func splitResourceMetric(encoded string) (string, string, bool) {
	for index := len(encoded) - 1; index >= 0; index-- {
		if encoded[index] == '#' {
			return encoded[:index], encoded[index+1:], index > 0 && index < len(encoded)-1
		}
	}
	return "", "", false
}

func baselineForResourceSource(baseline resourcebudget.Baseline, source string) resourcebudget.Baseline {
	filtered := baseline
	filtered.Metrics = nil
	for _, metric := range baseline.Metrics {
		if metric.Source == source {
			filtered.Metrics = append(filtered.Metrics, metric)
		}
	}
	return filtered
}
