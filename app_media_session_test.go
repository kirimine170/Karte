package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMediaChunkSessionOffsetsLimitsFinishAndAbort(t *testing.T) {
	app, config, _, recorder := newMediaImportTestApp(t)
	config.PDFMaxBytes = 1024
	payload := mediaImportPDFPayload(700)
	session, err := app.BeginMediaImport(mediaImportKindPDF, "chunked.pdf", int64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	if session.ChunkSize != mediaImportChunkBytes || session.MaxBytes != config.PDFMaxBytes {
		t.Fatalf("session limits = %+v", session)
	}

	if offset, err := app.AppendMediaImportChunk(session.ID, 1, base64.StdEncoding.EncodeToString(payload[:200])); err == nil || offset != 0 {
		t.Fatalf("wrong-offset append = (%d，%v)", offset, err)
	}
	if offset, err := app.AppendMediaImportChunk(session.ID, 0, "AAAA ===="); err == nil || offset != 0 {
		t.Fatalf("malformed append = (%d，%v)", offset, err)
	}

	offset := int64(0)
	for start := 0; start < len(payload); start += 200 {
		end := start + 200
		if end > len(payload) {
			end = len(payload)
		}
		encoded := base64.StdEncoding.EncodeToString(payload[start:end])
		offset, err = app.AppendMediaImportChunk(session.ID, offset, encoded)
		if err != nil {
			t.Fatal(err)
		}
		if offset != int64(end) {
			t.Fatalf("offset = %d，want %d", offset, end)
		}
	}
	relativePath, err := app.FinishMediaImport(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(filepath.Join(app.dataDir, filepath.FromSlash(relativePath)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, payload) {
		t.Fatal("chunked PDF differs from source")
	}
	if _, err := app.FinishMediaImport(session.ID); !errors.Is(err, errMediaImportClosed) {
		t.Fatalf("second Finish error = %v，want closed", err)
	}
	if recorder.eventCount() != 1 {
		t.Fatalf("events = %d，want 1", recorder.eventCount())
	}

	aborted, err := app.BeginMediaImport(mediaImportKindPDF, "aborted.pdf", 32)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.AppendMediaImportChunk(aborted.ID, 0, base64.StdEncoding.EncodeToString(mediaImportPDFPayload(16))); err != nil {
		t.Fatal(err)
	}
	if err := app.AbortMediaImport(aborted.ID); err != nil {
		t.Fatal(err)
	}
	if err := app.AbortMediaImport(aborted.ID); err != nil {
		t.Fatalf("idempotent Abort: %v", err)
	}
	assertNoMediaImportTemps(t, app.dataDir)

	if _, err := app.BeginMediaImport(mediaImportKindPDF, "too-large.pdf", config.PDFMaxBytes+1); !errors.Is(err, errMediaImportTooLarge) && !strings.Contains(err.Error(), "exceeds limit") {
		t.Fatalf("declared max+1 error = %v", err)
	}
	if _, err := app.BeginMediaImport(mediaImportKindPDF, "empty.pdf", 0); err == nil {
		t.Fatal("zero-size session succeeded")
	}
}

func TestMediaChunkStrictMaximum(t *testing.T) {
	exact := bytes.Repeat([]byte{'x'}, mediaImportChunkBytes)
	decoded, err := decodeStrictMediaChunk(base64.StdEncoding.EncodeToString(exact))
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != mediaImportChunkBytes {
		t.Fatalf("decoded bytes = %d", len(decoded))
	}
	tooLarge := append(exact, 'x')
	if _, err := decodeStrictMediaChunk(base64.StdEncoding.EncodeToString(tooLarge)); err == nil {
		t.Fatal("max+1 chunk succeeded")
	}
	for _, invalid := range []string{"", "AAAA\n", "A===", "AA=A", "AAAA===="} {
		if _, err := decodeStrictMediaChunk(invalid); err == nil {
			t.Fatalf("invalid chunk %q succeeded", invalid)
		}
	}
}

func TestMediaChunkSessionMaximumAndTTL(t *testing.T) {
	app, config, hooks, _ := newMediaImportTestApp(t)
	config.MaxSessions = 4
	config.SessionTTL = 5 * time.Minute
	now := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	hooks.now = func() time.Time { return now }

	sessions := make([]MediaImportSession, 0, config.MaxSessions)
	for index := 0; index < config.MaxSessions; index++ {
		session, err := app.BeginMediaImport(mediaImportKindPDF, "active.pdf", 32)
		if err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, session)
	}
	if _, err := app.BeginMediaImport(mediaImportKindPDF, "fifth.pdf", 32); err == nil {
		t.Fatal("fifth active session succeeded")
	}

	app.expireMediaImportSessions(now.Add(config.SessionTTL))
	app.mediaImports.mu.Lock()
	active := len(app.mediaImports.sessions)
	app.mediaImports.mu.Unlock()
	if active != 0 {
		t.Fatalf("active sessions after TTL = %d", active)
	}
	for _, session := range sessions {
		if _, err := app.AppendMediaImportChunk(session.ID, 0, base64.StdEncoding.EncodeToString([]byte("data"))); !errors.Is(err, errMediaImportClosed) {
			t.Fatalf("expired append error = %v，want closed", err)
		}
	}
	assertNoMediaImportTemps(t, app.dataDir)

	if _, err := app.BeginMediaImport(mediaImportKindPDF, "after-expiry.pdf", 32); err != nil {
		t.Fatalf("session slot was not released after TTL: %v", err)
	}
}

func TestMediaConcurrentBeginReservesAtMostFourStages(t *testing.T) {
	app, config, hooks, _ := newMediaImportTestApp(t)
	config.MaxSessions = 4
	stageLimitReached := make(chan struct{})
	releaseStages := make(chan struct{})
	var stageCount atomic.Int32
	hooks.afterDestinationOpen = func(*os.Root, string, *os.Root) error {
		if stageCount.Add(1) == int32(config.MaxSessions) {
			close(stageLimitReached)
		}
		<-releaseStages
		return nil
	}

	const attempts = 12
	start := make(chan struct{})
	results := make(chan MediaImportSession, attempts)
	errorsFound := make(chan error, attempts)
	var workers sync.WaitGroup
	for index := 0; index < attempts; index++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			<-start
			session, err := app.BeginMediaImport(mediaImportKindPDF, "concurrent.pdf", 32)
			if err != nil {
				errorsFound <- err
				return
			}
			results <- session
		}(index)
	}
	close(start)
	select {
	case <-stageLimitReached:
	case <-time.After(time.Second):
		t.Fatal("four reserved stages did not reach the barrier")
	}
	close(releaseStages)
	workers.Wait()
	close(results)
	close(errorsFound)

	var sessions []MediaImportSession
	for session := range results {
		sessions = append(sessions, session)
	}
	var rejected int
	for err := range errorsFound {
		if !strings.Contains(err.Error(), "at most 4") {
			t.Errorf("unexpected Begin error: %v", err)
		}
		rejected++
	}
	if len(sessions) != config.MaxSessions || rejected != attempts-config.MaxSessions {
		t.Fatalf("concurrent Begin success=%d rejected=%d，want %d/%d", len(sessions), rejected, config.MaxSessions, attempts-config.MaxSessions)
	}
	if stageCount.Load() != int32(config.MaxSessions) {
		t.Fatalf("stages created = %d，want %d", stageCount.Load(), config.MaxSessions)
	}
	for _, session := range sessions {
		if err := app.AbortMediaImport(session.ID); err != nil {
			t.Error(err)
		}
	}
	app.mediaImports.mu.Lock()
	active := len(app.mediaImports.sessions)
	creating := app.mediaImports.creating
	app.mediaImports.mu.Unlock()
	if active != 0 || creating != 0 {
		t.Fatalf("post-abort state sessions=%d creating=%d", active, creating)
	}
	assertNoMediaImportTemps(t, app.dataDir)
}

