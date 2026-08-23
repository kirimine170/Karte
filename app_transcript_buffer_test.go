package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	boardpkg "karte/internal/board"
)

type manualTranscriptClock struct {
	mu     sync.Mutex
	now    time.Duration
	timers []*manualTranscriptTimer
}

type manualTranscriptTimer struct {
	clock    *manualTranscriptClock
	due      time.Duration
	callback func()
	active   bool
}

func (clock *manualTranscriptClock) AfterFunc(delay time.Duration, callback func()) transcriptTimer {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	timer := &manualTranscriptTimer{
		clock:    clock,
		due:      clock.now + delay,
		callback: callback,
		active:   true,
	}
	clock.timers = append(clock.timers, timer)
	return timer
}

func (timer *manualTranscriptTimer) Stop() bool {
	timer.clock.mu.Lock()
	defer timer.clock.mu.Unlock()
	if !timer.active {
		return false
	}
	timer.active = false
	return true
}

func (clock *manualTranscriptClock) Advance(elapsed time.Duration) {
	clock.mu.Lock()
	target := clock.now + elapsed
	clock.mu.Unlock()
	for {
		clock.mu.Lock()
		var next *manualTranscriptTimer
		for _, timer := range clock.timers {
			if !timer.active || timer.due > target {
				continue
			}
			if next == nil || timer.due < next.due {
				next = timer
			}
		}
		if next == nil {
			clock.now = target
			clock.mu.Unlock()
			return
		}
		clock.now = next.due
		next.active = false
		callback := next.callback
		clock.mu.Unlock()
		callback()
	}
}

type transcriptWriteStep struct {
	n   int
	err error
}

type fakeTranscriptAppendFile struct {
	mu sync.Mutex

	data       bytes.Buffer
	writeSteps []transcriptWriteStep
	writeSizes []int
	writeStart chan struct{}
	writeGate  <-chan struct{}
	writeOnce  sync.Once
	syncErr    error
	closeErr   error
	syncCount  int
	closeCount int
}

func (file *fakeTranscriptAppendFile) Write(data []byte) (int, error) {
	if file.writeStart != nil {
		file.writeOnce.Do(func() { close(file.writeStart) })
	}
	if file.writeGate != nil {
		<-file.writeGate
	}
	file.mu.Lock()
	defer file.mu.Unlock()
	requested := len(data)
	file.writeSizes = append(file.writeSizes, requested)
	written := requested
	var writeErr error
	if len(file.writeSteps) > 0 {
		step := file.writeSteps[0]
		file.writeSteps = file.writeSteps[1:]
		written = step.n
		writeErr = step.err
	}
	if written >= 0 && written <= requested {
		_, _ = file.data.Write(data[:written])
	}
	return written, writeErr
}

func (file *fakeTranscriptAppendFile) Sync() error {
	file.mu.Lock()
	defer file.mu.Unlock()
	file.syncCount++
	return file.syncErr
}

func (file *fakeTranscriptAppendFile) Close() error {
	file.mu.Lock()
	defer file.mu.Unlock()
	file.closeCount++
	return file.closeErr
}

type fakeTranscriptFileSnapshot struct {
	data       string
	writeSizes []int
	syncCount  int
	closeCount int
}

func (file *fakeTranscriptAppendFile) snapshot() fakeTranscriptFileSnapshot {
	file.mu.Lock()
	defer file.mu.Unlock()
	return fakeTranscriptFileSnapshot{
		data:       file.data.String(),
		writeSizes: append([]int(nil), file.writeSizes...),
		syncCount:  file.syncCount,
		closeCount: file.closeCount,
	}
}

func newTranscriptBufferTestHarness(
	t *testing.T,
	clock *manualTranscriptClock,
	file *fakeTranscriptAppendFile,
	configure func(*transcriptBufferHooks),
	partialEmit func(transcriptPartialPayload),
	onError func(error),
) (*App, *transcriptBuffer, string, string) {
	t.Helper()
	root := t.TempDir()
	relPath := filepath.ToSlash(filepath.Join("content", "transcripts", "buffer.md"))
	absPath := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absPath, []byte("## Transcript\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := &App{root: root, dataDir: root}
	hooks := defaultTranscriptBufferHooks()
	hooks.clock = clock
	hooks.open = func(string) (transcriptAppendFile, error) { return file, nil }
	if configure != nil {
		configure(&hooks)
	}
	app.transcripts.hooks = &hooks
	buffer, err := app.newTranscriptBuffer(relPath, partialEmit, onError)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = buffer.Abort() })
	return app, buffer, relPath, absPath
}

