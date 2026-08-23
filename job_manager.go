package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

const (
	defaultJobManagerWorkers       = 4
	defaultJobManagerMaxPending    = 64
	defaultJobManagerAgingInterval = uint64(8)
)

var (
	errJobManagerNotStarted = errors.New("job manager is not started")
	errJobManagerClosed     = errors.New("job manager is closed")
	errJobQueueFull         = errors.New("job queue is full")
	errJobReplaced          = errors.New("job was replaced by newer work")
	errJobManagerReentrant  = errors.New("job manager SubmitAndWait cannot run from one of its jobs")
	errInvalidJob           = errors.New("invalid job")
)

type jobPriority uint8

const (
	jobPriorityLow jobPriority = iota
	jobPriorityNormal
	jobPriorityHigh
	jobPriorityCritical
)

type jobCoalesceMode uint8

const (
	jobKeepExisting jobCoalesceMode = iota
	jobReplacePending
)

type jobAdmissionStatus string

const (
	jobAccepted         jobAdmissionStatus = "accepted"
	jobDeduplicated     jobAdmissionStatus = "deduplicated"
	jobReplacedPending  jobAdmissionStatus = "replaced"
	jobRejectedFull     jobAdmissionStatus = "rejected-full"
	jobRejectedClosed   jobAdmissionStatus = "rejected-closed"
	jobRejectedCanceled jobAdmissionStatus = "rejected-canceled"
	jobRejectedInvalid  jobAdmissionStatus = "rejected-invalid"
)

type managedJob struct {
	Category string
	Group    string
	Key      string
	Priority jobPriority
	Coalesce jobCoalesceMode
	Context  context.Context
	Run      func(context.Context) error
	// OnFinish runs outside manager.mu and may reenter jobManager．It should remain
	// short and is invoked exactly once for success，error，replacement，or cancellation．
	OnFinish func(error)
}

type jobManagerConfig struct {
	Workers        int
	MaxPending     int
	AgingInterval  uint64
	CategoryLimits map[string]int
	OnPanic        func(managedJob, any)
}

type jobSubmission struct {
	Status  jobAdmissionStatus
	Handle  *jobHandle
	Pending int
	Err     error
}

type jobManagerStats struct {
	Started        bool
	Closing        bool
	Workers        int
	LiveWorkers    int
	Pending        int
	Running        int
	PeakPending    int
	PeakRunning    int
	Dispatches     uint64
	RunningByGroup map[string]int
}

type jobIdentity struct {
	category string
	key      string
}

type jobEntryState uint8

const (
	jobEntryPending jobEntryState = iota
	jobEntryRunning
	jobEntryCompleted
)

type jobEntry struct {
	id                 uint64
	sequence           uint64
	enqueuedAtDispatch uint64
	identity           jobIdentity
	spec               managedJob
	state              jobEntryState
	completion         *jobCompletion
	contextGeneration  uint64
	stopPendingContext func() bool
	runCancel          context.CancelFunc
	stopRunContext     func() bool
}

type jobCompletion struct {
	done     chan struct{}
	once     sync.Once
	mu       sync.Mutex
	err      error
	onFinish func(error)
}

func newJobCompletion(onFinish func(error)) *jobCompletion {
	completion := &jobCompletion{done: make(chan struct{})}
	if onFinish != nil {
		completion.onFinish = onFinish
	}
	return completion
}

func (completion *jobCompletion) complete(err error) func() {
	if completion == nil {
		return nil
	}
	var callback func()
	completion.once.Do(func() {
		completion.mu.Lock()
		completion.err = err
		completion.mu.Unlock()
		close(completion.done)
		if completion.onFinish != nil {
			callback = func() {
				defer func() { _ = recover() }()
				completion.onFinish(err)
			}
		}
	})
	return callback
}

func runJobCompletionCallbacks(callbacks ...func()) {
	for _, callback := range callbacks {
		if callback != nil {
			callback()
		}
	}
}

