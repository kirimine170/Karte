package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

const (
	// DefaultSampleRate is the PCM rate the ASR pipeline expects.
	DefaultSampleRate = 16000
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

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, nil, fmt.Errorf("ffmpeg stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return 0, nil, fmt.Errorf("ffmpeg start: %w (stderr: %s)", err, stderr.String())
	}

	data, err := io.ReadAll(stdout)
	if err != nil {
		return 0, nil, fmt.Errorf("ffmpeg read: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		return 0, nil, fmt.Errorf("ffmpeg failed: %w (stderr: %s)", err, stderr.String())
	}

	if len(data)%4 != 0 {
		return 0, nil, fmt.Errorf("unexpected PCM buffer size: %d", len(data))
	}

	samples := make([]float32, len(data)/4)
	for i := 0; i < len(samples); i++ {
		bits := binary.LittleEndian.Uint32(data[i*4 : i*4+4])
		samples[i] = math.Float32frombits(bits)
	}

	return sampleRate, samples, nil
}

// ConvertToPCM16Wav converts the given audio file into a temporary 16-bit mono WAV file.
// Caller must invoke the returned cleanup function.
func ConvertToPCM16Wav(ctx context.Context, srcPath string, sampleRate int) (string, func(), error) {
	if sampleRate <= 0 {
		sampleRate = DefaultSampleRate
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
	tmpFile.Close()

	cmd := exec.CommandContext(ctx, ffmpegPath,
		"-i", srcPath,
		"-ac", "1",
		"-ar", fmt.Sprintf("%d", sampleRate),
		"-f", "wav",
		"-y",
		tmpPath,
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		os.Remove(tmpPath)
		return "", nil, fmt.Errorf("ffmpeg wav convert failed: %w (stderr: %s)", err, stderr.String())
	}

	cleanup := func() {
		os.Remove(tmpPath)
	}

	return tmpPath, cleanup, nil
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
