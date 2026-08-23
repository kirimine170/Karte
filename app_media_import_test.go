package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type mediaImportTestRecorder struct {
	mu             sync.Mutex
	events         []string
	transcriptions []string
}

type generatedMediaPDFReader struct {
	position int64
	size     int64
}

func (reader *generatedMediaPDFReader) Read(data []byte) (int, error) {
	if reader.position >= reader.size {
		return 0, io.EOF
	}
	remaining := reader.size - reader.position
	if int64(len(data)) > remaining {
		data = data[:remaining]
	}
	header := []byte("%PDF-")
	for index := range data {
		position := reader.position + int64(index)
		if position < int64(len(header)) {
			data[index] = header[position]
		} else {
			data[index] = 'x'
		}
	}
	reader.position += int64(len(data))
	return len(data), nil
}

func (recorder *mediaImportTestRecorder) eventCount() int {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return len(recorder.events)
}

func (recorder *mediaImportTestRecorder) transcriptionCount() int {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return len(recorder.transcriptions)
}

func newMediaImportTestApp(t *testing.T) (*App, *mediaImportConfig, *mediaImportHooks, *mediaImportTestRecorder) {
	t.Helper()
	app := NewAppWithFileSystem(OSFileSystem{})
	app.dataDir = t.TempDir()
	config := defaultMediaImportConfig()
	hooks := defaultMediaImportHooks()
	hooks.now = func() time.Time { return time.Date(2026, time.August, 23, 12, 34, 56, 0, time.UTC) }
	recorder := &mediaImportTestRecorder{}
	hooks.emitEvent = func(_ *App, name string, _ interface{}) {
		recorder.mu.Lock()
		recorder.events = append(recorder.events, name)
		recorder.mu.Unlock()
	}
	hooks.refreshImageGraph = func(*App) {}
	hooks.startTranscription = func(_ *App, _, relativePath string) {
		recorder.mu.Lock()
		recorder.transcriptions = append(recorder.transcriptions, relativePath)
		recorder.mu.Unlock()
	}
	app.mediaImports.config = &config
	app.mediaImports.hooks = &hooks
	t.Cleanup(func() {
		app.abortAllMediaImports()
		app.lifecycle.beginShutdownDrain()
		app.lifecycle.cancelShutdownWorkers()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if !app.lifecycle.wait(ctx) {
			t.Error("media import workers did not stop")
		}
	})
	return app, &config, &hooks, recorder
}

func mediaImportPDFPayload(size int) []byte {
	if size < len("%PDF-") {
		panic("PDF fixture is too small")
	}
	data := bytes.Repeat([]byte{'x'}, size)
	copy(data, "%PDF-")
	return data
}

func mediaImportPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func assertNoMediaImportTemps(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(entry.Name(), ".karte-media-import-") {
			t.Errorf("temporary media file remains: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func regularFilesUnder(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return files
	}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestMediaImportExactByteLimitAndFinalMode(t *testing.T) {
	app, config, _, recorder := newMediaImportTestApp(t)
	config.PDFMaxBytes = 64
	payload := mediaImportPDFPayload(64)

	relativePath, err := app.importPdfFromReader("report.pdf", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	absolutePath := filepath.Join(app.dataDir, filepath.FromSlash(relativePath))
	stored, err := os.ReadFile(absolutePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, payload) {
		t.Fatal("published PDF differs from the source")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(absolutePath)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o644 {
			t.Fatalf("published mode = %04o，want 0644", got)
		}
	}
	if recorder.eventCount() != 1 {
		t.Fatalf("events = %d，want 1", recorder.eventCount())
	}

	_, err = app.importPdfFromReader("too-large.pdf", bytes.NewReader(mediaImportPDFPayload(65)))
	if !errors.Is(err, errMediaImportTooLarge) {
		t.Fatalf("max+1 error = %v，want size limit", err)
	}
	if recorder.eventCount() != 1 {
		t.Fatalf("events after rejected import = %d，want 1", recorder.eventCount())
	}
	assertNoMediaImportTemps(t, app.dataDir)
}

func TestMediaImportRejectsExtensionAndSignatureMismatch(t *testing.T) {
	app, _, _, recorder := newMediaImportTestApp(t)
	tests := []struct {
		name     string
		filename string
		importer func(string, io.Reader) (string, error)
		data     []byte
	}{
		{name: "pdf signature", filename: "report.pdf", importer: app.importPdfFromReader, data: []byte("plain text")},
		{name: "audio signature", filename: "sample.mp3", importer: app.importAudioFromReader, data: []byte("plain text")},
		{name: "pdf extension", filename: "report.txt", importer: app.importPdfFromReader, data: []byte("%PDF-1.7")},
		{name: "audio extension", filename: "sample.ogg", importer: app.importAudioFromReader, data: []byte("OggS")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.importer(test.filename, bytes.NewReader(test.data)); err == nil {
				t.Fatal("mismatched import succeeded")
			}
		})
	}
	if recorder.eventCount() != 0 || recorder.transcriptionCount() != 0 {
		t.Fatalf("rejected imports emitted events=%d or ASR=%d", recorder.eventCount(), recorder.transcriptionCount())
	}
	assertNoMediaImportTemps(t, app.dataDir)
}

func TestMediaImportRejectsImageBombBeforeFullDecode(t *testing.T) {
	app, _, hooks, recorder := newMediaImportTestApp(t)
	hooks.decodeConfig = func(io.Reader) (image.Config, string, error) {
		return image.Config{Width: mediaImportMaxDimension, Height: mediaImportMaxDimension}, "png", nil
	}
	var decoded atomic.Bool
	hooks.decodeImage = func(io.Reader) (image.Image, string, error) {
		decoded.Store(true)
		return image.NewRGBA(image.Rect(0, 0, 1, 1)), "png", nil
	}

	_, err := app.importImageFromReader("bomb.png", strings.NewReader("header"))
	if err == nil || !strings.Contains(err.Error(), "pixel limit") {
		t.Fatalf("bomb error = %v，want pixel-limit rejection", err)
	}
	if decoded.Load() {
		t.Fatal("full image decode ran after DecodeConfig rejection")
	}
	if recorder.eventCount() != 0 {
		t.Fatal("rejected image emitted an event")
	}
	assertNoMediaImportTemps(t, app.dataDir)
}

func TestImportedImageDimensionAndDerivedSizeLimits(t *testing.T) {
	if err := validateImportedImageDimensions(16_384, 2_441); err != nil {
		t.Fatalf("valid hard-boundary dimensions failed: %v", err)
	}
	for _, dimensions := range [][2]int{{16_385, 1}, {16_384, 2_442}, {0, 10}} {
		if err := validateImportedImageDimensions(dimensions[0], dimensions[1]); err == nil {
			t.Fatalf("invalid dimensions %v succeeded", dimensions)
		}
	}

	for _, dimensions := range [][2]int{{3_000, 3_000}, {4_096, 4_096}, {8_000, 5_000}, {16_384, 2_441}} {
		width, height := importedImageTargetDimensions(dimensions[0], dimensions[1])
		if width > mediaImportTargetLongEdge || height > mediaImportTargetLongEdge {
			t.Fatalf("%v resized to %dx%d，long edge exceeds %d", dimensions, width, height, mediaImportTargetLongEdge)
		}
		if int64(width)*int64(height) > mediaImportTargetPixels {
			t.Fatalf("%v resized to %dx%d，pixels exceed %d", dimensions, width, height, mediaImportTargetPixels)
		}
		if dimensions == [2]int{3_000, 3_000} && (width != 3_000 || height != 3_000) {
			t.Fatalf("small image resized to %dx%d", width, height)
		}
	}
}

func TestMediaImportRejectsSourceSymlinkAndPathSwap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on some Windows hosts")
	}
	t.Run("final symlink", func(t *testing.T) {
		app, _, _, recorder := newMediaImportTestApp(t)
		sourceDirectory := t.TempDir()
		target := filepath.Join(sourceDirectory, "target.pdf")
		if err := os.WriteFile(target, mediaImportPDFPayload(32), 0o600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(sourceDirectory, "link.pdf")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := app.ImportPdfFile(link); err == nil {
			t.Fatal("symlink source import succeeded")
		}
		if recorder.eventCount() != 0 {
			t.Fatal("symlink source emitted an event")
		}
	})

	t.Run("swap after lstat", func(t *testing.T) {
		app, _, hooks, recorder := newMediaImportTestApp(t)
		source := filepath.Join(t.TempDir(), "report.pdf")
		if err := os.WriteFile(source, mediaImportPDFPayload(32), 0o600); err != nil {
			t.Fatal(err)
		}
		hooks.afterSourceLstat = func(root *os.Root, name string, _ os.FileInfo) error {
			if err := root.Rename(name, "inspected.pdf"); err != nil {
				return err
			}
			return root.WriteFile(name, mediaImportPDFPayload(33), 0o600)
		}
		_, err := app.ImportPdfFile(source)
		if err == nil || !strings.Contains(err.Error(), "source changed") {
			t.Fatalf("source swap error = %v，want SameFile mismatch", err)
		}
		if recorder.eventCount() != 0 {
			t.Fatal("swapped source emitted an event")
		}
	})
}

func TestMediaImportRejectsDestinationSymlinkAndPathSwap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on some Windows hosts")
	}
	t.Run("ancestor symlink", func(t *testing.T) {
		app, _, _, _ := newMediaImportTestApp(t)
		outside := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(app.dataDir, "content")); err != nil {
			t.Fatal(err)
		}
		if _, err := app.importPdfFromReader("report.pdf", bytes.NewReader(mediaImportPDFPayload(32))); err == nil {
			t.Fatal("symlink destination import succeeded")
		}
		if files := regularFilesUnder(t, outside); len(files) != 0 {
			t.Fatalf("files escaped into symlink destination: %v", files)
		}
	})

	t.Run("swap after open", func(t *testing.T) {
		app, _, hooks, _ := newMediaImportTestApp(t)
		hooks.afterDestinationOpen = func(root *os.Root, path string, _ *os.Root) error {
			if err := root.Rename(path, "content-inspected"); err != nil {
				return err
			}
			return root.Mkdir(path, 0o755)
		}
		_, err := app.importPdfFromReader("report.pdf", bytes.NewReader(mediaImportPDFPayload(32)))
		if err == nil || !strings.Contains(err.Error(), "destination changed") {
			t.Fatalf("destination swap error = %v，want SameFile mismatch", err)
		}
		if files := regularFilesUnder(t, app.dataDir); len(files) != 0 {
			t.Fatalf("destination swap published files: %v", files)
		}
	})
}