func TestMediaSessionExpiryClosesAlreadyLookedUpAppend(t *testing.T) {
	app, config, hooks, _ := newMediaImportTestApp(t)
	config.SessionTTL = time.Minute
	now := time.Date(2026, time.August, 23, 0, 0, 0, 0, time.UTC)
	hooks.now = func() time.Time { return now }
	lookupReached := make(chan struct{})
	releaseAppend := make(chan struct{})
	var once sync.Once
	hooks.afterSessionLookup = func() {
		once.Do(func() { close(lookupReached) })
		<-releaseAppend
	}

	session, err := app.BeginMediaImport(mediaImportKindPDF, "race.pdf", 32)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, appendErr := app.AppendMediaImportChunk(session.ID, 0, base64.StdEncoding.EncodeToString(mediaImportPDFPayload(16)))
		result <- appendErr
	}()
	<-lookupReached
	app.expireMediaImportSessions(now.Add(config.SessionTTL))
	close(releaseAppend)
	if err := <-result; !errors.Is(err, errMediaImportClosed) {
		t.Fatalf("looked-up append after expiry = %v，want closed", err)
	}
	assertNoMediaImportTemps(t, app.dataDir)
}

func TestBeginMediaImportIsOwnedByLifecycleDrain(t *testing.T) {
	app, _, hooks, _ := newMediaImportTestApp(t)
	stageReached := make(chan struct{})
	releaseStage := make(chan struct{})
	var once sync.Once
	hooks.afterDestinationOpen = func(*os.Root, string, *os.Root) error {
		once.Do(func() { close(stageReached) })
		<-releaseStage
		return nil
	}

	result := make(chan error, 1)
	go func() {
		_, err := app.BeginMediaImport(mediaImportKindPDF, "slow-begin.pdf", 32)
		result <- err
	}()
	<-stageReached
	if !app.lifecycle.beginShutdown() {
		t.Fatal("lifecycle shutdown did not begin")
	}
	shortContext, shortCancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	if app.lifecycle.wait(shortContext) {
		shortCancel()
		t.Fatal("lifecycle drained while Begin was still creating its stage")
	}
	shortCancel()
	close(releaseStage)
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("slow Begin error = %v，want cancellation", err)
	}
	waitContext, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	if !app.lifecycle.wait(waitContext) {
		t.Fatal("lifecycle did not drain after Begin cleanup")
	}
	app.mediaImports.mu.Lock()
	active := len(app.mediaImports.sessions)
	creating := app.mediaImports.creating
	app.mediaImports.mu.Unlock()
	if active != 0 || creating != 0 {
		t.Fatalf("post-shutdown state sessions=%d creating=%d", active, creating)
	}
	assertNoMediaImportTemps(t, app.dataDir)
}

