package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
)

const (
	// DefaultSampleRate is the PCM rate the ASR pipeline expects.
	DefaultSampleRate = 16000
	// MaxDecodedPCMBytes bounds both in-memory float PCM and staged PCM16 data.
	MaxDecodedPCMBytes  = int64(256 * 1024 * 1024)
	pcmCopyBufferBytes  = 128 * 1024
	maxFFmpegErrorBytes = 64 * 1024
)

var (
	supportedImportExt = map[string]struct{}{
		".wav": {},
		".mp3": {},
		".m4a": {},
	}

	filenameCleaner = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)
)

// ListSupportedImportExt returns the extensions accepted by the audio import UI.
func ListSupportedImportExt() []string {
	out := make([]string, 0, len(supportedImportExt))
	for ext := range supportedImportExt {
		out = append(out, ext)
	}
	return out
}

// IsSupportedImportExt reports whether the provided filename has an allowed extension.
func IsSupportedImportExt(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	_, ok := supportedImportExt[ext]
	return ok
}

// SanitizeFileName turns an arbitrary string into a filesystem-friendly slug.
func SanitizeFileName(name string) string {
	slug := filenameCleaner.ReplaceAllString(strings.ToLower(name), "-")
	slug = strings.Trim(slug, "-_")
	if slug == "" {
		return "audio"
	}
	return slug
}

// DecodeToPCM converts the given audio file to mono float32 PCM using ffmpeg.
// It returns the sample rate actually used along with the samples.
func DecodeToPCM(ctx context.Context, path string, sampleRate int) (int, []float32, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if sampleRate <= 0 {
		sampleRate = DefaultSampleRate
	}

	ffmpegPath, err := findFFmpeg()
	if err != nil {
		return 0, nil, err
	}

	cmd := exec.CommandContext(ctx, ffmpegPath,
		"-i", path,
		"-vn",
		"-f", "f32le",
		"-acodec", "pcm_f32le",
		"-ac", "1",
		"-ar", fmt.Sprintf("%d", sampleRate),
		"-",
	)

	stderr := newCappedAudioBuffer(maxFFmpegErrorBytes)
	cmd.Stderr = &stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, nil, fmt.Errorf("ffmpeg stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return 0, nil, fmt.Errorf("ffmpeg start: %w (stderr: %s)", err, stderr.String())
	}

	samples, err := readFloat32PCMLimited(ctx, stdout, MaxDecodedPCMBytes)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, nil, ctxErr
		}
		return 0, nil, fmt.Errorf("ffmpeg PCM read: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return 0, nil, ctxErr
		}
		return 0, nil, fmt.Errorf("ffmpeg failed: %w (stderr: %s)", err, stderr.String())
	}

	return sampleRate, samples, nil
}