func TestMediaImportFaultsCleanTempsAndSuppressPostPublishActions(t *testing.T) {
	fault := errors.New("injected fault")
	tests := []struct {
		name      string
		configure func(*mediaImportHooks)
	}{
		{
			name: "zero short write",
			configure: func(hooks *mediaImportHooks) {
				hooks.write = func(*os.File, []byte) (int, error) { return 0, nil }
			},
		},
		{
			name: "ENOSPC write",
			configure: func(hooks *mediaImportHooks) {
				hooks.write = func(*os.File, []byte) (int, error) { return 0, syscall.ENOSPC }
			},
		},
		{
			name: "chmod",
			configure: func(hooks *mediaImportHooks) {
				hooks.chmod = func(*os.File, os.FileMode) error { return fault }
			},
		},
		{
			name: "file sync",
			configure: func(hooks *mediaImportHooks) {
				hooks.sync = func(*os.File) error { return fault }
			},
		},
		{
			name: "close",
			configure: func(hooks *mediaImportHooks) {
				defaultClose := hooks.close
				hooks.close = func(file *os.File) error {
					_ = defaultClose(file)
					return fault
				}
			},
		},
		{
			name: "publish link",
			configure: func(hooks *mediaImportHooks) {
				hooks.link = func(*os.Root, string, string) error { return fault }
			},
		},
		{
			name: "directory sync",
			configure: func(hooks *mediaImportHooks) {
				defaultSyncRoot := hooks.syncRoot
				var calls atomic.Int32
				hooks.syncRoot = func(root *os.Root) error {
					if calls.Add(1) == 1 {
						return fault
					}
					return defaultSyncRoot(root)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app, _, hooks, recorder := newMediaImportTestApp(t)
			test.configure(hooks)
			if _, err := app.importPdfFromReader("fault.pdf", bytes.NewReader(mediaImportPDFPayload(64))); err == nil {
				t.Fatal("faulted import succeeded")
			}
			if recorder.eventCount() != 0 || recorder.transcriptionCount() != 0 {
				t.Fatalf("fault emitted events=%d or ASR=%d", recorder.eventCount(), recorder.transcriptionCount())
			}
			if files := regularFilesUnder(t, filepath.Join(app.dataDir, "content")); len(files) != 0 {
				t.Fatalf("fault published files: %v", files)
			}
			assertNoMediaImportTemps(t, app.dataDir)
		})
	}
}

func TestMediaImageSecondLinkRollbackIsSynced(t *testing.T) {
	app, _, hooks, recorder := newMediaImportTestApp(t)
	defaultLink := hooks.link
	defaultSyncRoot := hooks.syncRoot
	var linkCalls atomic.Int32
	var syncCalls atomic.Int32
	hooks.encodeWebP = func(writer io.Writer, _ image.Image, _ bool) error {
		_, err := writer.Write([]byte("RIFF\x04\x00\x00\x00WEBPdata"))
		return err
	}
	hooks.link = func(root *os.Root, oldName, newName string) error {
		if linkCalls.Add(1) == 2 {
			return syscall.EIO
		}
		return defaultLink(root, oldName, newName)
	}
	hooks.syncRoot = func(root *os.Root) error {
		syncCalls.Add(1)
		return defaultSyncRoot(root)
	}

	_, err := app.importImageFromReader("photo.png", bytes.NewReader(mediaImportPNG(t, 2, 2)))
	if err == nil || !strings.Contains(err.Error(), "publish WebP") {
		t.Fatalf("second-link error = %v", err)
	}
	if syncCalls.Load() != 1 {
		t.Fatalf("rollback directory Sync calls = %d，want 1", syncCalls.Load())
	}
	if recorder.eventCount() != 0 {
		t.Fatal("rolled-back image emitted an event")
	}
	if files := regularFilesUnder(t, filepath.Join(app.dataDir, "data", "image")); len(files) != 0 {
		t.Fatalf("image rollback left files: %v", files)
	}
	assertNoMediaImportTemps(t, app.dataDir)
}

func TestMediaImageRollbackAggregatesRemoveAndSyncFaults(t *testing.T) {
	app, _, hooks, recorder := newMediaImportTestApp(t)
	defaultLink := hooks.link
	defaultRemove := hooks.remove
	removeFault := errors.New("remove rollback fault")
	syncFault := errors.New("sync rollback fault")
	var linkCalls atomic.Int32
	hooks.encodeWebP = func(writer io.Writer, _ image.Image, _ bool) error {
		_, err := writer.Write([]byte("RIFF\x04\x00\x00\x00WEBPdata"))
		return err
	}
	hooks.link = func(root *os.Root, oldName, newName string) error {
		if linkCalls.Add(1) == 2 {
			return syscall.EIO
		}
		return defaultLink(root, oldName, newName)
	}
	hooks.remove = func(root *os.Root, name string) error {
		if !strings.HasPrefix(name, ".karte-media-import-") {
			return removeFault
		}
		return defaultRemove(root, name)
	}
	hooks.syncRoot = func(*os.Root) error { return syncFault }

	_, err := app.importImageFromReader("photo.png", bytes.NewReader(mediaImportPNG(t, 1, 1)))
	if err == nil || !errors.Is(err, removeFault) || !errors.Is(err, syncFault) {
		t.Fatalf("aggregated rollback error = %v", err)
	}
	if recorder.eventCount() != 0 {
		t.Fatal("failed image rollback emitted an event")
	}
}

func TestMediaImportConcurrentNoReplacePublish(t *testing.T) {
	app, _, _, recorder := newMediaImportTestApp(t)
	const imports = 8
	payload := mediaImportPDFPayload(128)
	start := make(chan struct{})
	paths := make(chan string, imports)
	errorsFound := make(chan error, imports)
	var workers sync.WaitGroup
	for index := 0; index < imports; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			path, err := app.importPdfFromReader("same.pdf", bytes.NewReader(payload))
			if err != nil {
				errorsFound <- err
				return
			}
			paths <- path
		}()
	}
	close(start)
	workers.Wait()
	close(paths)
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("concurrent import: %v", err)
	}
	unique := make(map[string]struct{})
	for path := range paths {
		if _, exists := unique[path]; exists {
			t.Errorf("duplicate published path: %s", path)
		}
		unique[path] = struct{}{}
		stored, err := os.ReadFile(filepath.Join(app.dataDir, filepath.FromSlash(path)))
		if err != nil {
			t.Error(err)
		} else if !bytes.Equal(stored, payload) {
			t.Errorf("published content mismatch: %s", path)
		}
	}
	if len(unique) != imports {
		t.Fatalf("unique paths = %d，want %d", len(unique), imports)
	}
	if recorder.eventCount() != imports {
		t.Fatalf("events = %d，want %d", recorder.eventCount(), imports)
	}
	assertNoMediaImportTemps(t, app.dataDir)
}

