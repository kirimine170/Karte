package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// EncodePCMToM4A encodes PCM float32 samples to M4A format using ffmpeg
func EncodePCMToM4A(ctx context.Context, samples []float32, sampleRate int, outputPath string) error {
	if sampleRate <= 0 {
		sampleRate = DefaultSampleRate
	}

	ffmpegPath, err := findFFmpeg()
	if err != nil {
		return err
	}

	// Convert float32 samples to int16 PCM using bytes.Buffer for safety
	var pcmBuf bytes.Buffer
	for _, sample := range samples {
		// Clamp to [-1.0, 1.0] range
		if sample > 1.0 {
			sample = 1.0
		}
		if sample < -1.0 {
			sample = -1.0
		}
		// Convert to int16
		int16Sample := int16(sample * 32767.0)
		// Write int16 as little-endian bytes (binary.Write handles sign correctly)
		if err := binary.Write(&pcmBuf, binary.LittleEndian, int16Sample); err != nil {
			return fmt.Errorf("failed to write PCM data: %w", err)
		}
	}
	pcmData := pcmBuf.Bytes()

	// Create ffmpeg command to encode PCM to M4A
	// M4A is essentially MP4 container with AAC audio
	cmd := exec.CommandContext(ctx, ffmpegPath,
		"-f", "s16le", // Input format: signed 16-bit little-endian
		"-ar", fmt.Sprintf("%d", sampleRate), // Sample rate
		"-ac", "1", // Mono
		"-i", "pipe:0", // Read from stdin
		"-c:a", "aac", // Audio codec: AAC
		"-b:a", "128k", // Bitrate: 128kbps
		"-f", "mp4", // Output format: MP4 (M4A uses MP4 container)
		"-movflags", "+faststart", // Enable fast start for web playback
		"-y", // Overwrite output file
		outputPath,
	)

	cmd.Stdin = bytes.NewReader(pcmData)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg encode failed: %w (stderr: %s)", err, stderr.String())
	}

	return nil
}

// EncodePCMToWAV encodes PCM float32 samples to WAV format
func EncodePCMToWAV(ctx context.Context, samples []float32, sampleRate int, outputPath string) error {
	if sampleRate <= 0 {
		sampleRate = DefaultSampleRate
	}

	file, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("create wav file: %w", err)
	}
	defer file.Close()

	// Write RIFF header
	file.WriteString("RIFF")
	// Placeholder for file size (will be updated later)
	fileSizePos, _ := file.Seek(0, io.SeekCurrent)
	binary.Write(file, binary.LittleEndian, uint32(0)) // Placeholder
	file.WriteString("WAVE")

	// Write fmt chunk
	file.WriteString("fmt ")
	binary.Write(file, binary.LittleEndian, uint32(16)) // fmt chunk size
	binary.Write(file, binary.LittleEndian, uint16(1))  // Audio format: PCM
	binary.Write(file, binary.LittleEndian, uint16(1))  // Channels: mono
	binary.Write(file, binary.LittleEndian, uint32(sampleRate))
	binary.Write(file, binary.LittleEndian, uint32(sampleRate*2)) // Byte rate
	binary.Write(file, binary.LittleEndian, uint16(2))            // Block align
	binary.Write(file, binary.LittleEndian, uint16(16))           // Bits per sample

	// Write data chunk
	file.WriteString("data")
	dataSizePos, _ := file.Seek(0, io.SeekCurrent)
	binary.Write(file, binary.LittleEndian, uint32(0)) // Placeholder

	// Write PCM data
	for _, sample := range samples {
		// Clamp to [-1.0, 1.0] range
		if sample > 1.0 {
			sample = 1.0
		}
		if sample < -1.0 {
			sample = -1.0
		}
		// Convert to int16
		int16Sample := int16(sample * 32767.0)
		binary.Write(file, binary.LittleEndian, int16Sample)
	}

	// Update file size
	fileSize, _ := file.Seek(0, io.SeekEnd)
	fileSize -= 8 // Exclude RIFF header and size field
	file.Seek(fileSizePos, io.SeekStart)
	binary.Write(file, binary.LittleEndian, uint32(fileSize))

	// Update data chunk size
	dataSize := fileSize - 36 // Exclude RIFF header, fmt chunk, and data header
	file.Seek(dataSizePos, io.SeekStart)
	binary.Write(file, binary.LittleEndian, uint32(dataSize))

	return nil
}
