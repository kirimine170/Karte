package main

import (
	"bytes"
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	fm "karte/internal/frontmatter"
)

const (
	transcriptPartialInterval = 300 * time.Millisecond
	transcriptBatchDelay      = time.Second
	transcriptCheckpointDelay = 30 * time.Second
	transcriptBatchBytes      = 64 * 1024
	transcriptMaxPendingBytes = 1024 * 1024
	transcriptFileOpenFlags   = os.O_WRONLY | os.O_APPEND
)

var (
	errTranscriptBufferClosed = errors.New("transcript buffer is closed")
	errTranscriptBufferFailed = errors.New("transcript buffer failed")
	errTranscriptPathActive   = errors.New("transcript is actively being generated")
	errTranscriptPendingLimit = errors.New("transcript pending buffer limit exceeded")
	errTranscriptPathExists   = errors.New("transcript creation path already exists")
)

type transcriptAppendFile interface {
	io.Writer
	Sync() error
	Close() error
}

type transcriptTimer interface {
	Stop() bool
}

type transcriptClock interface {
	AfterFunc(time.Duration, func()) transcriptTimer
}

type realTranscriptClock struct{}

func (realTranscriptClock) AfterFunc(delay time.Duration, callback func()) transcriptTimer {
	return time.AfterFunc(delay, callback)
}

type rootedTranscriptFile struct {
	file *os.File
	root *os.Root
}

func (file *rootedTranscriptFile) Write(data []byte) (int, error) {
	return file.file.Write(data)
}

func (file *rootedTranscriptFile) Sync() error {
	return file.file.Sync()
}

func (file *rootedTranscriptFile) Close() error {
	return errors.Join(file.file.Close(), file.root.Close())
}

type transcriptBufferHooks struct {
	open            func(string) (transcriptAppendFile, error)
	clock           transcriptClock
	partialInterval time.Duration
	batchDelay      time.Duration
	checkpointDelay time.Duration
	batchBytes      int
	maxPendingBytes int
	onError         func(error)
}

func defaultTranscriptBufferHooks() transcriptBufferHooks {
	return transcriptBufferHooks{
		clock:           realTranscriptClock{},
		partialInterval: transcriptPartialInterval,
		batchDelay:      transcriptBatchDelay,
		checkpointDelay: transcriptCheckpointDelay,
		batchBytes:      transcriptBatchBytes,
		maxPendingBytes: transcriptMaxPendingBytes,
	}
}

type transcriptPartialPayload struct {
	Text           string
	Timestamp      float64
	TranscriptPath string
}

type transcriptBufferEvent struct {
	partialGeneration uint64
	partial           bool
	callback          func()
}

type transcriptBuffer struct {
	mu sync.Mutex

	eventMu       sync.Mutex
	eventQueue    []transcriptBufferEvent
	eventDispatch bool

	owner    *App
	relPath  string
	absPath  string
	file     transcriptAppendFile
	fileInfo os.FileInfo
	hooks    transcriptBufferHooks

	pending  bytes.Buffer
	dirty    bool
	mutated  bool
	closed   bool
	failure  error
	closeErr error

	batchTimer        transcriptTimer
	checkpointTimer   transcriptTimer
	partialTimer      transcriptTimer
	partialPending    bool
	partialGeneration uint64
	partialPayload    transcriptPartialPayload
	partialEmit       func(transcriptPartialPayload)

	derivedOnce sync.Once
	derivedErr  error
}

type appTranscriptState struct {
	mu            sync.Mutex
	active        map[string]*transcriptBuffer
	mutating      map[string]transcriptMutationEntry
	dirty         map[string]error
	hooks         *transcriptBufferHooks
	publish       func(*App, string) error
	emit          func(*App, string, interface{})
	beforeInstall func(string)
	afterInstall  func(string)
}

type transcriptMutationEntry struct {
	reservation *transcriptMutationReservation
	fileInfo    os.FileInfo
}

type transcriptMutationReservation struct {
	app      *App
	paths    []string
	creating bool
	once     sync.Once
}

type transcriptDocumentMapTransaction struct {
	app      *App
	path     string
	snapshot documentFileSnapshot
	finished bool
}

func (reservation *transcriptMutationReservation) Release() {
	if reservation == nil || reservation.app == nil {
		return
	}
	reservation.once.Do(func() {
		reservation.app.transcripts.mu.Lock()
		for _, path := range reservation.paths {
			entry, exists := reservation.app.transcripts.mutating[path]
			if exists && entry.reservation == reservation {
				delete(reservation.app.transcripts.mutating, path)
			}
		}
		reservation.app.transcripts.mu.Unlock()
	})
}

func normalizeTranscriptBufferHooks(hooks transcriptBufferHooks) transcriptBufferHooks {
	defaults := defaultTranscriptBufferHooks()
	if hooks.clock == nil {
		hooks.clock = defaults.clock
	}
	if hooks.partialInterval <= 0 {
		hooks.partialInterval = defaults.partialInterval
	}
	if hooks.batchDelay <= 0 {
		hooks.batchDelay = defaults.batchDelay
	}
	if hooks.checkpointDelay <= 0 {
		hooks.checkpointDelay = defaults.checkpointDelay
	}
	if hooks.batchBytes <= 0 {
		hooks.batchBytes = defaults.batchBytes
	}
	if hooks.maxPendingBytes <= 0 {
		hooks.maxPendingBytes = defaults.maxPendingBytes
	}
	if hooks.maxPendingBytes < hooks.batchBytes {
		hooks.maxPendingBytes = hooks.batchBytes
	}
	return hooks
}

