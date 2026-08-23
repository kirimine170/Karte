package asr

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"karte/internal/audio"
)

type wavChunkStreamer func(string, int, func(int, []float32) error) error

type segmentDecoderOps[T any] struct {
	newDecoder     func() (T, error)
	acceptWaveform func(T, int, []float32) error
	decode         func(T) (string, error)
	close          func(T)
}

type transcriptionResult struct {
	text         string
	segmentCount int
}

// countWavSegments walks a converted WAV once and counts each segment when it
// starts. An active final segment has therefore already been counted at EOF.
func countWavSegments(
	ctx context.Context,
	wavPath string,
	sampleRate int,
	streamChunks wavChunkStreamer,
) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if sampleRate <= 0 {
		return 0, fmt.Errorf("invalid sample rate: %d", sampleRate)
	}
	if streamChunks == nil {
		return 0, errors.New("nil WAV chunk streamer")
	}

	chunkSamples := sampleRate / 100
	if chunkSamples < 160 {
		chunkSamples = 160
	}

	var (
		vad                   = audio.DefaultSimpleVAD()
		segmentCount          int
		inSegment             bool
		currentSegmentSamples int
		maxSegmentSamples     = sampleRate * 15
	)
	err := streamChunks(wavPath, chunkSamples, func(_ int, chunk []float32) error {
		if err := ctx.Err(); err != nil {
			return err
		}

		isSpeech, flush := vad.Process(chunk)
		if isSpeech {
			if !inSegment {
				inSegment = true
				segmentCount++
			}
			currentSegmentSamples += len(chunk)
			if currentSegmentSamples >= maxSegmentSamples {
				inSegment = false
				currentSegmentSamples = 0
				vad.Reset()
			}
		}
		if flush {
			inSegment = false
			currentSegmentSamples = 0
			vad.Reset()
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return segmentCount, nil
}

// transcribeWavSinglePass walks the converted WAV exactly once and keeps at
// most one segment decoder alive at a time.
func transcribeWavSinglePass[T any](
	ctx context.Context,
	wavPath string,
	sampleRate int,
	ops segmentDecoderOps[T],
	progress func(line string, segmentIndex, totalSegments int, timestamp float64),
	streamChunks wavChunkStreamer,
) (transcriptionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return transcriptionResult{}, err
	}
	if sampleRate <= 0 {
		return transcriptionResult{}, fmt.Errorf("invalid sample rate: %d", sampleRate)
	}
	if ops.newDecoder == nil || ops.acceptWaveform == nil || ops.decode == nil || ops.close == nil {
		return transcriptionResult{}, errors.New("incomplete segment decoder operations")
	}
	if streamChunks == nil {
		return transcriptionResult{}, errors.New("nil WAV chunk streamer")
	}

	var (
		result              transcriptionResult
		transcript          strings.Builder
		vad                 = audio.DefaultSimpleVAD()
		segmentDecoder      T
		hasSegmentDecoder   bool
		segmentSamples      int
		segmentIndex        int
		processedSamples    int
		segmentStartSamples int
	)
	maxSegmentSamples := sampleRate * 15

	closeSegment := func() {
		if !hasSegmentDecoder {
			return
		}
		ops.close(segmentDecoder)
		var zero T
		segmentDecoder = zero
		hasSegmentDecoder = false
		segmentSamples = 0
	}
	defer closeSegment()

	finalizeSegment := func() error {
		if !hasSegmentDecoder {
			return nil
		}

		decoder := segmentDecoder
		var zero T
		segmentDecoder = zero
		hasSegmentDecoder = false
		segmentSamples = 0
		defer ops.close(decoder)

		if err := ctx.Err(); err != nil {
			return err
		}
		text, err := ops.decode(decoder)
		if err != nil {
			return fmt.Errorf("decode segment: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		result.segmentCount++
		text = strings.TrimSpace(text)
		if text == "" {
			return nil
		}

		segmentIndex++
		timestamp := float64(segmentStartSamples) / float64(sampleRate)
		appendLines(&transcript, text, func(line string) {
			if progress != nil {
				progress(line, segmentIndex, 0, timestamp)
			}
		})
		return nil
	}

	chunkSamples := sampleRate / 100
	if chunkSamples < 160 {
		chunkSamples = 160
	}

	err := streamChunks(wavPath, chunkSamples, func(chunkSampleRate int, chunk []float32) error {
		if err := ctx.Err(); err != nil {
			return err
		}

		isSpeech, flush := vad.Process(chunk)
		chunkSize := len(chunk)
		if isSpeech {
			if !hasSegmentDecoder {
				decoder, err := ops.newDecoder()
				if err != nil {
					return fmt.Errorf("create segment decoder: %w", err)
				}
				segmentDecoder = decoder
				hasSegmentDecoder = true
				segmentStartSamples = processedSamples
			}
			if err := ops.acceptWaveform(segmentDecoder, chunkSampleRate, chunk); err != nil {
				return fmt.Errorf("accept segment waveform: %w", err)
			}
			segmentSamples += chunkSize
			if segmentSamples >= maxSegmentSamples {
				if err := finalizeSegment(); err != nil {
					return err
				}
				vad.Reset()
			}
		}
		if flush {
			if err := finalizeSegment(); err != nil {
				return err
			}
			vad.Reset()
		}

		processedSamples += chunkSize
		return nil
	})
	if err != nil {
		return transcriptionResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return transcriptionResult{}, err
	}
	if err := finalizeSegment(); err != nil {
		return transcriptionResult{}, err
	}

	result.text = strings.TrimSpace(transcript.String())
	return result, nil
}

func appendLines(buf *strings.Builder, portion string, progress func(line string)) {
	for _, line := range strings.Split(portion, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		buf.WriteString(line)
		buf.WriteRune('\n')
		if progress != nil {
			progress(line)
		}
	}
}