func (completion *jobCompletion) wait(ctx context.Context) error {
	if completion == nil {
		return errInvalidJob
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-completion.done:
		completion.mu.Lock()
		defer completion.mu.Unlock()
		return completion.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

type jobHandle struct {
	manager    *jobManager
	id         uint64
	completion *jobCompletion
}

func (handle *jobHandle) Wait(ctx context.Context) error {
	if handle == nil {
		return errInvalidJob
	}
	return handle.completion.wait(ctx)
}

func (handle *jobHandle) Cancel() bool {
	if handle == nil || handle.manager == nil {
		return false
	}
	return handle.manager.cancelID(handle.id)
}

type jobManagerContextKey struct{}

type jobManager struct {
	mu   sync.Mutex
	cond *sync.Cond

	config jobManagerConfig
	ctx    context.Context
	cancel context.CancelFunc

	started          bool
	closing          bool
	stopParentCancel func() bool
	done             chan struct{}
	doneOnce         sync.Once
	liveWorkers      int

	pending       []*jobEntry
	pendingByKey  map[jobIdentity]*jobEntry
	runningByKey  map[jobIdentity]*jobEntry
	entriesByID   map[uint64]*jobEntry
	runningGroups map[string]int

	nextID       uint64
	nextSequence uint64
	dispatches   uint64
	peakPending  int
	peakRunning  int
	totalRunning int
}

func newJobManager(config jobManagerConfig) *jobManager {
	if config.Workers <= 0 {
		config.Workers = defaultJobManagerWorkers
	}
	if config.MaxPending <= 0 {
		config.MaxPending = defaultJobManagerMaxPending
	}
	if config.AgingInterval == 0 {
		config.AgingInterval = defaultJobManagerAgingInterval
	}
	limits := make(map[string]int, len(config.CategoryLimits))
	for category, limit := range config.CategoryLimits {
		category = strings.TrimSpace(category)
		if category == "" || limit <= 0 {
			continue
		}
		if limit > config.Workers {
			limit = config.Workers
		}
		limits[category] = limit
	}
	config.CategoryLimits = limits

	manager := &jobManager{
		config:        config,
		done:          make(chan struct{}),
		pendingByKey:  make(map[jobIdentity]*jobEntry),
		runningByKey:  make(map[jobIdentity]*jobEntry),
		entriesByID:   make(map[uint64]*jobEntry),
		runningGroups: make(map[string]int),
	}
	manager.cond = sync.NewCond(&manager.mu)
	return manager
}

// Start launches a fixed worker set through launch．App passes
// appLifecycle.goWorker so lifecycle owns the goroutines while jobManager owns
// admission，scheduling，cancellation，and completion．
func (manager *jobManager) Start(parent context.Context, launch func(func(context.Context)) bool) bool {
	if manager == nil || launch == nil {
		return false
	}
	if parent == nil {
		parent = context.Background()
	}

	manager.mu.Lock()
	if manager.started || manager.closing {
		manager.mu.Unlock()
		return false
	}
	manager.started = true
	manager.ctx, manager.cancel = context.WithCancel(parent)
	manager.liveWorkers = manager.config.Workers
	manager.stopParentCancel = context.AfterFunc(parent, func() {
		manager.Close()
	})
	manager.mu.Unlock()

	started := 0
	for index := 0; index < manager.config.Workers; index++ {
		if !launch(manager.worker) {
			manager.workerExited()
			continue
		}
		started++
	}
	if started != manager.config.Workers {
		manager.Close()
		return false
	}
	return true
}

func (manager *jobManager) Submit(spec managedJob) jobSubmission {
	if manager == nil {
		return jobSubmission{Status: jobRejectedClosed, Err: errJobManagerClosed}
	}
	category := strings.TrimSpace(spec.Category)
	key := strings.TrimSpace(spec.Key)
	if category == "" || key == "" || spec.Run == nil || spec.Priority > jobPriorityCritical {
		return jobSubmission{Status: jobRejectedInvalid, Err: errInvalidJob}
	}
	if spec.Coalesce != jobKeepExisting && spec.Coalesce != jobReplacePending {
		return jobSubmission{Status: jobRejectedInvalid, Err: errInvalidJob}
	}
	if spec.Context == nil {
		spec.Context = context.Background()
	}
	if err := spec.Context.Err(); err != nil {
		return jobSubmission{Status: jobRejectedCanceled, Err: err}
	}
	spec.Category = category
	spec.Key = key
	identity := jobIdentity{category: category, key: key}

	manager.mu.Lock()
	var completionCallback func()
	defer func() {
		manager.mu.Unlock()
		runJobCompletionCallbacks(completionCallback)
	}()
	if !manager.started {
		return jobSubmission{Status: jobRejectedClosed, Pending: len(manager.pending), Err: errJobManagerNotStarted}
	}
	if manager.closing || manager.ctx == nil || manager.ctx.Err() != nil {
		return jobSubmission{Status: jobRejectedClosed, Pending: len(manager.pending), Err: errJobManagerClosed}
	}

	if pending := manager.pendingByKey[identity]; pending != nil {
		if spec.Coalesce == jobKeepExisting {
			return manager.submissionLocked(jobDeduplicated, pending, nil)
		}
		oldCompletion := pending.completion
		manager.removePendingLocked(pending)
		completionCallback = oldCompletion.complete(errJobReplaced)
		entry := manager.newPendingEntryLocked(identity, spec)
		manager.cond.Broadcast()
		return manager.submissionLocked(jobReplacedPending, entry, nil)
	}
	if running := manager.runningByKey[identity]; running != nil && spec.Coalesce == jobKeepExisting {
		return manager.submissionLocked(jobDeduplicated, running, nil)
	}
	if len(manager.pending) >= manager.config.MaxPending {
		return jobSubmission{Status: jobRejectedFull, Pending: len(manager.pending), Err: errJobQueueFull}
	}

	entry := manager.newPendingEntryLocked(identity, spec)
	manager.cond.Broadcast()
	return manager.submissionLocked(jobAccepted, entry, nil)
}

// SubmitAndWait is for coordinator goroutines，not managed jobs．Calling it
// with a managed job context is rejected because all workers may otherwise
// wait for work that only those same workers can execute．
func (manager *jobManager) SubmitAndWait(ctx context.Context, spec managedJob) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Value(jobManagerContextKey{}) == manager {
		return errJobManagerReentrant
	}
	spec.Context = ctx
	submission := manager.Submit(spec)
	if submission.Err != nil {
		return submission.Err
	}
	if submission.Handle == nil {
		return errInvalidJob
	}
	if err := submission.Handle.Wait(ctx); err != nil {
		if ctx.Err() != nil {
			// A deduplicated handle belongs to the original submitter．The caller
			// may stop waiting without canceling that shared job．
			if submission.Status == jobAccepted || submission.Status == jobReplacedPending {
				submission.Handle.Cancel()
			}
			return ctx.Err()
		}
		return err
	}
	return nil
}

func (manager *jobManager) Cancel(category, key string) bool {
	if manager == nil {
		return false
	}
	identity := jobIdentity{category: strings.TrimSpace(category), key: strings.TrimSpace(key)}
	manager.mu.Lock()
	canceled := false
	var completionCallback func()
	if pending := manager.pendingByKey[identity]; pending != nil {
		completionCallback = manager.cancelPendingLocked(pending, context.Canceled)
		canceled = true
	}
	if running := manager.runningByKey[identity]; running != nil && running.runCancel != nil {
		running.runCancel()
		canceled = true
	}
	manager.cond.Broadcast()
	manager.mu.Unlock()
	runJobCompletionCallbacks(completionCallback)
	return canceled
}

func (manager *jobManager) CancelCategory(category string) int {
	if manager == nil {
		return 0
	}
	category = strings.TrimSpace(category)
	manager.mu.Lock()
	canceled := 0
	callbacks := make([]func(), 0)
	for _, entry := range append([]*jobEntry(nil), manager.pending...) {
		if entry.identity.category == category {
			callbacks = append(callbacks, manager.cancelPendingLocked(entry, context.Canceled))
			canceled++
		}
	}
	for _, entry := range manager.runningByKey {
		if entry.identity.category == category && entry.runCancel != nil {
			entry.runCancel()
			canceled++
		}
	}
	manager.cond.Broadcast()
	manager.mu.Unlock()
	runJobCompletionCallbacks(callbacks...)
	return canceled
}

func (manager *jobManager) CancelGroup(group string) int {
	if manager == nil {
		return 0
	}
	group = strings.TrimSpace(group)
	if group == "" {
		return 0
	}
	manager.mu.Lock()
	canceled := 0
	callbacks := make([]func(), 0)
	for _, entry := range append([]*jobEntry(nil), manager.pending...) {
		if entry.spec.Group == group {
			callbacks = append(callbacks, manager.cancelPendingLocked(entry, context.Canceled))
			canceled++
		}
	}
	for _, entry := range manager.runningByKey {
		if entry.spec.Group == group && entry.runCancel != nil {
			entry.runCancel()
			canceled++
		}
	}
	manager.cond.Broadcast()
	manager.mu.Unlock()
	runJobCompletionCallbacks(callbacks...)
	return canceled
}

func (manager *jobManager) cancelID(id uint64) bool {
	if manager == nil {
		return false
	}
	manager.mu.Lock()
	entry := manager.entriesByID[id]
	if entry == nil {
		manager.mu.Unlock()
		return false
	}
	var completionCallback func()
	switch entry.state {
	case jobEntryPending:
		completionCallback = manager.cancelPendingLocked(entry, context.Canceled)
	case jobEntryRunning:
		if entry.runCancel != nil {
			entry.runCancel()
		}
	default:
		manager.mu.Unlock()
		return false
	}
	manager.cond.Broadcast()
	manager.mu.Unlock()
	runJobCompletionCallbacks(completionCallback)
	return true
}

func (manager *jobManager) cancelPendingGeneration(id, generation uint64, err error) {
	manager.mu.Lock()
	entry := manager.entriesByID[id]
	var completionCallback func()
	if entry != nil && entry.state == jobEntryPending && entry.contextGeneration == generation {
		completionCallback = manager.cancelPendingLocked(entry, err)
		manager.cond.Broadcast()
	}
	manager.mu.Unlock()
	runJobCompletionCallbacks(completionCallback)
}

// Close atomically rejects new submissions and cancels pending and running jobs．
func (manager *jobManager) Close() bool {
	if manager == nil {
		return false
	}
	manager.mu.Lock()
	if manager.closing {
		manager.mu.Unlock()
		return false
	}
	manager.closing = true
	if manager.cancel != nil {
		manager.cancel()
	}
	pending := append([]*jobEntry(nil), manager.pending...)
	callbacks := make([]func(), 0, len(pending))
	for _, entry := range pending {
		callbacks = append(callbacks, manager.cancelPendingLocked(entry, context.Canceled))
	}
	for _, entry := range manager.runningByKey {
		if entry.runCancel != nil {
			entry.runCancel()
		}
	}
	stopParentCancel := manager.stopParentCancel
	manager.stopParentCancel = nil
	manager.cond.Broadcast()
	if !manager.started || manager.liveWorkers == 0 {
		manager.doneOnce.Do(func() { close(manager.done) })
	}
	manager.mu.Unlock()
	runJobCompletionCallbacks(callbacks...)
	if stopParentCancel != nil {
		stopParentCancel()
	}
	return true
}

func (manager *jobManager) Shutdown(ctx context.Context) bool {
	if manager == nil {
		return true
	}
	manager.Close()
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-manager.done:
		return true
	case <-ctx.Done():
		return false
	}
}