func normalizeTranscriptPath(path string) string {
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(strings.TrimSpace(path))))
}

func openConfinedTranscriptFile(app *App, relPath, absPath string) (transcriptAppendFile, os.FileInfo, error) {
	root, err := os.OpenRoot(app.dataDir)
	if err != nil {
		return nil, nil, fmt.Errorf("open transcript root: %w", err)
	}
	dataRelative, err := filepath.Rel(app.dataDir, absPath)
	if err != nil || !filepath.IsLocal(dataRelative) {
		_ = root.Close()
		return nil, nil, fmt.Errorf("invalid transcript path relative to data root: %s", relPath)
	}
	opened, err := root.OpenFile(dataRelative, transcriptFileOpenFlags, 0)
	if err != nil {
		_ = root.Close()
		return nil, nil, fmt.Errorf("open transcript for append: %w", err)
	}
	closeOpened := func(openErr error) (transcriptAppendFile, os.FileInfo, error) {
		return nil, nil, errors.Join(openErr, opened.Close(), root.Close())
	}
	openedInfo, err := opened.Stat()
	if err != nil {
		return closeOpened(fmt.Errorf("inspect opened transcript: %w", err))
	}
	pathInfo, err := os.Lstat(absPath)
	if err != nil {
		return closeOpened(fmt.Errorf("inspect transcript path after open: %w", err))
	}
	if !openedInfo.Mode().IsRegular() || !pathInfo.Mode().IsRegular() || pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(openedInfo, pathInfo) {
		return closeOpened(errors.New("transcript append target identity is not a regular non-symlink file"))
	}
	return &rootedTranscriptFile{file: opened, root: root}, openedInfo, nil
}

func (app *App) newTranscriptBuffer(
	relPath string,
	partialEmit func(transcriptPartialPayload),
	onError func(error),
) (*transcriptBuffer, error) {
	return app.newTranscriptBufferReserved(relPath, partialEmit, onError, nil)
}

