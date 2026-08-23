package audio

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestIncrementalWAVWriterPublishesExactHeaderAndSamples(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "録音.wav")
	writer, err := NewIncrementalWAVWriter(target, 16_000)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(writer.TempPath()) != directory {
		t.Fatalf("temp file directory = %q，want %q", filepath.Dir(writer.TempPath()), directory)
	}
	if err := writer.WriteSamples([]float32{0, 1, -1, 0.5}); err != nil {
		t.Fatal(err)
	}
	published, err := writer.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	if published != target || writer.SampleCount() != 4 {
		t.Fatalf("published=%q samples=%d", published, writer.SampleCount())
	}
	if _, err := os.Lstat(writer.TempPath()); !os.IsNotExist(err) {
		t.Fatalf("temporary file remained after publish: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != wavHeaderBytes+8 {
		t.Fatalf("WAV bytes = %d，want %d", len(data), wavHeaderBytes+8)
	}
	if string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" || string(data[36:40]) != "data" {
		t.Fatalf("invalid WAV chunk identifiers: %q", data[:44])
	}
	if got := binary.LittleEndian.Uint32(data[4:8]); got != 44 {
		t.Fatalf("RIFF size = %d，want 44", got)
	}
	if got := binary.LittleEndian.Uint32(data[24:28]); got != 16_000 {
		t.Fatalf("sample rate = %d，want 16000", got)
	}
	if got := binary.LittleEndian.Uint32(data[40:44]); got != 8 {
		t.Fatalf("data size = %d，want 8", got)
	}
	wantPCM := []byte{0, 0, 0xff, 0x7f, 0x01, 0x80, 0xff, 0x3f}
	if !reflect.DeepEqual(data[44:], wantPCM) {
		t.Fatalf("PCM bytes = %v，want %v", data[44:], wantPCM)
	}
}