func TestMediaNativeDecodeIsOwnedAndCancelledBeforePublish(t *testing.T) {
	app, _, hooks, recorder := newMediaImportTestApp(t)
	decodeReached := make(chan struct{})
	releaseDecode := make(chan struct{})
	hooks.decodeImage = func(io.Reader) (image.Image, string, error) {
		close(decodeReached)
		<-releaseDecode
		return image.NewRGBA(image.Rect(0, 0, 2, 2)), "png", nil
	}

	result := make(chan error, 1)
	go func() {
		_, err := app.importImageFromReader("slow.png", bytes.NewReader(mediaImportPNG(t, 2, 2)))
		result <- err
	}()
	<-decodeReached
	if !app.lifecycle.beginShutdown() {
		t.Fatal("lifecycle shutdown did not begin")
	}
	shortContext, shortCancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	if app.lifecycle.wait(shortContext) {
		shortCancel()
		t.Fatal("lifecycle drained during an owned decode")
	}
	shortCancel()
	close(releaseDecode)
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("decode cancellation error = %v", err)
	}
	waitContext, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	if !app.lifecycle.wait(waitContext) {
		t.Fatal("lifecycle did not drain after decode cancellation")
	}
	if recorder.eventCount() != 0 {
		t.Fatal("cancelled decode emitted an event")
	}
	if files := regularFilesUnder(t, filepath.Join(app.dataDir, "data", "image")); len(files) != 0 {
		t.Fatalf("cancelled decode published files: %v", files)
	}
	assertNoMediaImportTemps(t, app.dataDir)
}