func (app *App) newTranscriptBufferReserved(
	relPath string,
	partialEmit func(transcriptPartialPayload),
	onError func(error),
	reservation *transcriptMutationReservation,
) (*transcriptBuffer, error) {
	if app == nil {
		return nil, errors.New("app is nil")
	}
	relPath = normalizeTranscriptPath(relPath)
	absPath, ok := app.resolveContentPath(relPath)
	if !ok || !strings.EqualFold(filepath.Ext(relPath), ".md") {
		return nil, fmt.Errorf("invalid transcript path: %s", relPath)
	}
	info, err := os.Lstat(absPath)
	if err != nil {
		return nil, fmt.Errorf("inspect transcript for append: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("transcript append target is not a regular file")
	}

	app.transcripts.mu.Lock()
	if app.transcripts.active != nil && app.transcripts.active[relPath] != nil {
		app.transcripts.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", errTranscriptPathActive, relPath)
	}
	mutation, mutating := app.transcripts.mutating[relPath]
	if mutating && (reservation == nil || mutation.reservation != reservation) {
		app.transcripts.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", errTranscriptPathActive, relPath)
	}
	if reservation != nil && (!mutating || mutation.reservation != reservation) {
		app.transcripts.mu.Unlock()
		return nil, fmt.Errorf("transcript mutation reservation is not owned: %s", relPath)
	}
	for mutationPath, mutationEntry := range app.transcripts.mutating {
		if mutationEntry.reservation != nil && mutationEntry.reservation != reservation && mutationEntry.reservation.creating {
			app.transcripts.mu.Unlock()
			return nil, fmt.Errorf("%w: transcript creation at %s blocks %s", errTranscriptPathActive, mutationPath, relPath)
		}
	}
	hooks := defaultTranscriptBufferHooks()
	if app.transcripts.hooks != nil {
		hooks = *app.transcripts.hooks
	}
	hooks = normalizeTranscriptBufferHooks(hooks)
	if onError != nil {
		hooks.onError = onError
	}
	var file transcriptAppendFile
	var fileInfo os.FileInfo
	if hooks.open != nil {
		file, err = hooks.open(absPath)
	} else {
		file, fileInfo, err = openConfinedTranscriptFile(app, relPath, absPath)
	}
	if err != nil {
		app.transcripts.mu.Unlock()
		return nil, fmt.Errorf("open transcript for append: %w", err)
	}
	if fileInfo != nil && reservation != nil && mutation.fileInfo != nil && !os.SameFile(fileInfo, mutation.fileInfo) {
		_ = file.Close()
		app.transcripts.mu.Unlock()
		return nil, fmt.Errorf("transcript identity changed before active registration: %s", relPath)
	}
	if fileInfo != nil {
		for activePath, activeBuffer := range app.transcripts.active {
			if activeBuffer != nil && activeBuffer.fileInfo != nil && os.SameFile(fileInfo, activeBuffer.fileInfo) {
				_ = file.Close()
				app.transcripts.mu.Unlock()
				return nil, fmt.Errorf("%w: %s aliases %s", errTranscriptPathActive, relPath, activePath)
			}
		}
		for mutationPath, mutationEntry := range app.transcripts.mutating {
			if mutationEntry.reservation == reservation {
				continue
			}
			if mutationEntry.fileInfo != nil && os.SameFile(fileInfo, mutationEntry.fileInfo) {
				_ = file.Close()
				app.transcripts.mu.Unlock()
				return nil, fmt.Errorf("%w: %s aliases %s", errTranscriptPathActive, relPath, mutationPath)
			}
		}
	}
	buffer := &transcriptBuffer{
		owner:       app,
		relPath:     relPath,
		absPath:     absPath,
		file:        file,
		fileInfo:    fileInfo,
		hooks:       hooks,
		partialEmit: partialEmit,
	}
	if app.transcripts.active == nil {
		app.transcripts.active = make(map[string]*transcriptBuffer)
	}
	app.transcripts.active[relPath] = buffer
	app.transcripts.mu.Unlock()
	return buffer, nil
}

func (app *App) createTranscriptDocumentAndBuffer(
	relPath string,
	content string,
	partialEmit func(transcriptPartialPayload),
	onError func(error),
) (*transcriptBuffer, error) {
	reservation, err := app.reserveTranscriptCreationPath(relPath)
	if err != nil {
		return nil, err
	}
	defer reservation.Release()
	content, docID, err := app.prepareTranscriptDocumentContent(content)
	if err != nil {
		return nil, err
	}
	installedInfo, err := app.installTranscriptDocumentNoReplace(relPath, content)
	if err != nil {
		return nil, err
	}
	cleanupInstalled := func(cause error) error {
		cleanupErr := app.removeInstalledTranscriptPath(relPath, installedInfo)
		return errors.Join(cause, cleanupErr)
	}
	if err := app.pinTranscriptMutationIdentity(reservation, relPath, installedInfo); err != nil {
		return nil, cleanupInstalled(err)
	}

	app.transcripts.mu.Lock()
	afterInstall := app.transcripts.afterInstall
	app.transcripts.mu.Unlock()
	if afterInstall != nil {
		afterInstall(relPath)
	}
	if err := app.pinTranscriptMutationIdentity(reservation, relPath, installedInfo); err != nil {
		return nil, cleanupInstalled(err)
	}
	buffer, err := app.newTranscriptBufferReserved(relPath, partialEmit, onError, reservation)
	if err != nil {
		return nil, cleanupInstalled(err)
	}
	if docID == "" {
		return buffer, nil
	}
	mappingTransaction, err := app.beginTranscriptDocumentMapping(docID, relPath)
	if err != nil {
		abortErr := buffer.Abort()
		return nil, cleanupInstalled(errors.Join(err, abortErr))
	}
	if err := app.verifyInstalledTranscriptIdentity(relPath, installedInfo); err != nil {
		mappingRollbackErr := mappingTransaction.Rollback()
		abortErr := buffer.Abort()
		return nil, cleanupInstalled(errors.Join(err, mappingRollbackErr, abortErr))
	}
	mappingTransaction.Commit()
	return buffer, nil
}

func (app *App) beginTranscriptDocumentMapping(docID, relPath string) (*transcriptDocumentMapTransaction, error) {
	app.documentMapMu.Lock()
	documentMapPath, err := app.documentMapStore.preparePath(app.dataDir)
	if err != nil {
		app.documentMapMu.Unlock()
		return nil, fmt.Errorf("prepare transcript document map: %w", err)
	}
	snapshot := captureDocumentFileSnapshot(documentMapPath)
	if !snapshot.captured {
		app.documentMapMu.Unlock()
		return nil, errors.New("capture transcript document map before update")
	}
	if _, err := app.documentMapStore.update(app.dataDir, map[string]string{docID: relPath}); err != nil {
		rollbackErr := rollbackDocumentFile(documentMapPath, snapshot)
		app.documentMapMu.Unlock()
		return nil, errors.Join(fmt.Errorf("create transcript document map update failed: %w", err), rollbackErr)
	}
	return &transcriptDocumentMapTransaction{
		app:      app,
		path:     documentMapPath,
		snapshot: snapshot,
	}, nil
}

func (transaction *transcriptDocumentMapTransaction) Commit() {
	if transaction == nil || transaction.app == nil || transaction.finished {
		return
	}
	transaction.finished = true
	transaction.app.documentMapMu.Unlock()
}

func (transaction *transcriptDocumentMapTransaction) Rollback() error {
	if transaction == nil || transaction.app == nil || transaction.finished {
		return nil
	}
	transaction.finished = true
	err := rollbackDocumentFile(transaction.path, transaction.snapshot)
	transaction.app.documentMapMu.Unlock()
	return err
}

func (app *App) verifyInstalledTranscriptIdentity(relPath string, installedInfo os.FileInfo) error {
	absPath, ok := app.resolveContentPath(relPath)
	if !ok {
		return fmt.Errorf("invalid transcript path after document map update: %s", relPath)
	}
	currentInfo, err := os.Lstat(absPath)
	if err != nil {
		return fmt.Errorf("inspect transcript after document map update: %w", err)
	}
	if installedInfo == nil || !currentInfo.Mode().IsRegular() || currentInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(installedInfo, currentInfo) {
		return fmt.Errorf("created transcript identity changed after document map update: %s", relPath)
	}
	return nil
}

func (app *App) prepareTranscriptDocumentContent(content string) (string, string, error) {
	contentWithDocID, docID, err := app.ensureDocID(content)
	if err != nil {
		return "", "", fmt.Errorf("prepare transcript doc_id: %w", err)
	}
	frontMatter, markdownBody := fm.ParseFrontMatter(contentWithDocID)
	if frontMatter != nil {
		content = fm.FormatFrontMatter(frontMatter) + markdownBody
		if frontMatter.DocID != "" {
			docID = frontMatter.DocID
		}
	} else {
		content = contentWithDocID
	}
	return content, docID, nil
}

func (app *App) pinTranscriptMutationIdentity(
	reservation *transcriptMutationReservation,
	relPath string,
	installedInfo os.FileInfo,
) error {
	if installedInfo == nil {
		return errors.New("created transcript identity is unavailable")
	}
	absPath, ok := app.resolveContentPath(relPath)
	if !ok {
		return fmt.Errorf("invalid transcript path after creation: %s", relPath)
	}
	info, err := os.Lstat(absPath)
	if err != nil {
		return fmt.Errorf("inspect transcript after creation: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("created transcript is not a regular non-symlink file")
	}
	if !os.SameFile(info, installedInfo) {
		return fmt.Errorf("created transcript identity changed before registration: %s", relPath)
	}
	app.transcripts.mu.Lock()
	defer app.transcripts.mu.Unlock()
	entry, exists := app.transcripts.mutating[normalizeTranscriptPath(relPath)]
	if !exists || entry.reservation != reservation {
		return fmt.Errorf("transcript creation reservation was lost: %s", relPath)
	}
	entry.fileInfo = info
	app.transcripts.mutating[normalizeTranscriptPath(relPath)] = entry
	return nil
}

func (app *App) installTranscriptDocumentNoReplace(relPath, content string) (os.FileInfo, error) {
	absPath, ok := app.resolveContentPath(relPath)
	if !ok {
		return nil, fmt.Errorf("invalid transcript creation path: %s", relPath)
	}
	root, err := os.OpenRoot(app.dataDir)
	if err != nil {
		return nil, fmt.Errorf("open transcript creation root: %w", err)
	}
	dataRelative, err := filepath.Rel(app.dataDir, absPath)
	if err != nil || !filepath.IsLocal(dataRelative) {
		_ = root.Close()
		return nil, fmt.Errorf("invalid transcript creation path relative to data root: %s", relPath)
	}
	directoryRelative := filepath.Dir(dataRelative)
	if err := root.MkdirAll(directoryRelative, 0o755); err != nil {
		_ = root.Close()
		return nil, fmt.Errorf("prepare transcript creation directory: %w", err)
	}
	tempFile, tempRelative, err := createTranscriptTempFile(root, dataRelative)
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	tempPresent := true
	defer func() {
		_ = tempFile.Close()
		if tempPresent {
			if err := root.Remove(tempRelative); err == nil {
				_ = syncTranscriptRootDirectory(root, directoryRelative)
			}
		}
		_ = root.Close()
	}()
	if err := tempFile.Chmod(0o644); err != nil {
		return nil, fmt.Errorf("set transcript temporary file permissions: %w", err)
	}
	if err := writeTranscriptFileFully(tempFile, []byte(content)); err != nil {
		return nil, fmt.Errorf("write transcript temporary file: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		return nil, fmt.Errorf("sync transcript temporary file: %w", err)
	}
	tempInfo, statErr := tempFile.Stat()
	closeErr := tempFile.Close()
	if err := errors.Join(statErr, closeErr); err != nil {
		return nil, fmt.Errorf("close transcript temporary file: %w", err)
	}
	if tempInfo == nil || !tempInfo.Mode().IsRegular() {
		return nil, errors.New("transcript temporary file is not regular")
	}

	app.transcripts.mu.Lock()
	beforeInstall := app.transcripts.beforeInstall
	app.transcripts.mu.Unlock()
	if beforeInstall != nil {
		beforeInstall(relPath)
	}
	if err := root.Link(tempRelative, dataRelative); err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("%w: %s", errTranscriptPathExists, relPath)
		}
		return nil, fmt.Errorf("install transcript without replacement: %w", err)
	}
	if err := syncTranscriptRootDirectory(root, directoryRelative); err != nil {
		rollbackErr := rollbackInstalledTranscriptRoot(root, dataRelative, tempInfo)
		return nil, errors.Join(fmt.Errorf("sync installed transcript directory: %w", err), rollbackErr)
	}
	installedInfo, err := root.Lstat(dataRelative)
	if err != nil || !installedInfo.Mode().IsRegular() || installedInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(tempInfo, installedInfo) {
		identityErr := errors.New("installed transcript identity changed")
		if err != nil {
			identityErr = fmt.Errorf("inspect installed transcript identity: %w", err)
		}
		rollbackErr := rollbackInstalledTranscriptRoot(root, dataRelative, tempInfo)
		return nil, errors.Join(identityErr, rollbackErr)
	}
	if err := root.Remove(tempRelative); err == nil {
		tempPresent = false
		// The target link was already durable．Failure to persist removal of the
		// private temporary name must not roll back the only public name．
		_ = syncTranscriptRootDirectory(root, directoryRelative)
	}
	return installedInfo, nil
}

func createTranscriptTempFile(root *os.Root, dataRelative string) (*os.File, string, error) {
	for attempt := 0; attempt < 32; attempt++ {
		nonce := make([]byte, 16)
		if _, err := cryptorand.Read(nonce); err != nil {
			return nil, "", fmt.Errorf("generate transcript temporary name: %w", err)
		}
		name := fmt.Sprintf(".%s.tmp-%x", filepath.Base(dataRelative), nonce)
		tempRelative := filepath.Join(filepath.Dir(dataRelative), name)
		file, err := root.OpenFile(tempRelative, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			return file, tempRelative, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, "", fmt.Errorf("create transcript temporary file: %w", err)
		}
	}
	return nil, "", errors.New("transcript temporary name space exhausted")
}

func writeTranscriptFileFully(file *os.File, data []byte) error {
	for len(data) > 0 {
		written, err := file.Write(data)
		if written < 0 || written > len(data) {
			return fmt.Errorf("invalid transcript creation write count: %d of %d", written, len(data))
		}
		data = data[written:]
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func syncTranscriptRootDirectory(root *os.Root, directoryRelative string) error {
	directory, err := root.Open(directoryRelative)
	if err != nil {
		return err
	}
	return errors.Join(syncTranscriptDirectory(directory), directory.Close())
}

func rollbackInstalledTranscriptRoot(root *os.Root, dataRelative string, installedInfo os.FileInfo) error {
	currentInfo, err := root.Lstat(dataRelative)
	if errors.Is(err, os.ErrNotExist) {
		return syncTranscriptRootDirectory(root, filepath.Dir(dataRelative))
	}
	if err != nil {
		return fmt.Errorf("inspect transcript rollback target: %w", err)
	}
	if installedInfo == nil || !currentInfo.Mode().IsRegular() || !os.SameFile(installedInfo, currentInfo) {
		return errors.New("transcript rollback target identity changed; external file was preserved")
	}
	removeErr := root.Remove(dataRelative)
	syncErr := syncTranscriptRootDirectory(root, filepath.Dir(dataRelative))
	return errors.Join(removeErr, syncErr)
}

func (app *App) removeInstalledTranscriptPath(relPath string, installedInfo os.FileInfo) error {
	if app == nil || installedInfo == nil {
		return nil
	}
	absPath, ok := app.resolveContentPath(relPath)
	if !ok {
		return fmt.Errorf("invalid installed transcript path: %s", relPath)
	}
	root, err := os.OpenRoot(app.dataDir)
	if err != nil {
		return fmt.Errorf("open transcript cleanup root: %w", err)
	}
	defer root.Close()
	dataRelative, err := filepath.Rel(app.dataDir, absPath)
	if err != nil || !filepath.IsLocal(dataRelative) {
		return fmt.Errorf("invalid installed transcript path relative to data root: %s", relPath)
	}
	return rollbackInstalledTranscriptRoot(root, dataRelative, installedInfo)
}

func (app *App) transcriptPathActive(relPath string) bool {
	if app == nil {
		return false
	}
	relPath = normalizeTranscriptPath(relPath)
	app.transcripts.mu.Lock()
	active := app.transcripts.active != nil && app.transcripts.active[relPath] != nil
	app.transcripts.mu.Unlock()
	return active
}

// reserveTranscriptPathMutation closes the check/use race between editor
// replacement or rename operations and a newly opened append session．
func (app *App) reserveTranscriptPathMutation(paths ...string) (func(), error) {
	reservation, err := app.reserveTranscriptPathMutationLease(paths...)
	if err != nil {
		return nil, err
	}
	return reservation.Release, nil
}

func (app *App) reserveTranscriptPathMutationLease(paths ...string) (*transcriptMutationReservation, error) {
	return app.reserveTranscriptPathMutationLeaseMode(false, paths...)
}

func (app *App) reserveTranscriptCreationPath(path string) (*transcriptMutationReservation, error) {
	return app.reserveTranscriptPathMutationLeaseMode(true, path)
}

func (app *App) reserveTranscriptPathMutationLeaseMode(creating bool, paths ...string) (*transcriptMutationReservation, error) {
	if app == nil {
		return &transcriptMutationReservation{creating: creating}, nil
	}
	normalized := make([]string, 0, len(paths))
	identities := make(map[string]os.FileInfo, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		path = normalizeTranscriptPath(path)
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		normalized = append(normalized, path)
	}

	app.transcripts.mu.Lock()
	for mutationPath, mutationEntry := range app.transcripts.mutating {
		if mutationEntry.reservation != nil && (creating || mutationEntry.reservation.creating) {
			requestedPath := mutationPath
			if len(normalized) > 0 {
				requestedPath = normalized[0]
			}
			app.transcripts.mu.Unlock()
			return nil, fmt.Errorf("%w: transcript creation reservation blocks %s at %s", errTranscriptPathActive, requestedPath, mutationPath)
		}
	}
	for _, path := range normalized {
		if absPath, ok := app.resolveContentPath(path); ok {
			if info, err := os.Stat(absPath); err == nil {
				identities[path] = info
			}
		}
	}
	for _, path := range normalized {
		if app.transcripts.active[path] != nil {
			app.transcripts.mu.Unlock()
			return nil, fmt.Errorf("%w: %s", errTranscriptPathActive, path)
		}
		if _, exists := app.transcripts.mutating[path]; exists {
			app.transcripts.mu.Unlock()
			return nil, fmt.Errorf("%w: %s", errTranscriptPathActive, path)
		}
		identity := identities[path]
		if identity == nil {
			continue
		}
		for activePath, activeBuffer := range app.transcripts.active {
			if activeBuffer != nil && activeBuffer.fileInfo != nil && os.SameFile(identity, activeBuffer.fileInfo) {
				app.transcripts.mu.Unlock()
				return nil, fmt.Errorf("%w: %s aliases %s", errTranscriptPathActive, path, activePath)
			}
		}
		for mutationPath, mutationInfo := range app.transcripts.mutating {
			if mutationInfo.fileInfo != nil && os.SameFile(identity, mutationInfo.fileInfo) {
				app.transcripts.mu.Unlock()
				return nil, fmt.Errorf("%w: %s aliases %s", errTranscriptPathActive, path, mutationPath)
			}
		}
	}
	reservation := &transcriptMutationReservation{app: app, paths: normalized, creating: creating}
	if app.transcripts.mutating == nil {
		app.transcripts.mutating = make(map[string]transcriptMutationEntry)
	}
	for _, path := range normalized {
		app.transcripts.mutating[path] = transcriptMutationEntry{
			reservation: reservation,
			fileInfo:    identities[path],
		}
	}
	app.transcripts.mu.Unlock()
	return reservation, nil
}

func (app *App) unregisterTranscriptBuffer(buffer *transcriptBuffer) {
	if app == nil || buffer == nil {
		return
	}
	app.transcripts.mu.Lock()
	if app.transcripts.active[buffer.relPath] == buffer {
		delete(app.transcripts.active, buffer.relPath)
	}
	app.transcripts.mu.Unlock()
}

func (app *App) emitTranscriptEvent(name string, data interface{}) {
	if app == nil {
		return
	}
	app.transcripts.mu.Lock()
	emit := app.transcripts.emit
	app.transcripts.mu.Unlock()
	if emit != nil {
		emit(app, name, data)
		return
	}
	app.emitEvent(name, data)
}

func (buffer *transcriptBuffer) UpdatePartial(text string, timestamp float64) error {
	if buffer == nil {
		return errTranscriptBufferClosed
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	if buffer.closed {
		return errTranscriptBufferClosed
	}
	if buffer.failure != nil {
		return errors.Join(errTranscriptBufferFailed, buffer.failure)
	}
	buffer.partialPayload = transcriptPartialPayload{
		Text:           text,
		Timestamp:      timestamp,
		TranscriptPath: buffer.relPath,
	}
	buffer.partialPending = true
	if buffer.partialEmit != nil && buffer.partialTimer == nil {
		buffer.partialTimer = buffer.hooks.clock.AfterFunc(buffer.hooks.partialInterval, buffer.emitLatestPartial)
	}
	return nil
}

func (buffer *transcriptBuffer) emitLatestPartial() {
	buffer.mu.Lock()
	buffer.partialTimer = nil
	if buffer.closed || buffer.failure != nil || !buffer.partialPending || buffer.partialEmit == nil {
		buffer.mu.Unlock()
		return
	}
	payload := buffer.partialPayload
	generation := buffer.partialGeneration
	buffer.partialPending = false
	buffer.mu.Unlock()
	buffer.enqueueEvent(transcriptBufferEvent{
		partial:           true,
		partialGeneration: generation,
		callback: func() {
			buffer.partialEmit(payload)
		},
	})
}

// enqueueEvent uses a run-to-completion queue rather than holding eventMu
// across callbacks．A callback may reenter any buffer method; reentrant events
// are appended and the outer dispatcher delivers them in FIFO order．
func (buffer *transcriptBuffer) enqueueEvent(event transcriptBufferEvent) {
	if buffer == nil || event.callback == nil {
		return
	}
	buffer.eventMu.Lock()
	buffer.eventQueue = append(buffer.eventQueue, event)
	if buffer.eventDispatch {
		buffer.eventMu.Unlock()
		return
	}
	buffer.eventDispatch = true
	buffer.eventMu.Unlock()

	for {
		buffer.eventMu.Lock()
		if len(buffer.eventQueue) == 0 {
			buffer.eventDispatch = false
			buffer.eventMu.Unlock()
			return
		}
		next := buffer.eventQueue[0]
		buffer.eventQueue[0] = transcriptBufferEvent{}
		buffer.eventQueue = buffer.eventQueue[1:]
		buffer.eventMu.Unlock()

		if next.partial {
			buffer.mu.Lock()
			valid := !buffer.closed && buffer.failure == nil && next.partialGeneration == buffer.partialGeneration
			buffer.mu.Unlock()
			if !valid {
				continue
			}
		}
		func() {
			defer func() {
				if recovered := recover(); recovered != nil && buffer.owner != nil {
					buffer.owner.logError(fmt.Sprintf("Transcript event callback panicked: %v", recovered))
				}
			}()
			next.callback()
		}()
	}
}

func (buffer *transcriptBuffer) drainEvents() {
	if buffer == nil {
		return
	}
	done := make(chan struct{})
	buffer.enqueueEvent(transcriptBufferEvent{callback: func() { close(done) }})
	<-done
}

func (buffer *transcriptBuffer) cancelPartialLocked() {
	buffer.partialGeneration++
	buffer.partialPending = false
	buffer.partialPayload = transcriptPartialPayload{}
	if buffer.partialTimer != nil {
		buffer.partialTimer.Stop()
		buffer.partialTimer = nil
	}
}

func (buffer *transcriptBuffer) CancelPartial() {
	if buffer == nil {
		return
	}
	buffer.mu.Lock()
	buffer.cancelPartialLocked()
	buffer.mu.Unlock()
}

func formatTranscriptAppendLine(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	line = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(line)
	line = strings.Join(strings.Fields(line), " ")
	return line + "  \n"
}

func (buffer *transcriptBuffer) AppendFinal(line string) error {
	return buffer.AppendFinalAndEmit(line, nil)
}

// AppendFinalAndEmit serializes final event delivery with partial delivery．The
// file append is accepted before emit is called，and a pending partial is
// canceled before either operation can become visible．
func (buffer *transcriptBuffer) AppendFinalAndEmit(line string, emit func()) error {
	if buffer == nil {
		return errTranscriptBufferClosed
	}
	entry := formatTranscriptAppendLine(line)
	if entry == "" {
		buffer.mu.Lock()
		buffer.cancelPartialLocked()
		buffer.mu.Unlock()
		return nil
	}

	buffer.mu.Lock()
	buffer.cancelPartialLocked()
	if buffer.closed {
		buffer.mu.Unlock()
		return errTranscriptBufferClosed
	}
	if buffer.failure != nil {
		err := errors.Join(errTranscriptBufferFailed, buffer.failure)
		buffer.mu.Unlock()
		return err
	}
	if len(entry) > buffer.hooks.maxPendingBytes {
		err := fmt.Errorf("%w: entry=%d limit=%d", errTranscriptPendingLimit, len(entry), buffer.hooks.maxPendingBytes)
		report := buffer.failLocked(err)
		buffer.mu.Unlock()
		buffer.reportFailure(report)
		return err
	}
	if buffer.pending.Len()+len(entry) > buffer.hooks.maxPendingBytes {
		if err := buffer.flushLocked(); err != nil {
			report := buffer.failLocked(err)
			buffer.mu.Unlock()
			buffer.reportFailure(report)
			return errors.Join(errTranscriptBufferFailed, err)
		}
	}
	if buffer.pending.Len()+len(entry) > buffer.hooks.maxPendingBytes {
		err := fmt.Errorf("%w: pending=%d entry=%d limit=%d", errTranscriptPendingLimit, buffer.pending.Len(), len(entry), buffer.hooks.maxPendingBytes)
		report := buffer.failLocked(err)
		buffer.mu.Unlock()
		buffer.reportFailure(report)
		return err
	}
	_, _ = buffer.pending.WriteString(entry)
	buffer.dirty = true
	buffer.mutated = true
	buffer.scheduleCheckpointLocked()
	if buffer.pending.Len() >= buffer.hooks.batchBytes {
		if err := buffer.flushLocked(); err != nil {
			report := buffer.failLocked(err)
			buffer.mu.Unlock()
			buffer.reportFailure(report)
			return errors.Join(errTranscriptBufferFailed, err)
		}
	} else {
		buffer.scheduleBatchLocked()
	}
	buffer.mu.Unlock()
	if emit != nil {
		buffer.enqueueEvent(transcriptBufferEvent{callback: emit})
	}
	return nil
}

func (buffer *transcriptBuffer) scheduleBatchLocked() {
	if buffer.batchTimer == nil && buffer.pending.Len() > 0 && !buffer.closed {
		buffer.batchTimer = buffer.hooks.clock.AfterFunc(buffer.hooks.batchDelay, buffer.flushBatchTimer)
	}
}

func (buffer *transcriptBuffer) scheduleCheckpointLocked() {
	if buffer.checkpointTimer == nil && buffer.dirty && !buffer.closed {
		buffer.checkpointTimer = buffer.hooks.clock.AfterFunc(buffer.hooks.checkpointDelay, buffer.checkpointTimerFired)
	}
}

func (buffer *transcriptBuffer) flushBatchTimer() {
	buffer.mu.Lock()
	buffer.batchTimer = nil
	if buffer.closed || buffer.failure != nil {
		buffer.mu.Unlock()
		return
	}
	err := buffer.flushLocked()
	var report error
	if err != nil {
		report = buffer.failLocked(err)
	}
	buffer.mu.Unlock()
	buffer.reportFailure(report)
}

func (buffer *transcriptBuffer) checkpointTimerFired() {
	buffer.mu.Lock()
	buffer.checkpointTimer = nil
	if buffer.closed || buffer.failure != nil {
		buffer.mu.Unlock()
		return
	}
	err := buffer.checkpointLocked()
	var report error
	if err != nil {
		report = buffer.failLocked(err)
	}
	buffer.mu.Unlock()
	buffer.reportFailure(report)
}

func (buffer *transcriptBuffer) flushLocked() error {
	if buffer.file == nil {
		return errTranscriptBufferClosed
	}
	if err := buffer.verifyIdentityLocked(); err != nil {
		return err
	}
	for buffer.pending.Len() > 0 {
		data := buffer.pending.Bytes()
		written, err := buffer.file.Write(data)
		if written < 0 || written > len(data) {
			return fmt.Errorf("invalid transcript append count: %d of %d", written, len(data))
		}
		if written > 0 {
			buffer.pending.Next(written)
		}
		if err != nil {
			return fmt.Errorf("append transcript batch: %w", err)
		}
		if written == 0 {
			return fmt.Errorf("append transcript batch: %w", io.ErrShortWrite)
		}
	}
	buffer.pending.Reset()
	if buffer.batchTimer != nil {
		buffer.batchTimer.Stop()
		buffer.batchTimer = nil
	}
	return nil
}

func (buffer *transcriptBuffer) verifyIdentityLocked() error {
	if buffer.fileInfo == nil {
		// Injected test files do not have a filesystem identity．Production opens
		// always set fileInfo through openConfinedTranscriptFile．
		return nil
	}
	pathInfo, err := os.Lstat(buffer.absPath)
	if err != nil {
		return fmt.Errorf("verify active transcript identity: %w", err)
	}
	if !pathInfo.Mode().IsRegular() || pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(buffer.fileInfo, pathInfo) {
		return errors.New("active transcript identity changed")
	}
	return nil
}

func (buffer *transcriptBuffer) checkpointLocked() error {
	if err := buffer.flushLocked(); err != nil {
		return err
	}
	if !buffer.dirty {
		return nil
	}
	if err := buffer.verifyIdentityLocked(); err != nil {
		return err
	}
	if err := buffer.file.Sync(); err != nil {
		return fmt.Errorf("sync transcript checkpoint: %w", err)
	}
	buffer.dirty = false
	return nil
}

func (buffer *transcriptBuffer) failLocked(err error) error {
	if err == nil || buffer.failure != nil {
		return nil
	}
	buffer.failure = err
	if buffer.batchTimer != nil {
		buffer.batchTimer.Stop()
		buffer.batchTimer = nil
	}
	if buffer.checkpointTimer != nil {
		buffer.checkpointTimer.Stop()
		buffer.checkpointTimer = nil
	}
	buffer.cancelPartialLocked()
	return err
}

func (buffer *transcriptBuffer) reportFailure(err error) {
	if err != nil && buffer.hooks.onError != nil {
		buffer.hooks.onError(err)
	}
}

func (buffer *transcriptBuffer) Close() error {
	if buffer == nil {
		return nil
	}
	buffer.mu.Lock()
	if buffer.closed {
		err := buffer.closeErr
		buffer.mu.Unlock()
		return err
	}
	buffer.cancelPartialLocked()
	if buffer.batchTimer != nil {
		buffer.batchTimer.Stop()
		buffer.batchTimer = nil
	}
	if buffer.checkpointTimer != nil {
		buffer.checkpointTimer.Stop()
		buffer.checkpointTimer = nil
	}
	stickyErr := buffer.failure
	flushErr := buffer.flushLocked()
	var syncErr error
	if buffer.dirty {
		if err := buffer.file.Sync(); err != nil {
			syncErr = fmt.Errorf("sync transcript on close: %w", err)
		} else {
			buffer.dirty = false
		}
	}
	var fileCloseErr error
	if buffer.file != nil {
		if err := buffer.file.Close(); err != nil {
			fileCloseErr = fmt.Errorf("close transcript buffer: %w", err)
		}
		buffer.file = nil
	}
	buffer.closed = true
	buffer.closeErr = errors.Join(stickyErr, flushErr, syncErr, fileCloseErr)
	closeErr := buffer.closeErr
	report := errors.Join(flushErr, syncErr, fileCloseErr)
	buffer.mu.Unlock()
	buffer.owner.unregisterTranscriptBuffer(buffer)
	buffer.reportFailure(report)
	return closeErr
}

func (buffer *transcriptBuffer) Abort() error {
	if buffer == nil {
		return nil
	}
	buffer.mu.Lock()
	if buffer.closed {
		err := buffer.closeErr
		buffer.mu.Unlock()
		return err
	}
	buffer.cancelPartialLocked()
	if buffer.batchTimer != nil {
		buffer.batchTimer.Stop()
		buffer.batchTimer = nil
	}
	if buffer.checkpointTimer != nil {
		buffer.checkpointTimer.Stop()
		buffer.checkpointTimer = nil
	}
	buffer.pending.Reset()
	var closeErr error
	if buffer.file != nil {
		if err := buffer.file.Close(); err != nil {
			closeErr = fmt.Errorf("abort transcript buffer: %w", err)
		}
		buffer.file = nil
	}
	buffer.closed = true
	buffer.closeErr = closeErr
	buffer.mu.Unlock()
	buffer.owner.unregisterTranscriptBuffer(buffer)
	buffer.reportFailure(closeErr)
	return closeErr
}

func (buffer *transcriptBuffer) Mutated() bool {
	if buffer == nil {
		return false
	}
	buffer.mu.Lock()
	mutated := buffer.mutated
	buffer.mu.Unlock()
	return mutated
}

func (app *App) completeTranscriptBuffer(buffer *transcriptBuffer) error {
	if buffer == nil {
		return nil
	}
	closeErr := buffer.Close()
	buffer.drainEvents()
	if closeErr != nil {
		return closeErr
	}
	return app.publishTranscriptBuffer(buffer)
}

func (app *App) publishTranscriptBuffer(buffer *transcriptBuffer) error {
	if buffer == nil {
		return nil
	}
	if !buffer.Mutated() {
		return nil
	}
	buffer.derivedOnce.Do(func() {
		app.transcripts.mu.Lock()
		publish := app.transcripts.publish
		app.transcripts.mu.Unlock()
		if publish != nil {
			buffer.derivedErr = publish(app, buffer.relPath)
		} else {
			buffer.derivedErr = app.publishCompletedTranscript(buffer.relPath)
		}
		app.transcripts.mu.Lock()
		if buffer.derivedErr != nil {
			if app.transcripts.dirty == nil {
				app.transcripts.dirty = make(map[string]error)
			}
			app.transcripts.dirty[buffer.relPath] = buffer.derivedErr
		} else {
			delete(app.transcripts.dirty, buffer.relPath)
		}
		app.transcripts.mu.Unlock()
	})
	return buffer.derivedErr
}

// publishCompletedTranscript attempts every derived effect once and aggregates
// failures．publishTranscriptBuffer records a failed attempt in the in-memory
// dirty registry，without replaying already attempted effects in this session．
func (app *App) publishCompletedTranscript(relPath string) error {
	if app == nil || relPath == "" {
		return nil
	}
	var derivedErrs []error
	if app.vcs != nil {
		absPath, ok := app.resolveContentPath(relPath)
		if ok {
			if gitPath, err := filepath.Rel(app.dataDir, absPath); err == nil {
				if err := app.vcs.CommitFile(gitPath, fmt.Sprintf("Update transcript: %s", relPath)); err != nil {
					app.logError(fmt.Sprintf("Failed to commit completed transcript: %v", err))
					derivedErrs = append(derivedErrs, fmt.Errorf("commit completed transcript: %w", err))
				}
			} else {
				derivedErrs = append(derivedErrs, fmt.Errorf("resolve completed transcript Git path: %w", err))
			}
		} else {
			derivedErrs = append(derivedErrs, errors.New("resolve completed transcript for Git"))
		}
	}
	if !app.scheduleSiteBuild(relPath) {
		derivedErrs = append(derivedErrs, errors.New("schedule completed transcript site build"))
	}
	if err := app.RefreshGraphData(); err != nil {
		app.logError(fmt.Sprintf("Refresh graph after transcript completion failed: %v", err))
		derivedErrs = append(derivedErrs, fmt.Errorf("refresh completed transcript graph: %w", err))
	}
	app.emitTranscriptEvent("file-changed", relPath)
	return errors.Join(derivedErrs...)
}
