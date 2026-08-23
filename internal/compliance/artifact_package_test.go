package compliance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackageArtifactAssetsCopiesEmbeddedEvidenceAndMapsWindowsModel(t *testing.T) {
	repositoryRoot, inventory := writeArtifactPackageRepository(t)
	artifactRoot := t.TempDir()
	modelFile := inventory.Collections[1].Files[0]
	writeTestFile(t, artifactRoot, "karte_data/data/asr/model/model.onnx", "model")
	if err := PackageArtifactAssets(repositoryRoot, artifactRoot, "windows"); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(artifactRoot, "compliance", ArtifactAssetManifestName)
	var manifest ArtifactAssetManifest
	if err := LoadJSONFile(manifestPath, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Files) != 2 || len(manifest.Exclusions) != 0 {
		t.Fatalf("unexpected packaged manifest: %+v", manifest)
	}
	if got := manifest.Files[1].ArtifactPath; got != "karte_data/data/asr/model/model.onnx" {
		t.Fatalf("model artifact path = %q", got)
	}
	if manifest.Files[1].SHA256 != modelFile.SHA256 {
		t.Fatal("model hash was not preserved")
	}
	if _, err := os.Stat(filepath.Join(artifactRoot, "compliance", "asset-files", "frontend", "asset.bin")); err != nil {
		t.Fatalf("embedded frontend evidence copy is missing: %v", err)
	}
}

func TestPackageArtifactAssetsAcceptsRelativeArtifactRoot(t *testing.T) {
	repositoryRoot, inventory := writeArtifactPackageRepository(t)
	artifactRoot := t.TempDir()
	modelFile := inventory.Collections[1].Files[0]
	writeTestFile(t, artifactRoot, "karte_data/data/asr/model/model.onnx", "model")
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relativeArtifactRoot, err := filepath.Rel(workingDirectory, artifactRoot)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.IsAbs(relativeArtifactRoot) {
		t.Fatalf("fixture artifact root is not relative: %s", relativeArtifactRoot)
	}
	if err := PackageArtifactAssets(repositoryRoot, relativeArtifactRoot, "windows"); err != nil {
		t.Fatal(err)
	}
	var manifest ArtifactAssetManifest
	if err := LoadJSONFile(filepath.Join(artifactRoot, "compliance", ArtifactAssetManifestName), &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Files) != 2 || manifest.Files[1].SHA256 != modelFile.SHA256 {
		t.Fatalf("relative-root manifest mismatch: %+v", manifest)
	}
}

func TestPackageArtifactAssetsRecordsRegistryAuthorizedLinuxExclusion(t *testing.T) {
	repositoryRoot, _ := writeArtifactPackageRepository(t)
	artifactRoot := t.TempDir()
	if err := PackageArtifactAssets(repositoryRoot, artifactRoot, "linux"); err != nil {
		t.Fatal(err)
	}
	var manifest ArtifactAssetManifest
	if err := LoadJSONFile(filepath.Join(artifactRoot, "compliance", ArtifactAssetManifestName), &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Exclusions) != 1 || manifest.Exclusions[0].ComponentID != "asset:test-asr-model" {
		t.Fatalf("Linux model exclusion was not explicit: %+v", manifest.Exclusions)
	}
	var nativeManifest NativeBuildManifest
	if err := LoadJSONFile(filepath.Join(artifactRoot, "compliance", NativeBuildManifestName), &nativeManifest); err != nil {
		t.Fatal(err)
	}
	if len(nativeManifest.SystemRuntimes) != 1 || nativeManifest.SystemRuntimes[0].ComponentID != "native:test-linux-system" {
		t.Fatalf("Linux system runtime separation was not explicit: %+v", nativeManifest.SystemRuntimes)
	}
}

func TestModelArtifactPathUsesBuildmatrixPlatformLayout(t *testing.T) {
	source := "templates/karte_data_template/data/asr/model/model.onnx"
	for _, test := range []struct {
		platform string
		want     string
	}{
		{platform: "darwin", want: "Contents/Resources/karte_data_template/data/asr/model/model.onnx"},
		{platform: "windows", want: "karte_data/data/asr/model/model.onnx"},
	} {
		got, err := modelArtifactPath(test.platform, source)
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Errorf("modelArtifactPath(%s) = %q，want %q", test.platform, got, test.want)
		}
	}
	if _, err := modelArtifactPath("linux", source); err == nil || !strings.Contains(err.Error(), "registry artifactExclusions.linux") {
		t.Fatalf("Linux omission must require registry policy，got %v", err)
	}
}

func TestPackageArtifactAssetsRejectsSourceDrift(t *testing.T) {
	repositoryRoot, _ := writeArtifactPackageRepository(t)
	writeTestFile(t, repositoryRoot, "frontend/asset.bin", "changed")
	err := PackageArtifactAssets(repositoryRoot, t.TempDir(), "linux")
	if err == nil || !strings.Contains(err.Error(), "does not match compliance/assets.json") {
		t.Fatalf("expected source drift failure，got %v", err)
	}
}

func writeArtifactPackageRepository(t *testing.T) (string, AssetInventory) {
	t.Helper()
	repositoryRoot := t.TempDir()
	writeTestFile(t, repositoryRoot, "frontend/asset.bin", "frontend")
	writeTestFile(t, repositoryRoot, "templates/karte_data_template/data/asr/model/model.onnx", "model")
	frontendHash, frontendBytes, err := hashRegularFile(repositoryRoot, "frontend/asset.bin")
	if err != nil {
		t.Fatal(err)
	}
	modelHash, modelBytes, err := hashRegularFile(repositoryRoot, "templates/karte_data_template/data/asr/model/model.onnx")
	if err != nil {
		t.Fatal(err)
	}
	inventory := AssetInventory{SchemaVersion: SchemaVersion, Collections: []AssetCollection{
		{
			ID: "asset:frontend", Root: "frontend",
			Files: []AssetFile{{Path: "frontend/asset.bin", Bytes: frontendBytes, SHA256: frontendHash}},
		},
		{
			ID: "asset:test-asr-model", Root: "templates/karte_data_template/data/asr/model",
			ArtifactExclusions: map[string]string{
				"linux": "The Linux fixture intentionally obtains this optional model from user data storage.",
			},
			Files: []AssetFile{{
				Path: "templates/karte_data_template/data/asr/model/model.onnx", Bytes: modelBytes, SHA256: modelHash,
			}},
		},
	}}
	writeJSONFile(t, filepath.Join(repositoryRoot, filepath.FromSlash(AssetInventoryPath)), inventory)
	writeJSONFile(t, filepath.Join(repositoryRoot, "compliance", "native-components.json"), NativeRegistry{
		SchemaVersion: SchemaVersion,
		Components: []Component{{
			ID: "native:test-linux-system", Type: "native", Name: "system fixture", License: "MIT", Scope: "system-runtime",
			Source: "https://example.test/system", Properties: map[string]string{
				"platform": "linux", "artifactCoverage": "Excluded because the target operating system resolves this shared library.",
			},
		}},
	})
	return repositoryRoot, inventory
}
