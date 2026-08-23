package asr

import (
	"context"
	"errors"
	"testing"
)

const (
	testSampleRate  = 16000
	testFrameSample = 160
)

type fakeSegmentDecoder struct {
	text     string
	accepted int
	closed   bool
}

type progressEvent struct {
	line          string
	segmentIndex  int
	totalSegments int
	timestamp     float64
}

func TestTranscribeWavSinglePassPreservesTranscriptAndUsesOneTraversal(t *testing.T) {
	frames := append(testSegmentFrames(true), testSegmentFrames(false)...)
	decodeTexts := []string{" first line \n\n continued ", "second line"}
	streamCalls := 0
	decodeCalls := 0
	closeCalls := 0
	var decoders []*fakeSegmentDecoder
	var progress []progressEvent

	result, err := transcribeWavSinglePass(
		context.Background(),
		"converted.wav",
		testSampleRate,
		segmentDecoderOps[*fakeSegmentDecoder]{
			newDecoder: func() (*fakeSegmentDecoder, error) {
				if len(decoders) >= len(decodeTexts) {
					t.Fatalf("created unexpected decoder %d", len(decoders)+1)
				}
				decoder := &fakeSegmentDecoder{text: decodeTexts[len(decoders)]}
				decoders = append(decoders, decoder)
				return decoder, nil
			},
			acceptWaveform: func(decoder *fakeSegmentDecoder, sampleRate int, chunk []float32) error {
				if sampleRate != testSampleRate {
					t.Fatalf("sample rate = %d，want %d", sampleRate, testSampleRate)
				}
				decoder.accepted += len(chunk)
				return nil
			},
			decode: func(decoder *fakeSegmentDecoder) (string, error) {
				decodeCalls++
				return decoder.text, nil
			},
			close: func(decoder *fakeSegmentDecoder) {
				if decoder.closed {
					t.Fatal("decoder closed more than once")
				}
				decoder.closed = true
				closeCalls++
			},
		},
		func(line string, segmentIndex, totalSegments int, timestamp float64) {
			progress = append(progress, progressEvent{
				line:          line,
				segmentIndex:  segmentIndex,
				totalSegments: totalSegments,
				timestamp:     timestamp,
			})
		},
		frameStreamer(t, frames, &streamCalls),
	)
	if err != nil {
		t.Fatalf("transcribeWavSinglePass() error = %v", err)
	}

	if result.text != "first line\ncontinued\nsecond line" {
		t.Fatalf("text = %q，want equivalent normalized transcript", result.text)
	}
	if result.segmentCount != 2 {
		t.Fatalf("segment count = %d，want 2", result.segmentCount)
	}
	if streamCalls != 1 {
		t.Fatalf("WAV traversals = %d，want 1", streamCalls)
	}
	if len(decoders) != 2 || decodeCalls != 2 || closeCalls != 2 {
		t.Fatalf("decoder lifecycle = created %d，decoded %d，closed %d，want 2 each", len(decoders), decodeCalls, closeCalls)
	}
	for i, decoder := range decoders {
		if decoder.accepted == 0 {
			t.Fatalf("decoder %d accepted no samples", i+1)
		}
	}

	wantProgress := []progressEvent{
		{line: "first line", segmentIndex: 1, totalSegments: 0},
		{line: "continued", segmentIndex: 1, totalSegments: 0},
		{line: "second line", segmentIndex: 2, totalSegments: 0},
	}
	if len(progress) != len(wantProgress) {
		t.Fatalf("progress events = %d，want %d", len(progress), len(wantProgress))
	}
	for i, want := range wantProgress {
		got := progress[i]
		if got.line != want.line || got.segmentIndex != want.segmentIndex || got.totalSegments != want.totalSegments {
			t.Errorf("progress[%d] = %#v，want %#v", i, got, want)
		}
	}
	if progress[0].timestamp >= progress[len(progress)-1].timestamp {
		t.Fatalf("timestamps did not advance: first=%f final=%f", progress[0].timestamp, progress[len(progress)-1].timestamp)
	}
}