func TestMediaChunkFinishCancellationPreventsImagePublish(t *testing.T) {
	app, _, hooks, recorder := newMediaImportTestApp(t)
	decodeReached := make(chan struct{})
	releaseDecode := make(chan struct{})
	hooks.decodeImage = func(io.Reader) (image.Image, string, error) {
		close(decodeReached)
		<-releaseDecode
		return image.NewRGBA(image.Rect(0, 0, 2, 2)), "png", nil
	}
	payload := mediaImportPNG(t, 2, 2)
	session, err := app.BeginMediaImport(mediaImportKindImage, "slow.png", int64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.AppendMediaImportChunk(session.ID, 0, base64.StdEncoding.EncodeToString(payload)); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, finishErr := app.FinishMediaImport(session.ID)
		result <- finishErr
	}()
	<-decodeReached
	if !app.lifecycle.beginShutdown() {
		t.Fatal("lifecycle shutdown did not begin")
	}
	close(releaseDecode)
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Finish cancellation error = %v", err)
	}
	waitContext, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	if !app.lifecycle.wait(waitContext) {
		t.Fatal("chunk watcher and Finish did not drain")
	}
	if recorder.eventCount() != 0 {
		t.Fatal("cancelled Finish emitted an event")
	}
	if files := regularFilesUnder(t, filepath.Join(app.dataDir, "data", "image")); len(files) != 0 {
		t.Fatalf("cancelled Finish published files: %v", files)
	}
	assertNoMediaImportTemps(t, app.dataDir)
}

func TestMediaCollisionRetryObservesCancellation(t *testing.T) {
	t.Run("single", func(t *testing.T) {
		app, _, hooks, recorder := newMediaImportTestApp(t)
		var linkCalls atomic.Int32
		hooks.link = func(*os.Root, string, string) error {
			linkCalls.Add(1)
			app.lifecycle.cancelShutdownWorkers()
			return os.ErrExist
		}
		_, err := app.importPdfFromReader("collision.pdf", bytes.NewReader(mediaImportPDFPayload(32)))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("collision cancellation error = %v", err)
		}
		if linkCalls.Load() != 1 {
			t.Fatalf("Link calls after cancellation = %d，want 1", linkCalls.Load())
		}
		if recorder.eventCount() != 0 {
			t.Fatal("cancelled collision emitted an event")
		}
		assertNoMediaImportTemps(t, app.dataDir)
	})

	t.Run("image pair rollback", func(t *testing.T) {
		app, _, hooks, recorder := newMediaImportTestApp(t)
		defaultLink := hooks.link
		var linkCalls atomic.Int32
		hooks.encodeWebP = func(writer io.Writer, _ image.Image, _ bool) error {
			_, err := writer.Write([]byte("RIFF\x04\x00\x00\x00WEBPdata"))
			return err
		}
		hooks.link = func(root *os.Root, oldName, newName string) error {
			call := linkCalls.Add(1)
			if call == 2 {
				app.lifecycle.cancelShutdownWorkers()
				return os.ErrExist
			}
			return defaultLink(root, oldName, newName)
		}
		_, err := app.importImageFromReader("collision.png", bytes.NewReader(mediaImportPNG(t, 1, 1)))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("image collision cancellation error = %v", err)
		}
		if linkCalls.Load() != 2 {
			t.Fatalf("image Link calls after rollback cancellation = %d，want 2", linkCalls.Load())
		}
		if recorder.eventCount() != 0 {
			t.Fatal("cancelled image collision emitted an event")
		}
		if files := regularFilesUnder(t, filepath.Join(app.dataDir, "data", "image")); len(files) != 0 {
			t.Fatalf("cancelled image collision left files: %v", files)
		}
		assertNoMediaImportTemps(t, app.dataDir)
	})
}

func TestAudioImportStartsASROnlyAfterDurablePublish(t *testing.T) {
	app, _, hooks, recorder := newMediaImportTestApp(t)
	var publishedAtASR atomic.Bool
	hooks.startTranscription = func(_ *App, absolutePath, _ string) {
		if info, err := os.Stat(absolutePath); err == nil && info.Mode().IsRegular() {
			publishedAtASR.Store(true)
		}
		recorder.mu.Lock()
		recorder.transcriptions = append(recorder.transcriptions, absolutePath)
		recorder.mu.Unlock()
	}
	payload := append([]byte("ID3"), bytes.Repeat([]byte{'a'}, 64)...)
	path, err := app.importAudioFromReader("voice.mp3", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if !publishedAtASR.Load() {
		t.Fatal("ASR started before a published regular file was visible")
	}
	if recorder.eventCount() != 1 || recorder.transcriptionCount() != 1 {
		t.Fatalf("audio events=%d ASR=%d，want 1/1", recorder.eventCount(), recorder.transcriptionCount())
	}
	if _, err := os.Stat(filepath.Join(app.dataDir, filepath.FromSlash(path))); err != nil {
		t.Fatal(err)
	}
}
