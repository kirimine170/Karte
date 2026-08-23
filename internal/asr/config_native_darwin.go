//go:build darwin && !universal

package asr

import sherpa "github.com/k2-fsa/sherpa-onnx-go-macos"

func (c *Config) offlineRecognizerConfig() *sherpa.OfflineRecognizerConfig {
	modelCfg := sherpa.OfflineModelConfig{
		Tokens:     c.Model.Tokens,
		NumThreads: c.Runtime.Threads,
		Provider:   c.Runtime.Provider,
	}

	if c.Model.ZipformerCTC != "" {
		modelCfg.ZipformerCtc = sherpa.OfflineZipformerCtcModelConfig{
			Model: c.Model.ZipformerCTC,
		}
	} else {
		modelCfg.Transducer = sherpa.OfflineTransducerModelConfig{
			Encoder: c.Model.Encoder,
			Decoder: c.Model.Decoder,
			Joiner:  c.Model.Joiner,
		}
	}

	return &sherpa.OfflineRecognizerConfig{
		FeatConfig: sherpa.FeatureConfig{
			SampleRate: c.SampleRate,
			FeatureDim: 80,
		},
		ModelConfig:    modelCfg,
		DecodingMethod: c.Decoding.Method,
		MaxActivePaths: 4,
		BlankPenalty:   0.0,
	}
}