// ConvertToPCM16Wav converts the given audio file into a temporary 16-bit mono WAV file.
// Caller must invoke the returned cleanup function.
func ConvertToPCM16Wav(ctx context.Context, srcPath string, sampleRate int) (string, func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if sampleRate <= 0 {
		sampleRate = DefaultSampleRate
	}
	if sampleRate > 384000 {
		return "", nil, fmt.Errorf("sample rate %d exceeds the supported limit", sampleRate)
	}

	ffmpegPath, err := findFFmpeg()
	if err != nil {
		return "", nil, err
	}

	tmpFile, err := os.CreateTemp("", "karte-pcm-*.wav")
	if err != nil {
		return "", nil, fmt.Errorf("create temp wav: %w", err)
	}
	tmpPath := tmpFile.Name()
	succeeded := false
	defer func() {
		if !succeeded {
			_ = tmpFile.Close()
			_ = os.Remove(tmpPath)
		}
	}()
	if err := writePCM16WAVHeader(tmpFile, sampleRate, 0); err != nil {
		return "", nil, fmt.Errorf("write temporary WAV header: %w", err)
	}

	cmd := exec.CommandContext(ctx, ffmpegPath,
		"-i", srcPath,
		"-vn",
		"-ac", "1",
		"-ar", fmt.Sprintf("%d", sampleRate),
		"-acodec", "pcm_s16le",
		"-f", "s16le",
		"-",
	)

	stderr := newCappedAudioBuffer(maxFFmpegErrorBytes)
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", nil, fmt.Errorf("ffmpeg PCM16 stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return "", nil, fmt.Errorf("ffmpeg PCM16 start: %w (stderr: %s)", err, stderr.String())
	}
	pcmBytes, copyErr := copyPCM16Limited(ctx, tmpFile, stdout, MaxDecodedPCMBytes)
	if copyErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", nil, ctxErr
		}
		return "", nil, fmt.Errorf("stream decoded PCM16: %w", copyErr)
	}
	if err := cmd.Wait(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", nil, ctxErr
		}
		return "", nil, fmt.Errorf("ffmpeg wav convert failed: %w (stderr: %s)", err, stderr.String())
	}
	if pcmBytes%2 != 0 {
		return "", nil, fmt.Errorf("unexpected PCM16 byte count: %d", pcmBytes)
	}
	if err := writePCM16WAVHeader(tmpFile, sampleRate, pcmBytes); err != nil {
		return "", nil, fmt.Errorf("finalize temporary WAV header: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		return "", nil, fmt.Errorf("sync temporary WAV: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return "", nil, fmt.Errorf("close temporary WAV: %w", err)
	}

	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() { _ = os.Remove(tmpPath) })
	}
	succeeded = true
	return tmpPath, cleanup, nil
}

type cappedAudioBuffer struct {
	remaining int
	buffer    bytes.Buffer
}

func newCappedAudioBuffer(limit int) cappedAudioBuffer {
	return cappedAudioBuffer{remaining: limit}
}

func (buffer *cappedAudioBuffer) Write(data []byte) (int, error) {
	originalLength := len(data)
	if len(data) > buffer.remaining {
		data = data[:buffer.remaining]
	}
	if len(data) > 0 {
		_, _ = buffer.buffer.Write(data)
		buffer.remaining -= len(data)
	}
	return originalLength, nil
}

func (buffer *cappedAudioBuffer) String() string {
	return buffer.buffer.String()
}

func readFloat32PCMLimited(ctx context.Context, reader io.Reader, limit int64) ([]float32, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if reader == nil || limit <= 0 {
		return nil, errors.New("invalid PCM reader or limit")
	}
	raw := make([]byte, pcmCopyBufferBytes+3)
	samples := make([]float32, 0, minInt(int(limit/4), DefaultSampleRate*60))
	var total int64
	pending := 0
	emptyReads := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		read, readErr := reader.Read(raw[pending:])
		if read > 0 {
			emptyReads = 0
			total += int64(read)
			if total > limit {
				return nil, fmt.Errorf("decoded PCM exceeds %d bytes", limit)
			}
			available := pending + read
			complete := available - available%4
			for offset := 0; offset < complete; offset += 4 {
				bits := binary.LittleEndian.Uint32(raw[offset : offset+4])
				samples = append(samples, math.Float32frombits(bits))
			}
			pending = available - complete
			copy(raw[:pending], raw[complete:available])
		} else if readErr == nil {
			emptyReads++
			if emptyReads >= 100 {
				return nil, io.ErrNoProgress
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return nil, readErr
			}
			if pending != 0 {
				return nil, fmt.Errorf("unexpected PCM buffer size: %d", total)
			}
			return samples, nil
		}
	}
}

func copyPCM16Limited(ctx context.Context, writer io.Writer, reader io.Reader, limit int64) (int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if writer == nil || reader == nil || limit <= 0 {
		return 0, errors.New("invalid PCM copy arguments")
	}
	buffer := make([]byte, pcmCopyBufferBytes)
	var writtenTotal int64
	emptyReads := 0
	for {
		if err := ctx.Err(); err != nil {
			return writtenTotal, err
		}
		remaining := limit - writtenTotal
		readBuffer := buffer
		if remaining < int64(len(readBuffer)) {
			readBuffer = readBuffer[:remaining+1]
		}
		read, readErr := reader.Read(readBuffer)
		if read > 0 {
			emptyReads = 0
			if writtenTotal+int64(read) > limit {
				return writtenTotal, fmt.Errorf("decoded PCM exceeds %d bytes", limit)
			}
			written, writeErr := writeAudioFull(writer, readBuffer[:read])
			writtenTotal += int64(written)
			if writeErr != nil {
				return writtenTotal, writeErr
			}
		} else if readErr == nil {
			emptyReads++
			if emptyReads >= 100 {
				return writtenTotal, io.ErrNoProgress
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return writtenTotal, nil
			}
			return writtenTotal, readErr
		}
	}
}

