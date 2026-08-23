//go:build darwin && !universal

package asr

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestPinnedNativeRuntimeLoadsBundledModelAndRunsInference(t *testing.T) {
	modelDir := os.Getenv("KARTE_ASR_NATIVE_SMOKE_MODEL_DIR")
	if modelDir == "" {
		t.Skip("set KARTE_ASR_NATIVE_SMOKE_MODEL_DIR to run the native runtime smoke test")
	}

	modelPath := func(name string) string {
		path := filepath.Join(modelDir, name)
		assertRealModelFile(t, path)
		return path
	}

	cfg := &Config{
		Enabled:    true,
		SampleRate: 16000,
		Model: ModelSpec{
			Tokens:  modelPath("tokens.txt"),
			Encoder: modelPath("encoder-epoch-75-avg-11-chunk-16-left-128.int8.onnx"),
			Decoder: modelPath("decoder-epoch-75-avg-11-chunk-16-left-128.onnx"),
			Joiner:  modelPath("joiner-epoch-75-avg-11-chunk-16-left-128.int8.onnx"),
		},
		Decoding: DecodeSpec{Method: "greedy_search"},
		Runtime:  RuntimeSpec{Threads: 1, Provider: "cpu"},
	}

	service, err := NewRealtimeService(cfg)
	if err != nil {
		t.Fatalf("load bundled streaming model with pinned native runtime: %v", err)
	}
	defer service.Close()

	// Two seconds of silence is long enough to make the streaming recognizer
	// execute encoder/decoder work without depending on an external audio file.
	service.ProcessAudio(make([]float32, cfg.SampleRate*2))
	service.Flush()
}

func assertRealModelFile(t *testing.T, path string) {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open native smoke model %s: %v", path, err)
	}
	defer file.Close()

	prefix := make([]byte, 200)
	n, err := file.Read(prefix)
	if err != nil && err != io.EOF {
		t.Fatalf("read native smoke model %s: %v", path, err)
	}
	if bytes.Contains(prefix[:n], []byte("version https://git-lfs.github.com/spec/v1")) {
		t.Fatalf("native smoke model is a Git LFS pointer: %s", path)
	}
}