func TestCountWavSegmentsCountsFinalSegmentOnce(t *testing.T) {
	frames := append(testSegmentFrames(true), testSegmentFrames(false)...)
	streamCalls := 0

	segmentCount, err := countWavSegments(
		context.Background(),
		"converted.wav",
		testSampleRate,
		frameStreamer(t, frames, &streamCalls),
	)
	if err != nil {
		t.Fatalf("countWavSegments() error = %v", err)
	}
	if segmentCount != 2 {
		t.Fatalf("segment count = %d，want 2", segmentCount)
	}
	if streamCalls != 1 {
		t.Fatalf("WAV traversals = %d，want 1", streamCalls)
	}
}

func TestTranscribeWavSinglePassCountsFinalSegmentOnce(t *testing.T) {
	streamCalls := 0
	created := 0
	decoded := 0
	closed := 0

	result, err := transcribeWavSinglePass(
		context.Background(),
		"converted.wav",
		testSampleRate,
		segmentDecoderOps[*fakeSegmentDecoder]{
			newDecoder: func() (*fakeSegmentDecoder, error) {
				created++
				return &fakeSegmentDecoder{text: "final"}, nil
			},
			acceptWaveform: func(decoder *fakeSegmentDecoder, _ int, chunk []float32) error {
				decoder.accepted += len(chunk)
				return nil
			},
			decode: func(decoder *fakeSegmentDecoder) (string, error) {
				decoded++
				return decoder.text, nil
			},
			close: func(*fakeSegmentDecoder) { closed++ },
		},
		nil,
		frameStreamer(t, testSegmentFrames(false), &streamCalls),
	)
	if err != nil {
		t.Fatalf("transcribeWavSinglePass() error = %v", err)
	}
	if result.segmentCount != 1 {
		t.Fatalf("final segment count = %d，want 1", result.segmentCount)
	}
	if streamCalls != 1 || created != 1 || decoded != 1 || closed != 1 {
		t.Fatalf("lifecycle = traversed %d，created %d，decoded %d，closed %d，want 1 each", streamCalls, created, decoded, closed)
	}
}

func TestTranscribeWavSinglePassCancellationClosesActiveSegment(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	created := 0
	decoded := 0
	closed := 0
	frames := testSegmentFrames(false)
	frames = append(frames, repeatedFrames(20, 0.1)...)

	streamer := func(_ string, _ int, handler func(int, []float32) error) error {
		for _, frame := range frames {
			if err := handler(testSampleRate, frame); err != nil {
				return err
			}
			if created == 1 {
				cancel()
			}
		}
		return nil
	}

	_, err := transcribeWavSinglePass(
		ctx,
		"converted.wav",
		testSampleRate,
		segmentDecoderOps[*fakeSegmentDecoder]{
			newDecoder: func() (*fakeSegmentDecoder, error) {
				created++
				return &fakeSegmentDecoder{text: "unused"}, nil
			},
			acceptWaveform: func(*fakeSegmentDecoder, int, []float32) error { return nil },
			decode: func(decoder *fakeSegmentDecoder) (string, error) {
				decoded++
				return decoder.text, nil
			},
			close: func(*fakeSegmentDecoder) { closed++ },
		},
		nil,
		streamer,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v，want context.Canceled", err)
	}
	if created != 1 || decoded != 0 || closed != 1 {
		t.Fatalf("lifecycle after cancellation = created %d，decoded %d，closed %d，want 1，0，1", created, decoded, closed)
	}
}