func writeAudioFull(writer io.Writer, data []byte) (int, error) {
	writtenTotal := 0
	for len(data) > 0 {
		written, err := writer.Write(data)
		if written < 0 || written > len(data) {
			return writtenTotal, errors.New("invalid PCM write count")
		}
		writtenTotal += written
		data = data[written:]
		if err != nil {
			return writtenTotal, err
		}
		if written == 0 {
			return writtenTotal, io.ErrShortWrite
		}
	}
	return writtenTotal, nil
}

func writePCM16WAVHeader(file *os.File, sampleRate int, dataBytes int64) error {
	if file == nil || sampleRate <= 0 || dataBytes < 0 || dataBytes > math.MaxUint32-36 {
		return errors.New("invalid WAV header values")
	}
	var header [44]byte
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], uint32(36+dataBytes))
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
	binary.LittleEndian.PutUint32(header[40:44], uint32(dataBytes))
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	_, err := writeAudioFull(file, header[:])
	return err
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

// StreamWavChunks reads a PCM WAV file and feeds samples chunk by chunk to handler.
// chunkSamples specifies how many samples per callback; if <=0, a 0.5s chunk is used.
func StreamWavChunks(wavPath string, chunkSamples int, handler func(sampleRate int, chunk []float32) error) error {
	file, err := os.Open(wavPath)
	if err != nil {
		return fmt.Errorf("open wav: %w", err)
	}
	defer file.Close()

	// Parse RIFF header
	var riffHeader [12]byte
	if _, err := io.ReadFull(file, riffHeader[:]); err != nil {
		return fmt.Errorf("read RIFF header: %w", err)
	}
	if string(riffHeader[0:4]) != "RIFF" || string(riffHeader[8:12]) != "WAVE" {
		return fmt.Errorf("not a RIFF/WAVE file")
	}

	var (
		sampleRate    uint32
		numChannels   uint16
		bitsPerSample uint16
		dataSize      uint32
		dataOffset    int64
	)

	for {
		var chunkID [4]byte
		var chunkSize uint32
		if _, err := io.ReadFull(file, chunkID[:]); err != nil {
			return fmt.Errorf("read chunk id: %w", err)
		}
		if err := binary.Read(file, binary.LittleEndian, &chunkSize); err != nil {
			return fmt.Errorf("read chunk size: %w", err)
		}
		chunkStart, _ := file.Seek(0, io.SeekCurrent)

		switch string(chunkID[:]) {
		case "fmt ":
			var audioFormat uint16
			if err := binary.Read(file, binary.LittleEndian, &audioFormat); err != nil {
				return fmt.Errorf("read audio format: %w", err)
			}
			if err := binary.Read(file, binary.LittleEndian, &numChannels); err != nil {
				return fmt.Errorf("read channels: %w", err)
			}
			if err := binary.Read(file, binary.LittleEndian, &sampleRate); err != nil {
				return fmt.Errorf("read sample rate: %w", err)
			}
			var byteRate uint32
			if err := binary.Read(file, binary.LittleEndian, &byteRate); err != nil {
				return fmt.Errorf("read byte rate: %w", err)
			}
			var blockAlign uint16
			if err := binary.Read(file, binary.LittleEndian, &blockAlign); err != nil {
				return fmt.Errorf("read block align: %w", err)
			}
			if err := binary.Read(file, binary.LittleEndian, &bitsPerSample); err != nil {
				return fmt.Errorf("read bits per sample: %w", err)
			}
			if audioFormat != 1 {
				return fmt.Errorf("unsupported audio format: %d", audioFormat)
			}
		case "data":
			dataSize = chunkSize
			dataOffset = chunkStart
		default:
			// skip unknown chunk
		}

		if _, err := file.Seek(chunkStart+int64(chunkSize), io.SeekStart); err != nil {
			return fmt.Errorf("seek next chunk: %w", err)
		}

		if dataSize > 0 && sampleRate > 0 && bitsPerSample > 0 {
			break
		}
	}

	if dataSize == 0 || sampleRate == 0 || bitsPerSample == 0 {
		return fmt.Errorf("invalid wav header (missing data/fmt)")
	}

	if _, err := file.Seek(dataOffset, io.SeekStart); err != nil {
		return fmt.Errorf("seek data chunk: %w", err)
	}

	if chunkSamples <= 0 {
		chunkSamples = int(sampleRate) / 2
		if chunkSamples <= 0 {
			chunkSamples = int(sampleRate)
		}
	}

	bytesPerSample := int(bitsPerSample / 8)
	frameSize := bytesPerSample * int(numChannels)
	rawBuf := make([]byte, chunkSamples*frameSize)
	floatBuf := make([]float32, chunkSamples)

	reader := io.LimitReader(file, int64(dataSize))
	for {
		n, err := reader.Read(rawBuf)
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read data chunk: %w", err)
		}
		if n == 0 {
			break
		}

		sampleCount := n / frameSize
		if sampleCount == 0 {
			continue
		}

		if numChannels == 1 {
			for i := 0; i < sampleCount; i++ {
				offset := i * bytesPerSample
				sample := int16(binary.LittleEndian.Uint16(rawBuf[offset : offset+bytesPerSample]))
				floatBuf[i] = float32(sample) / 32768.0
			}
		} else {
			step := frameSize
			for i := 0; i < sampleCount; i++ {
				var sum float32
				for ch := 0; ch < int(numChannels); ch++ {
					offset := i*step + ch*bytesPerSample
					sample := int16(binary.LittleEndian.Uint16(rawBuf[offset : offset+bytesPerSample]))
					sum += float32(sample) / 32768.0
				}
				floatBuf[i] = sum / float32(numChannels)
			}
		}

		if err := handler(int(sampleRate), floatBuf[:sampleCount]); err != nil {
			return err
		}
	}

	return nil
}