func TestTranscriptPartialUsesFixedCadenceAndNeverTouchesFile(t *testing.T) {
	clock := &manualTranscriptClock{}
	file := &fakeTranscriptAppendFile{}
	var emitted []string
	_, buffer, _, _ := newTranscriptBufferTestHarness(t, clock, file, nil, func(payload transcriptPartialPayload) {
		emitted = append(emitted, payload.Text)
	}, nil)

	if err := buffer.UpdatePartial("first", 0); err != nil {
		t.Fatal(err)
	}
	clock.Advance(250 * time.Millisecond)
	if err := buffer.UpdatePartial("latest", 0.25); err != nil {
		t.Fatal(err)
	}
	clock.Advance(50 * time.Millisecond)
	if !reflect.DeepEqual(emitted, []string{"latest"}) {
		t.Fatalf("first cadence emissions = %v", emitted)
	}

	for index, text := range []string{"continuous-a", "continuous-b", "continuous-latest"} {
		if err := buffer.UpdatePartial(text, float64(index)); err != nil {
			t.Fatal(err)
		}
		clock.Advance(100 * time.Millisecond)
	}
	if !reflect.DeepEqual(emitted, []string{"latest", "continuous-latest"}) {
		t.Fatalf("continuous cadence emissions = %v", emitted)
	}
	if snapshot := file.snapshot(); snapshot.data != "" || snapshot.syncCount != 0 {
		t.Fatalf("partial changed append file: %+v", snapshot)
	}
}

func TestTranscriptEventQueueAllowsReentrantCancelAndFinal(t *testing.T) {
	clock := &manualTranscriptClock{}
	file := &fakeTranscriptAppendFile{}
	var buffer *transcriptBuffer
	var callbackErr error
	var events []string
	_, opened, _, _ := newTranscriptBufferTestHarness(t, clock, file, nil, func(payload transcriptPartialPayload) {
		events = append(events, "partial:"+payload.Text)
		buffer.CancelPartial()
		callbackErr = buffer.AppendFinalAndEmit("callback\r\nfinal", func() {
			events = append(events, "final")
		})
	}, nil)
	buffer = opened

	if err := buffer.UpdatePartial("reentrant", 0); err != nil {
		t.Fatal(err)
	}
	clock.Advance(transcriptPartialInterval)
	if callbackErr != nil {
		t.Fatal(callbackErr)
	}
	if !reflect.DeepEqual(events, []string{"partial:reentrant", "final"}) {
		t.Fatalf("event order = %v", events)
	}
	if err := buffer.Close(); err != nil {
		t.Fatal(err)
	}
	if got := file.snapshot().data; got != "callback final  \n" {
		t.Fatalf("normalized reentrant final = %q", got)
	}
}

func TestTranscriptFinalCancelsPartialAndFlushesWithoutEarlySync(t *testing.T) {
	clock := &manualTranscriptClock{}
	file := &fakeTranscriptAppendFile{}
	var events []string
	_, buffer, _, _ := newTranscriptBufferTestHarness(t, clock, file, nil, func(payload transcriptPartialPayload) {
		events = append(events, "partial:"+payload.Text)
	}, nil)
	if err := buffer.UpdatePartial("obsolete", 0); err != nil {
		t.Fatal(err)
	}
	if err := buffer.AppendFinalAndEmit("final", func() { events = append(events, "final") }); err != nil {
		t.Fatal(err)
	}
	clock.Advance(transcriptPartialInterval)
	if !reflect.DeepEqual(events, []string{"final"}) {
		t.Fatalf("events after final = %v", events)
	}
	clock.Advance(transcriptBatchDelay - transcriptPartialInterval - time.Nanosecond)
	if snapshot := file.snapshot(); len(snapshot.writeSizes) != 0 || snapshot.syncCount != 0 {
		t.Fatalf("batch flushed early: %+v", snapshot)
	}
	clock.Advance(time.Nanosecond)
	if snapshot := file.snapshot(); snapshot.data != "final  \n" || snapshot.syncCount != 0 {
		t.Fatalf("one-second batch state = %+v", snapshot)
	}
	clock.Advance(transcriptCheckpointDelay - transcriptBatchDelay)
	if snapshot := file.snapshot(); snapshot.syncCount != 1 {
		t.Fatalf("checkpoint sync count = %d", snapshot.syncCount)
	}
}