func TestLegacyBase64ValidationAndDecodeAreBounded(t *testing.T) {
	validationValue := strings.Repeat("QUJD", 4_096)
	allocations := testing.AllocsPerRun(50, func() {
		if err := validateStrictBase64(validationValue, int64(len(validationValue))); err != nil {
			panic(err)
		}
	})
	if allocations != 0 {
		t.Fatalf("strict Base64 validation allocations = %.1f，want 0", allocations)
	}

	app, config, hooks, recorder := newMediaImportTestApp(t)
	config.LegacyMaxBytes = 2 * 1024 * 1024
	config.PDFMaxBytes = config.LegacyMaxBytes
	payload := mediaImportPDFPayload(1024 * 1024)
	encoded := base64.StdEncoding.EncodeToString(payload)
	defaultWrite := hooks.write
	var maxWrite atomic.Int64
	var writeCalls atomic.Int64
	hooks.write = func(file *os.File, data []byte) (int, error) {
		writeCalls.Add(1)
		for {
			current := maxWrite.Load()
			if int64(len(data)) <= current || maxWrite.CompareAndSwap(current, int64(len(data))) {
				break
			}
		}
		return defaultWrite(file, data)
	}

	if _, err := app.ImportPdfBase64("large.pdf", encoded); err != nil {
		t.Fatal(err)
	}
	if maxWrite.Load() > mediaImportCopyBufferBytes {
		t.Fatalf("legacy decode write = %d，want <= %d", maxWrite.Load(), mediaImportCopyBufferBytes)
	}
	if writeCalls.Load() < 2 {
		t.Fatalf("legacy decode write calls = %d，want streaming writes", writeCalls.Load())
	}
	if recorder.eventCount() != 1 {
		t.Fatalf("legacy events = %d，want 1", recorder.eventCount())
	}

	config.LegacyMaxBytes = int64(len(payload))
	tooLarge := base64.StdEncoding.EncodeToString(append(payload, 'x'))
	if _, err := app.ImportPdfBase64("too-large.pdf", tooLarge); !errors.Is(err, errMediaImportTooLarge) {
		t.Fatalf("legacy max+1 error = %v，want size limit", err)
	}
}