func (manager *jobManager) Stats() jobManagerStats {
	if manager == nil {
		return jobManagerStats{}
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	runningGroups := make(map[string]int, len(manager.runningGroups))
	for category, running := range manager.runningGroups {
		runningGroups[category] = running
	}
	return jobManagerStats{
		Started:        manager.started,
		Closing:        manager.closing,
		Workers:        manager.config.Workers,
		LiveWorkers:    manager.liveWorkers,
		Pending:        len(manager.pending),
		Running:        manager.totalRunning,
		PeakPending:    manager.peakPending,
		PeakRunning:    manager.peakRunning,
		Dispatches:     manager.dispatches,
		RunningByGroup: runningGroups,
	}
}

func (manager *jobManager) newPendingEntryLocked(identity jobIdentity, spec managedJob) *jobEntry {
	manager.nextID++
	manager.nextSequence++
	entry := &jobEntry{
		id:                 manager.nextID,
		sequence:           manager.nextSequence,
		enqueuedAtDispatch: manager.dispatches,
		identity:           identity,
		spec:               spec,
		state:              jobEntryPending,
		completion:         newJobCompletion(spec.OnFinish),
		contextGeneration:  1,
	}
	manager.pending = append(manager.pending, entry)
	manager.pendingByKey[identity] = entry
	manager.entriesByID[entry.id] = entry
	if len(manager.pending) > manager.peakPending {
		manager.peakPending = len(manager.pending)
	}
	generation := entry.contextGeneration
	entry.stopPendingContext = context.AfterFunc(spec.Context, func() {
		manager.cancelPendingGeneration(entry.id, generation, spec.Context.Err())
	})
	return entry
}

func (manager *jobManager) submissionLocked(status jobAdmissionStatus, entry *jobEntry, err error) jobSubmission {
	var handle *jobHandle
	if entry != nil {
		handle = &jobHandle{manager: manager, id: entry.id, completion: entry.completion}
	}
	return jobSubmission{Status: status, Handle: handle, Pending: len(manager.pending), Err: err}
}

func (manager *jobManager) removePendingLocked(entry *jobEntry) {
	if entry == nil || entry.state != jobEntryPending {
		return
	}
	if entry.stopPendingContext != nil {
		entry.stopPendingContext()
		entry.stopPendingContext = nil
	}
	for index, candidate := range manager.pending {
		if candidate == entry {
			copy(manager.pending[index:], manager.pending[index+1:])
			manager.pending[len(manager.pending)-1] = nil
			manager.pending = manager.pending[:len(manager.pending)-1]
			break
		}
	}
	if manager.pendingByKey[entry.identity] == entry {
		delete(manager.pendingByKey, entry.identity)
	}
}

func (manager *jobManager) cancelPendingLocked(entry *jobEntry, err error) func() {
	if entry == nil || entry.state != jobEntryPending {
		return nil
	}
	manager.removePendingLocked(entry)
	entry.state = jobEntryCompleted
	delete(manager.entriesByID, entry.id)
	return entry.completion.complete(err)
}

func (manager *jobManager) worker(workerContext context.Context) {
	defer manager.workerExited()
	stopWorkerCancel := context.AfterFunc(workerContext, func() {
		manager.Close()
	})
	defer stopWorkerCancel()

	for {
		manager.mu.Lock()
		var entry *jobEntry
		for entry == nil && !manager.closing {
			entry = manager.takeEligibleLocked()
			if entry == nil {
				manager.cond.Wait()
			}
		}
		if entry == nil {
			manager.mu.Unlock()
			return
		}
		manager.mu.Unlock()

		err := manager.runEntry(entry)
		manager.finishEntry(entry, err)
	}
}

func (manager *jobManager) takeEligibleLocked() *jobEntry {
	bestIndex := -1
	var bestPriority jobPriority
	var bestSequence uint64
	for index, entry := range manager.pending {
		if entry == nil || entry.state != jobEntryPending {
			continue
		}
		if manager.runningByKey[entry.identity] != nil {
			continue
		}
		limit := manager.config.Workers
		if configured := manager.config.CategoryLimits[entry.identity.category]; configured > 0 {
			limit = configured
		}
		if manager.runningGroups[entry.identity.category] >= limit {
			continue
		}
		effectivePriority := manager.effectivePriorityLocked(entry)
		if bestIndex == -1 || effectivePriority > bestPriority ||
			(effectivePriority == bestPriority && entry.sequence < bestSequence) {
			bestIndex = index
			bestPriority = effectivePriority
			bestSequence = entry.sequence
		}
	}
	if bestIndex == -1 {
		return nil
	}

	entry := manager.pending[bestIndex]
	manager.removePendingLocked(entry)
	entry.state = jobEntryRunning
	manager.runningByKey[entry.identity] = entry
	manager.runningGroups[entry.identity.category]++
	manager.totalRunning++
	manager.dispatches++
	if manager.totalRunning > manager.peakRunning {
		manager.peakRunning = manager.totalRunning
	}
	return entry
}

func (manager *jobManager) effectivePriorityLocked(entry *jobEntry) jobPriority {
	priority := uint64(entry.spec.Priority)
	if manager.dispatches > entry.enqueuedAtDispatch {
		priority += (manager.dispatches - entry.enqueuedAtDispatch) / manager.config.AgingInterval
	}
	if priority > uint64(jobPriorityCritical) {
		priority = uint64(jobPriorityCritical)
	}
	return jobPriority(priority)
}

func (manager *jobManager) runEntry(entry *jobEntry) (err error) {
	manager.mu.Lock()
	jobContext, cancel := context.WithCancel(manager.ctx)
	jobContext = context.WithValue(jobContext, jobManagerContextKey{}, manager)
	stopJobContext := context.AfterFunc(entry.spec.Context, cancel)
	entry.runCancel = cancel
	entry.stopRunContext = stopJobContext
	manager.mu.Unlock()

	defer func() {
		stopJobContext()
		cancel()
		if recovered := recover(); recovered != nil {
			err = &jobPanicError{Value: recovered}
			manager.reportPanic(entry.spec, recovered)
		}
	}()
	if contextErr := jobContext.Err(); contextErr != nil {
		return contextErr
	}
	err = entry.spec.Run(jobContext)
	if err == nil && jobContext.Err() != nil {
		return jobContext.Err()
	}
	return err
}

func (manager *jobManager) reportPanic(spec managedJob, recovered any) {
	if manager.config.OnPanic == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	manager.config.OnPanic(spec, recovered)
}

func (manager *jobManager) finishEntry(entry *jobEntry, err error) {
	manager.mu.Lock()
	if entry.stopRunContext != nil {
		entry.stopRunContext()
		entry.stopRunContext = nil
	}
	if entry.runCancel != nil {
		entry.runCancel()
		entry.runCancel = nil
	}
	if manager.runningByKey[entry.identity] == entry {
		delete(manager.runningByKey, entry.identity)
	}
	delete(manager.entriesByID, entry.id)
	if manager.runningGroups[entry.identity.category] > 0 {
		manager.runningGroups[entry.identity.category]--
	}
	if manager.totalRunning > 0 {
		manager.totalRunning--
	}
	entry.state = jobEntryCompleted
	completionCallback := entry.completion.complete(err)
	manager.cond.Broadcast()
	manager.mu.Unlock()
	runJobCompletionCallbacks(completionCallback)
}

func (manager *jobManager) workerExited() {
	manager.mu.Lock()
	if manager.liveWorkers > 0 {
		manager.liveWorkers--
	}
	if manager.liveWorkers == 0 {
		manager.doneOnce.Do(func() { close(manager.done) })
	}
	manager.mu.Unlock()
}

type jobPanicError struct {
	Value any
}

func (err *jobPanicError) Error() string {
	return fmt.Sprintf("job panicked: %v", err.Value)
}
