package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os/exec"
	"path/filepath"
	"regexp"
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
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return 0, nil, fmt.Errorf("ffmpeg not found in PATH: %w", err)
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
