package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const wavHeaderBytes = 44

var errIncrementalWAVClosed = errors.New("incremental WAV writer is closed")

type incrementalWAVFile interface {
	io.Writer
	io.Seeker
	Sync() error
	Close() error
	Name() string
}

type incrementalWAVHooks struct {
	createTemp func(string, string) (incrementalWAVFile, error)
	write      func(incrementalWAVFile, []byte) (int, error)
	seek       func(incrementalWAVFile, int64, int) (int64, error)
	sync       func(incrementalWAVFile) error
	close      func(incrementalWAVFile) error
	replace    func(string, string) error
	remove     func(string) error
}

func defaultIncrementalWAVHooks() incrementalWAVHooks {
	return incrementalWAVHooks{
		createTemp: func(directory, pattern string) (incrementalWAVFile, error) {
			return os.CreateTemp(directory, pattern)
		},
		write: func(file incrementalWAVFile, data []byte) (int, error) { return file.Write(data) },
		seek: func(file incrementalWAVFile, offset int64, whence int) (int64, error) {
			return file.Seek(offset, whence)
		},
		sync:    func(file incrementalWAVFile) error { return file.Sync() },
		close:   func(file incrementalWAVFile) error { return file.Close() },
		replace: atomicPublishRecordingFile,
		remove:  os.Remove,
	}
}

// IncrementalWAVWriter owns a same-directory temporary PCM16 WAV file．It is
// published only after the final RIFF sizes are patched and durable file data
// has been synced．
type IncrementalWAVWriter struct {
	mu sync.Mutex

	targetPath string
	tempPath   string
	sampleRate int
	file       incrementalWAVFile
	hooks      incrementalWAVHooks
	pcm        []byte
	samples    uint64
	closed     bool
	published  bool
}

func NewIncrementalWAVWriter(targetPath string, sampleRate int) (*IncrementalWAVWriter, error) {
	return newIncrementalWAVWriter(targetPath, sampleRate, defaultIncrementalWAVHooks())
}

func newIncrementalWAVWriter(targetPath string, sampleRate int, hooks incrementalWAVHooks) (*IncrementalWAVWriter, error) {
	if targetPath == "" || !filepath.IsAbs(targetPath) {
		return nil, fmt.Errorf("WAV target path must be absolute")
	}
	if sampleRate <= 0 {
		sampleRate = RecordingSampleRate
	}
	defaults := defaultIncrementalWAVHooks()
	if hooks.createTemp == nil {
		hooks.createTemp = defaults.createTemp
	}
	if hooks.write == nil {
		hooks.write = defaults.write
	}
	if hooks.seek == nil {
		hooks.seek = defaults.seek
	}
	if hooks.sync == nil {
		hooks.sync = defaults.sync
	}
	if hooks.close == nil {
		hooks.close = defaults.close
	}
	if hooks.replace == nil {
		hooks.replace = defaults.replace
	}
	if hooks.remove == nil {
		hooks.remove = defaults.remove
	}
	file, err := hooks.createTemp(filepath.Dir(targetPath), "."+filepath.Base(targetPath)+".tmp-*")
	if err != nil {
		return nil, fmt.Errorf("create incremental WAV temp file: %w", err)
	}
	w := &IncrementalWAVWriter{
		targetPath: targetPath,
		tempPath:   file.Name(),
		sampleRate: sampleRate,
		file:       file,
		hooks:      hooks,
		pcm:        make([]byte, MaxRecordingFrameSamples*2),
	}
	header := wavHeader(sampleRate, 0)
	if err := w.writeFull(header[:]); err != nil {
		closeErr := closeIncrementalWAVFile(file, hooks)
		removeErr := hooks.remove(file.Name())
		return nil, errors.Join(fmt.Errorf("write incremental WAV header: %w", err), closeErr, removeErr)
	}
	return w, nil
}

func (writer *IncrementalWAVWriter) WriteSamples(samples []float32) error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.closed || writer.file == nil {
		return errIncrementalWAVClosed
	}
	if len(samples) == 0 {
		return nil
	}
	if writer.samples+uint64(len(samples)) > uint64((^uint32(0)-36)/2) {
		return fmt.Errorf("incremental WAV exceeds RIFF size limit")
	}
	sampleCount := len(samples)
	for len(samples) > 0 {
		chunkSamples := len(writer.pcm) / 2
		if len(samples) < chunkSamples {
			chunkSamples = len(samples)
		}
		pcm := writer.pcm[:chunkSamples*2]
		for index, sample := range samples[:chunkSamples] {
			if sample > 1 {
				sample = 1
			} else if sample < -1 {
				sample = -1
			}
			binary.LittleEndian.PutUint16(pcm[index*2:], uint16(int16(sample*32767)))
		}
		if err := writer.writeFull(pcm); err != nil {
			return fmt.Errorf("write incremental WAV samples: %w", err)
		}
		samples = samples[chunkSamples:]
	}
	writer.samples += uint64(sampleCount)
	return nil
}