func TestTranscribeWavSinglePassPropagatesErrorsAndClosesSegment(t *testing.T) {
	t.Run("stream", func(t *testing.T) {
		streamErr := errors.New("stream failed")
		created := 0
		decoded := 0
		closed := 0
		frames := testSegmentFrames(false)

		streamer := func(_ string, _ int, handler func(int, []float32) error) error {
			for _, frame := range frames {
				if err := handler(testSampleRate, frame); err != nil {
					return err
				}
				if created == 1 {
					return streamErr
				}
			}
			return nil
		}

		_, err := transcribeWavSinglePass(
			context.Background(),
			"converted.wav",
			testSampleRate,
			segmentDecoderOps[*fakeSegmentDecoder]{
				newDecoder: func() (*fakeSegmentDecoder, error) {
					created++
					return &fakeSegmentDecoder{text: "unused"}, nil
				},
				acceptWaveform: func(*fakeSegmentDecoder, int, []float32) error { return nil },
				decode: func(decoder *fakeSegmentDecoder) (string, error) {
					decoded++
					return decoder.text, nil
				},
				close: func(*fakeSegmentDecoder) { closed++ },
			},
			nil,
			streamer,
		)
		if !errors.Is(err, streamErr) {
			t.Fatalf("error = %v，want stream error", err)
		}
		if created != 1 || decoded != 0 || closed != 1 {
			t.Fatalf("lifecycle after stream error = created %d，decoded %d，closed %d，want 1，0，1", created, decoded, closed)
		}
	})

	t.Run("decode", func(t *testing.T) {
		decodeErr := errors.New("decode failed")
		closed := 0
		streamCalls := 0

		_, err := transcribeWavSinglePass(
			context.Background(),
			"converted.wav",
			testSampleRate,
			segmentDecoderOps[*fakeSegmentDecoder]{
				newDecoder: func() (*fakeSegmentDecoder, error) {
					return &fakeSegmentDecoder{}, nil
				},
				acceptWaveform: func(*fakeSegmentDecoder, int, []float32) error { return nil },
				decode:         func(*fakeSegmentDecoder) (string, error) { return "", decodeErr },
				close:          func(*fakeSegmentDecoder) { closed++ },
			},
			nil,
			frameStreamer(t, testSegmentFrames(false), &streamCalls),
		)
		if !errors.Is(err, decodeErr) {
			t.Fatalf("error = %v，want decode error", err)
		}
		if streamCalls != 1 || closed != 1 {
			t.Fatalf("lifecycle after decode error = traversed %d，closed %d，want 1 each", streamCalls, closed)
		}
	})
}

func BenchmarkTranscribeWavSinglePass(b *testing.B) {
	frames := append(testSegmentFrames(true), testSegmentFrames(false)...)
	streamer := func(_ string, _ int, handler func(int, []float32) error) error {
		for _, frame := range frames {
			if err := handler(testSampleRate, frame); err != nil {
				return err
			}
		}
		return nil
	}
	ops := segmentDecoderOps[*fakeSegmentDecoder]{
		newDecoder:     func() (*fakeSegmentDecoder, error) { return &fakeSegmentDecoder{text: "text"}, nil },
		acceptWaveform: func(*fakeSegmentDecoder, int, []float32) error { return nil },
		decode:         func(decoder *fakeSegmentDecoder) (string, error) { return decoder.text, nil },
		close:          func(*fakeSegmentDecoder) {},
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := transcribeWavSinglePass(context.Background(), "converted.wav", testSampleRate, ops, nil, streamer); err != nil {
			b.Fatal(err)
		}
	}
}

func frameStreamer(t *testing.T, frames [][]float32, calls *int) wavChunkStreamer {
	t.Helper()
	return func(path string, chunkSamples int, handler func(int, []float32) error) error {
		*calls = *calls + 1
		if path != "converted.wav" {
			t.Fatalf("WAV path = %q，want converted.wav", path)
		}
		if chunkSamples != testFrameSample {
			t.Fatalf("chunk samples = %d，want %d", chunkSamples, testFrameSample)
		}
		for _, frame := range frames {
			if err := handler(testSampleRate, frame); err != nil {
				return err
			}
		}
		return nil
	}
}

func testSegmentFrames(withTrailingSilence bool) [][]float32 {
	frames := repeatedFrames(20, 0.001)
	frames = append(frames, repeatedFrames(50, 0.1)...)
	if withTrailingSilence {
		frames = append(frames, repeatedFrames(100, 0)...)
	}
	return frames
}

func repeatedFrames(count int, level float32) [][]float32 {
	frames := make([][]float32, count)
	for i := range frames {
		frame := make([]float32, testFrameSample)
		for j := range frame {
			frame[j] = level
		}
		frames[i] = frame
	}
	return frames
}
