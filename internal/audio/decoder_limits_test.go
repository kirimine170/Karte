package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"strings"
	"testing"
)

type fixedAudioChunkReader struct {
	reader *bytes.Reader
	size   int
	reads  int
	hook   func(int)
}

func (reader *fixedAudioChunkReader) Read(data []byte) (int, error) {
	if len(data) > reader.size {
		data = data[:reader.size]
	}
	read, err := reader.reader.Read(data)
	reader.reads++
	if reader.hook != nil {
		reader.hook(reader.reads)
	}
	return read, err
}

type zeroAudioWriter struct{}

func (zeroAudioWriter) Write([]byte) (int, error) {
	return 0, nil
}

func float32PCM(values ...float32) []byte {
	data := make([]byte, len(values)*4)
	for index, value := range values {
		binary.LittleEndian.PutUint32(data[index*4:], math.Float32bits(value))
	}
	return data
}

func TestReadFloat32PCMLimitedBoundariesAndChunking(t *testing.T) {
	data := float32PCM(0.25, -0.5, 1)
	reader := &fixedAudioChunkReader{reader: bytes.NewReader(data), size: 3}
	samples, err := readFloat32PCMLimited(context.Background(), reader, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(samples) != 3 || samples[0] != 0.25 || samples[1] != -0.5 || samples[2] != 1 {
		t.Fatalf("decoded samples = %v", samples)
	}

	tooLarge := append(append([]byte(nil), data...), 0)
	if _, err := readFloat32PCMLimited(context.Background(), bytes.NewReader(tooLarge), int64(len(data))); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("max+1 PCM error = %v", err)
	}
	if _, err := readFloat32PCMLimited(context.Background(), bytes.NewReader(data[:3]), 4); err == nil || !strings.Contains(err.Error(), "buffer size") {
		t.Fatalf("unaligned PCM error = %v", err)
	}
}

func TestReadFloat32PCMLimitedCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader := &fixedAudioChunkReader{
		reader: bytes.NewReader(float32PCM(1, 2, 3)),
		size:   4,
		hook: func(reads int) {
			if reads == 1 {
				cancel()
			}
		},
	}
	if _, err := readFloat32PCMLimited(ctx, reader, 64); !errors.Is(err, context.Canceled) {
		t.Fatalf("PCM cancellation error = %v", err)
	}
}

func TestCopyPCM16LimitedBoundariesCancellationAndShortWrite(t *testing.T) {
	data := bytes.Repeat([]byte{0x5a}, 17)
	var destination bytes.Buffer
	written, err := copyPCM16Limited(context.Background(), &destination, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if written != int64(len(data)) || !bytes.Equal(destination.Bytes(), data) {
		t.Fatalf("copied bytes = %d data=%x", written, destination.Bytes())
	}

	if _, err := copyPCM16Limited(context.Background(), io.Discard, bytes.NewReader(append(data, 0)), int64(len(data))); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("max+1 PCM16 error = %v", err)
	}
	if _, err := copyPCM16Limited(context.Background(), zeroAudioWriter{}, bytes.NewReader(data), int64(len(data))); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short-write error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := copyPCM16Limited(ctx, io.Discard, bytes.NewReader(data), int64(len(data))); !errors.Is(err, context.Canceled) {
		t.Fatalf("copy cancellation error = %v", err)
	}
}

func TestWritePCM16WAVHeaderProducesStreamableBoundedWAV(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "decoder-limit-*.wav")
	if err != nil {
		t.Fatal(err)
	}
	path := file.Name()
	data := []byte{0x00, 0x00, 0xff, 0x7f, 0x00, 0x80}
	if err := writePCM16WAVHeader(file, 16_000, int64(len(data))); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	header, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(header[:4]) != "RIFF" || string(header[8:12]) != "WAVE" || string(header[36:40]) != "data" {
		t.Fatalf("invalid WAV signature: %q", header[:44])
	}
	if got := binary.LittleEndian.Uint32(header[4:8]); got != uint32(36+len(data)) {
		t.Fatalf("RIFF size = %d", got)
	}
	if got := binary.LittleEndian.Uint32(header[40:44]); got != uint32(len(data)) {
		t.Fatalf("data size = %d", got)
	}

	var samples []float32
	if err := StreamWavChunks(path, 2, func(sampleRate int, chunk []float32) error {
		if sampleRate != 16_000 {
			t.Fatalf("sample rate = %d", sampleRate)
		}
		samples = append(samples, chunk...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(samples) != 3 {
		t.Fatalf("streamed samples = %d", len(samples))
	}
}

func TestCappedAudioBufferBoundsFFmpegDiagnostics(t *testing.T) {
	buffer := newCappedAudioBuffer(8)
	data := []byte("0123456789abcdef")
	written, err := buffer.Write(data)
	if err != nil {
		t.Fatal(err)
	}
	if written != len(data) || buffer.String() != "01234567" {
		t.Fatalf("capped diagnostic write = %d %q", written, buffer.String())
	}
}