// findFFmpeg tries to locate the packaged ffmpeg, PATH, and common install
// locations. Explicit overrides remain available for development and support.
func findFFmpeg() (string, error) {
	executable, _ := os.Executable()
	return findFFmpegForOS(runtime.GOOS, executable, os.Getenv, exec.LookPath, regularFileExists)
}

func findFFmpegForOS(
	goos string,
	executable string,
	getenv func(string) string,
	lookPath func(string) (string, error),
	fileExists func(string) bool,
) (string, error) {
	for _, envName := range []string{"KARTE_FFMPEG_BINARY", "FFMPEG_PATH"} {
		if custom := strings.TrimSpace(getenv(envName)); custom != "" && fileExists(custom) {
			return custom, nil
		}
	}

	if executable != "" {
		exeDir := filepath.Dir(executable)
		binaryName := "ffmpeg"
		if goos == "windows" {
			binaryName = "ffmpeg.exe"
		}
		for _, candidate := range []string{
			filepath.Join(exeDir, binaryName),
			filepath.Join(exeDir, "bin", binaryName),
			filepath.Join(exeDir, "runtime", "ffmpeg", "bin", binaryName),
		} {
			if fileExists(candidate) {
				return candidate, nil
			}
		}
	}

	if p, err := lookPath("ffmpeg"); err == nil {
		return p, nil
	}

	common := map[string][]string{
		"darwin": {"/opt/homebrew/bin/ffmpeg", "/usr/local/bin/ffmpeg"},
		"linux":  {"/usr/bin/ffmpeg", "/usr/local/bin/ffmpeg"},
	}
	for _, candidate := range common[goos] {
		if fileExists(candidate) {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("ffmpeg not found; reinstall Karte or set KARTE_FFMPEG_BINARY/FFMPEG_PATH")
}

func regularFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
