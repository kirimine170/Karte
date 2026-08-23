package compliance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundledModelLicenseHeaderUsesActualPathAndPinnedSources(t *testing.T) {
	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	modelRoot := "templates/karte_data_template/data/asr/sherpa-onnx-streaming-zipformer-ar_en_id_ja_ru_th_vi_zh-2025-02-10"
	data, err := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(modelRoot), "LICENSE"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, forbidden := range []string{":contentReference[", "third_party/models/", "Below this line, paste"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("model LICENSE still contains broken marker %q", forbidden)
		}
	}
	for _, want := range []string{
		modelRoot,
		"d21e331a200a518138599f1ec412b3bb1c919fe9",
		"c6726c1147387ad2a11148b33973135d92a55e6c",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("model LICENSE is missing pinned evidence %q", want)
		}
	}
	var registry AssetSourceRegistry
	if err := LoadJSONFile(filepath.Join(repositoryRoot, "compliance", "asset-sources.json"), &registry); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, collection := range registry.Collections {
		if collection.ID != "asset:pengcheng-starling-asr-model" {
			continue
		}
		found = true
		if !strings.Contains(collection.Source, "d21e331a200a518138599f1ec412b3bb1c919fe9") ||
			!strings.Contains(collection.SecondarySource, "c6726c1147387ad2a11148b33973135d92a55e6c") {
			t.Fatalf("model sources are not immutable: %+v", collection)
		}
	}
	if !found {
		t.Fatal("model asset registry entry is missing")
	}
}