func TestTranscriptTenThousandFinalsStayOrderedAndBatched(t *testing.T) {
	clock := &manualTranscriptClock{}
	file := &fakeTranscriptAppendFile{}
	_, buffer, _, _ := newTranscriptBufferTestHarness(t, clock, file, nil, nil, nil)
	var expected strings.Builder
	for index := range 10_000 {
		line := fmt.Sprintf("%05d segment", index)
		expected.WriteString(formatTranscriptAppendLine(line))
		if err := buffer.AppendFinal(line); err != nil {
			t.Fatalf("append %d: %v", index, err)
		}
	}
	if err := buffer.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot := file.snapshot()
	if snapshot.data != expected.String() {
		t.Fatalf("10k output length/order mismatch: got=%d want=%d", len(snapshot.data), expected.Len())
	}
	if len(snapshot.writeSizes) < 2 || len(snapshot.writeSizes) > 5 {
		t.Fatalf("10k write batches = %v", snapshot.writeSizes)
	}
	if snapshot.syncCount != 1 || snapshot.closeCount != 1 {
		t.Fatalf("10k close state = %+v", snapshot)
	}
}

func TestTranscriptWriteFaultsAreStickyAndFailClosed(t *testing.T) {
	tests := []struct {
		name string
		step transcriptWriteStep
	}{
		{name: "n-positive-with-error", step: transcriptWriteStep{n: 3, err: errors.New("injected write fault")}},
		{name: "zero-without-error", step: transcriptWriteStep{n: 0}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clock := &manualTranscriptClock{}
			file := &fakeTranscriptAppendFile{writeSteps: []transcriptWriteStep{test.step}}
			var reported []error
			_, buffer, _, _ := newTranscriptBufferTestHarness(t, clock, file, func(hooks *transcriptBufferHooks) {
				hooks.batchBytes = 1
			}, nil, func(err error) {
				reported = append(reported, err)
			})
			firstErr := buffer.AppendFinal("first")
			if firstErr == nil {
				t.Fatal("first append unexpectedly succeeded")
			}
			writeCount := len(file.snapshot().writeSizes)
			if err := buffer.AppendFinal("second"); !errors.Is(err, errTranscriptBufferFailed) {
				t.Fatalf("second append error = %v", err)
			}
			if got := len(file.snapshot().writeSizes); got != writeCount {
				t.Fatalf("failed buffer wrote again before close: %d -> %d", writeCount, got)
			}
			closeErr := buffer.Close()
			if closeErr == nil {
				t.Fatal("sticky fault was lost on close")
			}
			if test.step.err != nil && !errors.Is(closeErr, test.step.err) {
				t.Fatalf("close error = %v", closeErr)
			}
			if test.step.err == nil && !errors.Is(closeErr, io.ErrShortWrite) {
				t.Fatalf("short-write close error = %v", closeErr)
			}
			if len(reported) != 1 {
				t.Fatalf("fault reports = %d，want 1", len(reported))
			}
			if got := file.snapshot().data; got != "first  \n" {
				t.Fatalf("close retry duplicated or lost bytes: %q", got)
			}
		})
	}
}

func TestTranscriptSyncCloseAndPendingLimitFaultsAreAggregated(t *testing.T) {
	t.Run("sync-and-close", func(t *testing.T) {
		clock := &manualTranscriptClock{}
		syncErr := errors.New("injected sync fault")
		closeErr := errors.New("injected close fault")
		file := &fakeTranscriptAppendFile{syncErr: syncErr, closeErr: closeErr}
		_, buffer, _, _ := newTranscriptBufferTestHarness(t, clock, file, nil, nil, nil)
		if err := buffer.AppendFinal("final"); err != nil {
			t.Fatal(err)
		}
		err := buffer.Close()
		if !errors.Is(err, syncErr) || !errors.Is(err, closeErr) {
			t.Fatalf("aggregated close error = %v", err)
		}
	})

	t.Run("pending-limit", func(t *testing.T) {
		clock := &manualTranscriptClock{}
		file := &fakeTranscriptAppendFile{}
		_, buffer, _, _ := newTranscriptBufferTestHarness(t, clock, file, func(hooks *transcriptBufferHooks) {
			hooks.batchBytes = 4
			hooks.maxPendingBytes = 8
		}, nil, nil)
		if err := buffer.AppendFinal("larger-than-limit"); !errors.Is(err, errTranscriptPendingLimit) {
			t.Fatalf("pending limit error = %v", err)
		}
		if err := buffer.AppendFinal("x"); !errors.Is(err, errTranscriptBufferFailed) {
			t.Fatalf("post-limit append error = %v", err)
		}
		if got := file.snapshot().data; got != "" {
			t.Fatalf("limit fault wrote bytes: %q", got)
		}
	})
}