func (writer *IncrementalWAVWriter) writeFull(data []byte) error {
	for len(data) > 0 {
		written, err := writer.hooks.write(writer.file, data)
		if err != nil {
			return err
		}
		if written <= 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

func (writer *IncrementalWAVWriter) Finalize() (string, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.closed || writer.file == nil {
		if writer.published {
			return writer.targetPath, nil
		}
		return "", errIncrementalWAVClosed
	}
	header := wavHeader(writer.sampleRate, uint32(writer.samples*2))
	if _, err := writer.hooks.seek(writer.file, 0, io.SeekStart); err != nil {
		return "", writer.failLocked("seek incremental WAV header", err)
	}
	if err := writer.writeFull(header[:]); err != nil {
		return "", writer.failLocked("patch incremental WAV header", err)
	}
	if err := writer.hooks.sync(writer.file); err != nil {
		return "", writer.failLocked("sync incremental WAV", err)
	}
	if err := closeIncrementalWAVFile(writer.file, writer.hooks); err != nil {
		writer.file = nil
		writer.closed = true
		removeErr := writer.hooks.remove(writer.tempPath)
		return "", errors.Join(fmt.Errorf("close incremental WAV: %w", err), removeErr)
	}
	writer.file = nil
	writer.closed = true
	if err := writer.hooks.replace(writer.tempPath, writer.targetPath); err != nil {
		removeErr := writer.hooks.remove(writer.tempPath)
		return "", errors.Join(fmt.Errorf("publish incremental WAV atomically: %w", err), removeErr)
	}
	writer.published = true
	return writer.targetPath, nil
}

func (writer *IncrementalWAVWriter) failLocked(operation string, cause error) error {
	var closeErr error
	if writer.file != nil {
		closeErr = closeIncrementalWAVFile(writer.file, writer.hooks)
		writer.file = nil
	}
	writer.closed = true
	removeErr := writer.hooks.remove(writer.tempPath)
	if errors.Is(removeErr, os.ErrNotExist) {
		removeErr = nil
	}
	return errors.Join(fmt.Errorf("%s: %w", operation, cause), closeErr, removeErr)
}

func (writer *IncrementalWAVWriter) Abort() error {
	if writer == nil {
		return nil
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.published {
		return nil
	}
	var closeErr error
	if writer.file != nil {
		closeErr = closeIncrementalWAVFile(writer.file, writer.hooks)
		writer.file = nil
	}
	writer.closed = true
	removeErr := writer.hooks.remove(writer.tempPath)
	if errors.Is(removeErr, os.ErrNotExist) {
		removeErr = nil
	}
	return errors.Join(closeErr, removeErr)
}

func closeIncrementalWAVFile(file incrementalWAVFile, hooks incrementalWAVHooks) error {
	if file == nil {
		return nil
	}
	closeErr := hooks.close(file)
	if closeErr == nil {
		return nil
	}
	return errors.Join(closeErr, file.Close())
}

func (writer *IncrementalWAVWriter) SampleCount() uint64 {
	if writer == nil {
		return 0
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.samples
}

func (writer *IncrementalWAVWriter) TargetPath() string {
	if writer == nil {
		return ""
	}
	return writer.targetPath
}

func (writer *IncrementalWAVWriter) TempPath() string {
	if writer == nil {
		return ""
	}
	return writer.tempPath
}

func wavHeader(sampleRate int, dataBytes uint32) [wavHeaderBytes]byte {
	var header [wavHeaderBytes]byte
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], 36+dataBytes)
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)
	binary.LittleEndian.PutUint16(header[22:24], 1)
	binary.LittleEndian.PutUint32(header[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(header[28:32], uint32(sampleRate*2))
	binary.LittleEndian.PutUint16(header[32:34], 2)
	binary.LittleEndian.PutUint16(header[34:36], 16)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], dataBytes)
	return header
}