func TestIncrementalWAVWriterFinalizeOrderAndIdempotence(t *testing.T) {
	target := filepath.Join(t.TempDir(), "ordered.wav")
	hooks := defaultIncrementalWAVHooks()
	var operations []string
	defaultWrite := hooks.write
	hooks.write = func(file incrementalWAVFile, data []byte) (int, error) {
		operations = append(operations, "write")
		return defaultWrite(file, data)
	}
	defaultSeek := hooks.seek
	hooks.seek = func(file incrementalWAVFile, offset int64, whence int) (int64, error) {
		operations = append(operations, "seek")
		return defaultSeek(file, offset, whence)
	}
	defaultSync := hooks.sync
	hooks.sync = func(file incrementalWAVFile) error {
		operations = append(operations, "sync")
		return defaultSync(file)
	}
	defaultClose := hooks.close
	hooks.close = func(file incrementalWAVFile) error {
		operations = append(operations, "close")
		return defaultClose(file)
	}
	defaultReplace := hooks.replace
	hooks.replace = func(source, destination string) error {
		operations = append(operations, "publish")
		return defaultReplace(source, destination)
	}
	writer, err := newIncrementalWAVWriter(target, 16_000, hooks)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteSamples([]float32{0.25}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Finalize(); err != nil {
		t.Fatal(err)
	}
	wantSuffix := []string{"seek", "write", "sync", "close", "publish"}
	if got := operations[len(operations)-len(wantSuffix):]; !reflect.DeepEqual(got, wantSuffix) {
		t.Fatalf("finalize order = %v，want suffix %v", operations, wantSuffix)
	}
	if second, err := writer.Finalize(); err != nil || second != target {
		t.Fatalf("second Finalize = %q，%v", second, err)
	}
	if err := writer.Abort(); err != nil {
		t.Fatalf("Abort after publish failed: %v", err)
	}
}

func TestIncrementalWAVWriterNeverReplacesCollision(t *testing.T) {
	target := filepath.Join(t.TempDir(), "same-second.wav")
	writer, err := NewIncrementalWAVWriter(target, 16_000)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteSamples([]float32{0.5}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("existing recording"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Finalize(); err == nil {
		t.Fatal("Finalize replaced an existing target")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "existing recording" {
		t.Fatalf("existing recording changed: %q", data)
	}
	if _, err := os.Lstat(writer.TempPath()); !os.IsNotExist(err) {
		t.Fatalf("temp file remained after collision: %v", err)
	}
}

func TestIncrementalWAVWriterFaultsRemoveIncompleteTempAndPreserveTarget(t *testing.T) {
	wantErr := errors.New("injected WAV fault")
	tests := []struct {
		name      string
		configure func(*incrementalWAVHooks)
		writeErr  bool
	}{
		{
			name: "sample write",
			configure: func(hooks *incrementalWAVHooks) {
				defaultWrite := hooks.write
				calls := 0
				hooks.write = func(file incrementalWAVFile, data []byte) (int, error) {
					calls++
					if calls == 2 {
						return 0, wantErr
					}
					return defaultWrite(file, data)
				}
			},
			writeErr: true,
		},
		{
			name: "header seek",
			configure: func(hooks *incrementalWAVHooks) {
				hooks.seek = func(incrementalWAVFile, int64, int) (int64, error) { return 0, wantErr }
			},
		},
		{
			name: "sync",
			configure: func(hooks *incrementalWAVHooks) {
				hooks.sync = func(incrementalWAVFile) error { return wantErr }
			},
		},
		{
			name: "close",
			configure: func(hooks *incrementalWAVHooks) {
				hooks.close = func(file incrementalWAVFile) error {
					_ = file.Close()
					return wantErr
				}
			},
		},
		{
			name: "publish",
			configure: func(hooks *incrementalWAVHooks) {
				hooks.replace = func(string, string) error { return wantErr }
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			target := filepath.Join(directory, "recording.wav")
			hooks := defaultIncrementalWAVHooks()
			test.configure(&hooks)
			writer, err := newIncrementalWAVWriter(target, 16_000, hooks)
			if err != nil {
				t.Fatal(err)
			}
			if test.writeErr {
				err = writer.WriteSamples([]float32{0.25})
				if !errors.Is(err, wantErr) {
					t.Fatalf("WriteSamples error = %v，want injected fault", err)
				}
				if abortErr := writer.Abort(); abortErr != nil {
					t.Fatalf("Abort after write error: %v", abortErr)
				}
			} else {
				if err := writer.WriteSamples([]float32{0.25}); err != nil {
					t.Fatal(err)
				}
				_, err = writer.Finalize()
				if !errors.Is(err, wantErr) {
					t.Fatalf("Finalize error = %v，want injected fault", err)
				}
			}
			if _, err := os.Lstat(writer.TempPath()); !os.IsNotExist(err) {
				t.Fatalf("incomplete temp remained: %v", err)
			}
			if _, err := os.Lstat(target); !os.IsNotExist(err) {
				t.Fatalf("incomplete target was published: %v", err)
			}
		})
	}
}

func TestIncrementalWAVWriterRejectsRelativePathAndRIFFOverflow(t *testing.T) {
	if _, err := NewIncrementalWAVWriter("relative.wav", 16_000); err == nil {
		t.Fatal("relative target was accepted")
	}
	writer, err := NewIncrementalWAVWriter(filepath.Join(t.TempDir(), "overflow.wav"), 16_000)
	if err != nil {
		t.Fatal(err)
	}
	writer.samples = uint64((^uint32(0) - 36) / 2)
	if err := writer.WriteSamples([]float32{0}); err == nil || !strings.Contains(err.Error(), "RIFF size") {
		t.Fatalf("overflow error = %v", err)
	}
	if err := writer.Abort(); err != nil {
		t.Fatal(err)
	}
}

func TestIncrementalWAVWriterChunksVariableInputWithoutGrowingScratch(t *testing.T) {
	target := filepath.Join(t.TempDir(), "chunked.wav")
	writer, err := NewIncrementalWAVWriter(target, 16_000)
	if err != nil {
		t.Fatal(err)
	}
	initialLength := len(writer.pcm)
	initialCapacity := cap(writer.pcm)
	if err := writer.WriteSamples(make([]float32, RecordingFrameSize*17+3)); err != nil {
		t.Fatal(err)
	}
	if len(writer.pcm) != initialLength || cap(writer.pcm) != initialCapacity {
		t.Fatalf("PCM scratch grew from len/cap %d/%d to %d/%d", initialLength, initialCapacity, len(writer.pcm), cap(writer.pcm))
	}
	if writer.SampleCount() != RecordingFrameSize*17+3 {
		t.Fatalf("sample count = %d", writer.SampleCount())
	}
	if _, err := writer.Finalize(); err != nil {
		t.Fatal(err)
	}
}

func TestIncrementalWAVHeaderHelperMatchesCanonicalLayout(t *testing.T) {
	header := wavHeader(8_000, 320)
	if got := binary.LittleEndian.Uint32(header[4:8]); got != 356 {
		t.Fatalf("RIFF size = %d", got)
	}
	if got := binary.LittleEndian.Uint32(header[28:32]); got != 16_000 {
		t.Fatalf("byte rate = %d", got)
	}
	if _, err := io.ReadFull(strings.NewReader(string(header[:])), make([]byte, wavHeaderBytes)); err != nil {
		t.Fatal(err)
	}
}