func TestActiveTranscriptRejectsSaveAndRenameWithoutTOCTOUWindow(t *testing.T) {
	clock := &manualTranscriptClock{}
	file := &fakeTranscriptAppendFile{}
	app, buffer, relPath, absPath := newTranscriptBufferTestHarness(t, clock, file, nil, nil, nil)
	original, err := os.ReadFile(absPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.SaveFile(relPath, "replacement"); !errors.Is(err, errTranscriptPathActive) {
		t.Fatalf("active SaveFile error = %v", err)
	}
	if err := app.RenameFile(relPath, "content/transcripts/renamed.md"); !errors.Is(err, errTranscriptPathActive) {
		t.Fatalf("active RenameFile error = %v", err)
	}
	if err := app.ResolveConflict(relPath, "local"); !errors.Is(err, errTranscriptPathActive) {
		t.Fatalf("active ResolveConflict error = %v", err)
	}
	if _, err := app.SaveBoard(relPath, boardpkg.Document{}); !errors.Is(err, errTranscriptPathActive) {
		t.Fatalf("active SaveBoard error = %v", err)
	}
	after, err := os.ReadFile(absPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, original) {
		t.Fatalf("active transcript was replaced: %q", after)
	}
	if _, err := os.Stat(filepath.Join(app.dataDir, ".mdsys", "doc_map.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("active mutation changed doc_map state: %v", err)
	}
	if err := buffer.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTranscriptCreationTransfersReservationIntoActiveRegistration(t *testing.T) {
	root := t.TempDir()
	app := &App{root: root, dataDir: root}
	app.jobs.closing = true
	if err := os.MkdirAll(filepath.Join(root, "content", "transcripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	openStarted := make(chan struct{})
	allowOpen := make(chan struct{})
	appendFile := &fakeTranscriptAppendFile{}
	hooks := defaultTranscriptBufferHooks()
	hooks.open = func(string) (transcriptAppendFile, error) {
		close(openStarted)
		<-allowOpen
		return appendFile, nil
	}
	app.transcripts.hooks = &hooks
	relPath := "content/transcripts/transfer.md"
	body := app.composeTranscriptMarkdown("data/audio/transfer.wav", "")
	type createResult struct {
		buffer *transcriptBuffer
		err    error
	}
	created := make(chan createResult, 1)
	go func() {
		buffer, err := app.createTranscriptDocumentAndBuffer(relPath, body, nil, nil)
		created <- createResult{buffer: buffer, err: err}
	}()
	<-openStarted

	saveResult := make(chan error, 1)
	renameResult := make(chan error, 1)
	go func() { saveResult <- app.SaveFile(relPath, "replacement") }()
	go func() { renameResult <- app.RenameFile(relPath, "content/transcripts/renamed-transfer.md") }()
	close(allowOpen)
	result := <-created
	if result.err != nil {
		t.Fatal(result.err)
	}
	if err := <-saveResult; !errors.Is(err, errTranscriptPathActive) {
		t.Fatalf("SaveFile during transfer = %v", err)
	}
	if err := <-renameResult; !errors.Is(err, errTranscriptPathActive) {
		t.Fatalf("RenameFile during transfer = %v", err)
	}
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relPath)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "transfer.wav") || strings.Contains(string(content), "replacement") {
		t.Fatalf("creation transfer content = %s", content)
	}
	if err := result.buffer.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTranscriptCreationReservationRejectsPostReplaceAlias(t *testing.T) {
	root := t.TempDir()
	app := &App{root: root, dataDir: root}
	app.jobs.closing = true
	if err := os.MkdirAll(filepath.Join(root, "content", "transcripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	saved := make(chan struct{})
	allowOpen := make(chan struct{})
	app.transcripts.afterInstall = func(string) {
		close(saved)
		<-allowOpen
	}
	relPath := "content/transcripts/replace-gap.md"
	body := app.composeTranscriptMarkdown("data/audio/replace-gap.wav", "")
	type createResult struct {
		buffer *transcriptBuffer
		err    error
	}
	created := make(chan createResult, 1)
	go func() {
		buffer, err := app.createTranscriptDocumentAndBuffer(relPath, body, nil, nil)
		created <- createResult{buffer: buffer, err: err}
	}()
	<-saved

	absPath := filepath.Join(root, filepath.FromSlash(relPath))
	aliasRel := "content/transcripts/replace-gap-alias.md"
	aliasPath := filepath.Join(root, filepath.FromSlash(aliasRel))
	if err := os.Link(absPath, aliasPath); err != nil {
		close(allowOpen)
		result := <-created
		if result.buffer != nil {
			_ = result.buffer.Abort()
		}
		t.Skipf("hard-link aliases unavailable: %v", err)
	}
	if err := app.SaveFile(aliasRel, "replacement"); !errors.Is(err, errTranscriptPathActive) {
		close(allowOpen)
		result := <-created
		if result.buffer != nil {
			_ = result.buffer.Abort()
		}
		t.Fatalf("SaveFile through post-replace alias = %v", err)
	}
	close(allowOpen)
	result := <-created
	if result.err != nil {
		t.Fatal(result.err)
	}
	content, err := os.ReadFile(absPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "replace-gap.wav") || strings.Contains(string(content), "replacement") {
		t.Fatalf("post-replace alias changed transcript: %q", content)
	}
	if err := result.buffer.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTranscriptCreateMappingFailureRemovesOnlyInstalledIdentity(t *testing.T) {
	t.Run("installed file is removed", func(t *testing.T) {
		root := t.TempDir()
		app := &App{root: root, dataDir: root}
		wantErr := errors.New("document map replace failed")
		app.documentMapStore.operations.replace = func(string, string) error { return wantErr }
		relPath := "content/transcripts/map-failure.md"
		body := app.composeTranscriptMarkdown("data/audio/map-failure.wav", "")
		if _, err := app.createTranscriptDocumentAndBuffer(relPath, body, nil, nil); !errors.Is(err, wantErr) {
			t.Fatalf("create error = %v", err)
		}
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(relPath))); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("installed transcript survived mapping rollback: %v", err)
		}
	})

	t.Run("external replacement before mapping is preserved", func(t *testing.T) {
		root := t.TempDir()
		app := &App{root: root, dataDir: root}
		relPath := "content/transcripts/map-replaced.md"
		absPath := filepath.Join(root, filepath.FromSlash(relPath))
		movedPath := absPath + ".installed"
		externalBytes := []byte("external replacement\n")
		app.transcripts.afterInstall = func(string) {
			if err := os.Rename(absPath, movedPath); err != nil {
				t.Errorf("move installed transcript: %v", err)
				return
			}
			if err := os.WriteFile(absPath, externalBytes, 0o600); err != nil {
				t.Errorf("write external replacement: %v", err)
			}
		}
		body := app.composeTranscriptMarkdown("data/audio/map-replaced.wav", "")
		if _, err := app.createTranscriptDocumentAndBuffer(relPath, body, nil, nil); err == nil || !strings.Contains(err.Error(), "identity changed") {
			t.Fatalf("external replacement create error = %v", err)
		}
		content, err := os.ReadFile(absPath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(content, externalBytes) {
			t.Fatalf("mapping rollback removed external replacement: %q", content)
		}
		if _, err := os.Lstat(filepath.Join(root, ".mdsys", "doc_map.json")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("mapping was written after pre-map identity replacement: %v", err)
		}
	})

	t.Run("post-mapping replacement restores previous map bytes", func(t *testing.T) {
		root := t.TempDir()
		app := &App{root: root, dataDir: root}
		if _, err := app.updateDocumentMapping("existing-doc", "content/existing.md"); err != nil {
			t.Fatal(err)
		}
		documentMapPath := filepath.Join(root, ".mdsys", "doc_map.json")
		previousMapBytes, err := os.ReadFile(documentMapPath)
		if err != nil {
			t.Fatal(err)
		}
		relPath := "content/transcripts/map-post-replaced.md"
		absPath := filepath.Join(root, filepath.FromSlash(relPath))
		movedPath := absPath + ".installed"
		externalBytes := []byte("external replacement after mapping\n")
		app.documentMapStore.operations.replace = func(sourcePath, destinationPath string) error {
			if err := atomicReplaceFile(sourcePath, destinationPath); err != nil {
				return err
			}
			if err := os.Rename(absPath, movedPath); err != nil {
				return err
			}
			return os.WriteFile(absPath, externalBytes, 0o600)
		}
		body := app.composeTranscriptMarkdown("data/audio/map-post-replaced.wav", "")
		if _, err := app.createTranscriptDocumentAndBuffer(relPath, body, nil, nil); err == nil || !strings.Contains(err.Error(), "identity changed") {
			t.Fatalf("post-mapping replacement create error = %v", err)
		}
		content, err := os.ReadFile(absPath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(content, externalBytes) {
			t.Fatalf("rollback removed post-mapping external replacement: %q", content)
		}
		afterMapBytes, err := os.ReadFile(documentMapPath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(afterMapBytes, previousMapBytes) {
			t.Fatalf("document map bytes changed after rollback:\n%s", afterMapBytes)
		}
		mappings, err := decodeDocumentMap(afterMapBytes)
		if err != nil {
			t.Fatal(err)
		}
		if len(mappings) != 1 || mappings["existing-doc"] != "content/existing.md" {
			t.Fatalf("document map rollback = %#v", mappings)
		}
	})
}

func TestTranscriptDerivedEffectsStartOnlyAtCompletionBoundary(t *testing.T) {
	root := t.TempDir()
	app := &App{root: root, dataDir: root}
	app.jobs.closing = true
	derivedCount := 0
	app.transcripts.publish = func(*App, string) error {
		derivedCount++
		return nil
	}
	relPath := "content/transcripts/completion-boundary.md"
	body := app.composeTranscriptMarkdown("data/audio/completion-boundary.wav", "")
	buffer, err := app.createTranscriptDocumentAndBuffer(relPath, body, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if derivedCount != 0 {
		t.Fatalf("initial transcript install published %d derived effects", derivedCount)
	}
	if err := buffer.AppendFinal("confirmed"); err != nil {
		t.Fatal(err)
	}
	if err := app.completeTranscriptBuffer(buffer); err != nil {
		t.Fatal(err)
	}
	if derivedCount != 1 {
		t.Fatalf("completion derived effect count = %d", derivedCount)
	}
}

func TestImportTranscriptCreateCollisionPreservesExternalFileAndUsesSuffix(t *testing.T) {
	root := t.TempDir()
	app := &App{root: root, dataDir: root}
	app.jobs.closing = true
	app.transcripts.publish = func(*App, string) error { return nil }
	baseRel := "content/transcripts/collision.md"
	externalBytes := []byte("external file created at claim boundary\n")
	app.transcripts.beforeInstall = func(relPath string) {
		if relPath != baseRel {
			return
		}
		absPath, ok := app.resolveContentPath(relPath)
		if !ok {
			t.Errorf("invalid collision path: %s", relPath)
			return
		}
		if err := os.WriteFile(absPath, externalBytes, 0o600); err != nil {
			t.Errorf("create external collision: %v", err)
		}
	}
	transcriptPath, err := app.startStreamingTranscription(
		context.Background(),
		&progressThenFailASRService{},
		filepath.Join(root, "collision.wav"),
		"data/audio/collision.wav",
	)
	if err != nil {
		t.Fatal(err)
	}
	if transcriptPath != "content/transcripts/collision-2.md" {
		t.Fatalf("collision transcript path = %s", transcriptPath)
	}
	baseBytes, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(baseRel)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(baseBytes, externalBytes) {
		t.Fatalf("external collision was overwritten: %q", baseBytes)
	}
}

func TestTranscriptConfinedOpenRejectsEscapingAncestorSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "content"), 0o755); err != nil {
		t.Fatal(err)
	}
	outsidePath := filepath.Join(outside, "escape.md")
	if err := os.WriteFile(outsidePath, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "content", "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	app := &App{root: root, dataDir: root}
	if _, err := app.newTranscriptBuffer("content/linked/escape.md", nil, nil); err == nil {
		t.Fatal("confined append accepted an escaping ancestor symlink")
	}
	content, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "outside" {
		t.Fatalf("outside file changed: %q", content)
	}
}

func TestTranscriptConfinedCreateRejectsEscapingAncestorSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "content"), 0o755); err != nil {
		t.Fatal(err)
	}
	linkedDirectory := filepath.Join(root, "content", "transcripts")
	if err := os.Symlink(outside, linkedDirectory); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	app := &App{root: root, dataDir: root}
	relPath := "content/transcripts/escape-create.md"
	body := app.composeTranscriptMarkdown("data/audio/escape-create.wav", "")
	if _, err := app.createTranscriptDocumentAndBuffer(relPath, body, nil, nil); err == nil {
		t.Fatal("confined transcript creation accepted an escaping ancestor symlink")
	}
	if _, err := os.Lstat(filepath.Join(outside, "escape-create.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("transcript creation escaped the data root: %v", err)
	}
}

func TestActiveTranscriptRejectsSymlinkAndCaseAliases(t *testing.T) {
	root := t.TempDir()
	relPath := "content/transcripts/AliasCase.md"
	absPath := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absPath, []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := &App{root: root, dataDir: root}
	buffer, err := app.newTranscriptBuffer(relPath, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer buffer.Abort()

	aliasDirectory := filepath.Join(root, "content", "transcript-alias")
	if err := os.Symlink(filepath.Join(root, "content", "transcripts"), aliasDirectory); err == nil {
		aliasPath := "content/transcript-alias/AliasCase.md"
		if err := app.SaveFile(aliasPath, "replacement through symlink"); !errors.Is(err, errTranscriptPathActive) {
			t.Fatalf("symlink alias SaveFile error = %v", err)
		}
		if _, err := app.newTranscriptBuffer(aliasPath, nil, nil); err == nil {
			t.Fatal("second buffer accepted a symlink alias of the active inode")
		}
	}

	caseAlias := "content/transcripts/aliascase.md"
	caseAbs := filepath.Join(root, filepath.FromSlash(caseAlias))
	if caseInfo, statErr := os.Stat(caseAbs); statErr == nil {
		originalInfo, originalErr := os.Stat(absPath)
		if originalErr != nil {
			t.Fatal(originalErr)
		}
		if os.SameFile(caseInfo, originalInfo) {
			if err := app.SaveFile(caseAlias, "replacement through case alias"); !errors.Is(err, errTranscriptPathActive) {
				t.Fatalf("case alias SaveFile error = %v", err)
			}
		}
	}
	content, err := os.ReadFile(absPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "original\n" {
		t.Fatalf("alias mutation changed active transcript: %q", content)
	}
}

func TestTranscriptDetectsIdentityReplacementBeforeFlush(t *testing.T) {
	root := t.TempDir()
	relPath := "content/transcripts/identity.md"
	absPath := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absPath, []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := &App{root: root, dataDir: root}
	buffer, err := app.newTranscriptBuffer(relPath, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := buffer.AppendFinal("must-not-escape"); err != nil {
		t.Fatal(err)
	}
	movedPath := absPath + ".moved"
	if err := os.Rename(absPath, movedPath); err != nil {
		_ = buffer.Abort()
		t.Skipf("open-file rename unavailable: %v", err)
	}
	if err := os.WriteFile(absPath, []byte("replacement\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := buffer.Close(); err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("identity replacement close error = %v", err)
	}
	for _, path := range []string{movedPath, absPath} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(content), "must-not-escape") {
			t.Fatalf("pending append reached replaced identity %s: %q", path, content)
		}
	}
}

func TestTranscriptDerivedAttemptsAreExactOnceAndFailureStaysDirty(t *testing.T) {
	clock := &manualTranscriptClock{}
	file := &fakeTranscriptAppendFile{}
	wantErr := errors.New("derived publication failed")
	app, buffer, relPath, _ := newTranscriptBufferTestHarness(t, clock, file, nil, nil, nil)
	publishCount := 0
	app.transcripts.publish = func(*App, string) error {
		publishCount++
		return wantErr
	}
	if err := buffer.AppendFinal("confirmed"); err != nil {
		t.Fatal(err)
	}
	if err := app.completeTranscriptBuffer(buffer); !errors.Is(err, wantErr) {
		t.Fatalf("first completion error = %v", err)
	}
	if err := app.completeTranscriptBuffer(buffer); !errors.Is(err, wantErr) {
		t.Fatalf("second completion error = %v", err)
	}
	if publishCount != 1 {
		t.Fatalf("derived publish attempts = %d", publishCount)
	}
	app.transcripts.mu.Lock()
	dirtyErr := app.transcripts.dirty[relPath]
	app.transcripts.mu.Unlock()
	if !errors.Is(dirtyErr, wantErr) {
		t.Fatalf("dirty marker = %v", dirtyErr)
	}
}

func TestCompleteTranscriptBufferDrainsAcceptedFinalBeforeCloseError(t *testing.T) {
	clock := &manualTranscriptClock{}
	wantErr := errors.New("sync failed")
	file := &fakeTranscriptAppendFile{syncErr: wantErr}
	app, buffer, _, _ := newTranscriptBufferTestHarness(t, clock, file, nil, nil, nil)
	delivered := make(chan struct{})

	// Hold the dispatcher in a deterministic queued state，so Close can finish
	// before the accepted final callback is delivered．
	buffer.eventMu.Lock()
	buffer.eventDispatch = true
	buffer.eventMu.Unlock()
	if err := buffer.AppendFinalAndEmit("accepted final", func() { close(delivered) }); err != nil {
		t.Fatal(err)
	}
	completed := make(chan error, 1)
	go func() { completed <- app.completeTranscriptBuffer(buffer) }()

	for {
		buffer.eventMu.Lock()
		queued := len(buffer.eventQueue)
		buffer.eventMu.Unlock()
		if queued >= 2 {
			break
		}
		select {
		case err := <-completed:
			t.Fatalf("completion returned before queuing its event drain: %v", err)
		default:
			runtime.Gosched()
		}
	}
	buffer.eventMu.Lock()
	buffer.eventDispatch = false
	buffer.eventMu.Unlock()
	buffer.enqueueEvent(transcriptBufferEvent{callback: func() {}})
	if err := <-completed; !errors.Is(err, wantErr) {
		t.Fatalf("completion error = %v", err)
	}
	select {
	case <-delivered:
	default:
		t.Fatal("accepted final callback was not delivered before terminal error")
	}
}

type progressThenFailASRService struct {
	err error
}

func (service *progressThenFailASRService) Close() {}

func (service *progressThenFailASRService) TranscribeFile(
	_ context.Context,
	_ string,
	progress func(string, int, int, float64),
) (string, error) {
	progress("confirmed before failure", 1, 2, 1.25)
	return "", service.err
}

func (service *progressThenFailASRService) ProcessSamples([]float32) (string, error) {
	return "", nil
}

type timerFaultRaceASRService struct {
	clock        *manualTranscriptClock
	writeStarted <-chan struct{}
	releaseWrite chan struct{}
	advanceDone  chan struct{}
	err          error
}

func (service *timerFaultRaceASRService) Close() {}

func (service *timerFaultRaceASRService) TranscribeFile(
	_ context.Context,
	_ string,
	progress func(string, int, int, float64),
) (string, error) {
	progress("confirmed before timer fault", 1, 1, 0)
	go func() {
		service.clock.Advance(transcriptBatchDelay)
		close(service.advanceDone)
	}()
	<-service.writeStarted
	close(service.releaseWrite)
	return "", service.err
}

func (service *timerFaultRaceASRService) ProcessSamples([]float32) (string, error) {
	return "", nil
}

func TestImportFailureClosesBufferAndPreservesConfirmedLines(t *testing.T) {
	root := t.TempDir()
	app := &App{root: root, dataDir: root, logFilePath: filepath.Join(root, "app.log")}
	app.jobs.closing = true
	published := 0
	app.transcripts.publish = func(*App, string) error {
		published++
		return nil
	}
	wantErr := errors.New("decoder failed")
	transcriptPath, err := app.startStreamingTranscription(
		context.Background(),
		&progressThenFailASRService{err: wantErr},
		filepath.Join(root, "audio.wav"),
		"data/audio/audio.wav",
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("import error = %v", err)
	}
	content, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(transcriptPath)))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(content), "confirmed before failure") {
		t.Fatalf("confirmed import line was lost: %s", content)
	}
	if published != 1 {
		t.Fatalf("failed import completion publications = %d", published)
	}
}

func TestImportReturnRacesTimerFaultWithoutLosingStickyError(t *testing.T) {
	root := t.TempDir()
	app := &App{root: root, dataDir: root, logFilePath: filepath.Join(root, "app.log")}
	app.jobs.closing = true
	clock := &manualTranscriptClock{}
	writeStarted := make(chan struct{})
	releaseWrite := make(chan struct{})
	writeErr := errors.New("timer write failed")
	file := &fakeTranscriptAppendFile{
		writeSteps: []transcriptWriteStep{{n: 0, err: writeErr}},
		writeStart: writeStarted,
		writeGate:  releaseWrite,
	}
	hooks := defaultTranscriptBufferHooks()
	hooks.clock = clock
	hooks.open = func(string) (transcriptAppendFile, error) { return file, nil }
	app.transcripts.hooks = &hooks
	serviceErr := errors.New("decoder returned concurrently")
	advanceDone := make(chan struct{})
	service := &timerFaultRaceASRService{
		clock:        clock,
		writeStarted: writeStarted,
		releaseWrite: releaseWrite,
		advanceDone:  advanceDone,
		err:          serviceErr,
	}
	_, err := app.startStreamingTranscription(
		context.Background(),
		service,
		filepath.Join(root, "audio.wav"),
		"data/audio/race.wav",
	)
	<-advanceDone
	if !errors.Is(err, serviceErr) || !errors.Is(err, writeErr) {
		t.Fatalf("raced import error = %v", err)
	}
}