func TestMediaStreamingHeapDoesNotScaleWithFileSize(t *testing.T) {
	app, config, _, _ := newMediaImportTestApp(t)
	config.PDFMaxBytes = 20 * 1024 * 1024
	measure := func(name string, size int64) uint64 {
		t.Helper()
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		if _, err := app.importPdfFromReader(name, &generatedMediaPDFReader{size: size}); err != nil {
			t.Fatal(err)
		}
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		return after.TotalAlloc - before.TotalAlloc
	}

	smallAllocation := measure("small.pdf", 1024*1024)
	largeAllocation := measure("large.pdf", 16*1024*1024)
	const tolerance = uint64(1024 * 1024)
	if largeAllocation > smallAllocation+tolerance {
		t.Fatalf("streaming heap scaled with input: 1MiB=%d bytes，16MiB=%d bytes", smallAllocation, largeAllocation)
	}
}

func TestWebClipConversionUsesSharedImageBombGuard(t *testing.T) {
	app, _, hooks, recorder := newMediaImportTestApp(t)
	assetDirectory := filepath.Join(app.dataDir, "content", "clips", "assets", "guarded")
	if err := os.MkdirAll(assetDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(assetDirectory, "bomb.png")
	if err := os.WriteFile(source, []byte("header"), 0o600); err != nil {
		t.Fatal(err)
	}
	hooks.decodeConfig = func(io.Reader) (image.Config, string, error) {
		return image.Config{Width: mediaImportMaxDimension, Height: mediaImportMaxDimension}, "png", nil
	}
	var decoded atomic.Bool
	hooks.decodeImage = func(io.Reader) (image.Image, string, error) {
		decoded.Store(true)
		return image.NewRGBA(image.Rect(0, 0, 1, 1)), "png", nil
	}
	destination := filepath.Join(assetDirectory, "bomb.webp")

	err := app.convertImageFileToWebPContext(context.Background(), source, destination, ".png")
	if err == nil || !strings.Contains(err.Error(), "pixel limit") {
		t.Fatalf("Web Clip bomb error = %v，want pixel-limit rejection", err)
	}
	if decoded.Load() {
		t.Fatal("Web Clip bomb reached full decode")
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Web Clip bomb destination stat = %v", err)
	}
	if recorder.eventCount() != 0 {
		t.Fatal("Web Clip bomb emitted an import event")
	}
	assertNoMediaImportTemps(t, app.dataDir)
}

func TestWebClipConversionPublishesNoReplaceAndCleansTemp(t *testing.T) {
	app, _, hooks, _ := newMediaImportTestApp(t)
	assetDirectory := filepath.Join(app.dataDir, "content", "clips", "assets", "atomic")
	if err := os.MkdirAll(assetDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(assetDirectory, "photo.png")
	if err := os.WriteFile(source, mediaImportPNG(t, 2, 2), 0o600); err != nil {
		t.Fatal(err)
	}
	hooks.encodeWebP = func(writer io.Writer, _ image.Image, _ bool) error {
		_, err := writer.Write([]byte("RIFF\x04\x00\x00\x00WEBPnew"))
		return err
	}
	destination := filepath.Join(assetDirectory, "photo.webp")
	if err := app.convertImageFileToWebPContext(context.Background(), source, destination, ".png"); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.convertImageFileToWebPContext(context.Background(), source, destination, ".png"); !errors.Is(err, os.ErrExist) {
		t.Fatalf("second Web Clip publish error = %v，want no-replace collision", err)
	}
	second, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("existing Web Clip WebP was replaced")
	}
	assertNoMediaImportTemps(t, app.dataDir)
}
