package asr

import "testing"

func TestConfigIsStreamingModel(t *testing.T) {
	tests := []struct {
		name  string
		model ModelSpec
		want  bool
	}{
		{
			name: "bundled streaming encoder",
			model: ModelSpec{
				Encoder: "data/asr/model/encoder-epoch-75-avg-11-chunk-16-left-128.int8.onnx",
				Decoder: "data/asr/model/decoder-epoch-75-avg-11-chunk-16-left-128.onnx",
			},
			want: true,
		},
		{
			name: "streaming model directory",
			model: ModelSpec{
				Encoder: "data/asr/streaming-model/encoder.onnx",
				Decoder: "data/asr/streaming-model/decoder.onnx",
			},
			want: true,
		},
		{
			name: "offline transducer",
			model: ModelSpec{
				Encoder: "encoder.int8.onnx",
				Decoder: "decoder.onnx",
				Joiner:  "joiner.int8.onnx",
			},
			want: false,
		},
		{
			name: "streaming CTC",
			model: ModelSpec{
				ZipformerCTC: "zipformer2-ctc-chunk-16.onnx",
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{Model: tt.model}
			if got := cfg.IsStreamingModel(); got != tt.want {
				t.Fatalf("IsStreamingModel() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNilConfigIsNotStreamingModel(t *testing.T) {
	var cfg *Config
	if cfg.IsStreamingModel() {
		t.Fatal("nil config must not be classified as streaming")
	}
}
