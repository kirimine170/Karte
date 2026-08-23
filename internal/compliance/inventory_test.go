package compliance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildAssetInventoryHashesEveryDistributedFileAndDetectsDrift(t *testing.T) {
	root := t.TempDir()
	assetRoot := filepath.Join(root, "assets")
	if err := os.MkdirAll(assetRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetRoot, "LICENSE"), []byte(testMITLicense), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetRoot, "model.bin"), []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := AssetSourceRegistry{SchemaVersion: SchemaVersion, Collections: []AssetSource{{
		ID: "asset:test", Name: "test asset", Version: "1", License: "MIT",
		Source: "https://example.test/assets/v1", Root: "assets", LicensePath: "assets/LICENSE",
	}}}
	inventory, err := BuildAssetInventory(root, registry)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Collections) != 1 || len(inventory.Collections[0].Files) != 2 {
		t.Fatalf("unexpected inventory: %+v", inventory)
	}
	if err := ValidateAssetInventory(root, registry, inventory); err != nil {
		t.Fatalf("fresh inventory failed verification: %v", err)
	}
	if err := os.WriteFile(filepath.Join(assetRoot, "model.bin"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAssetInventory(root, registry, inventory); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("expected changed asset bytes to invalidate inventory，got %v", err)
	}
}

func TestBuildAssetInventoryRejectsAncestorSymlinkForAssetAndLicense(t *testing.T) {
	for _, licenseThroughLink := range []bool{false, true} {
		t.Run(map[bool]string{false: "asset-root", true: "license"}[licenseThroughLink], func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			if err := os.WriteFile(filepath.Join(outside, "LICENSE"), []byte(testMITLicense), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(outside, "asset.bin"), []byte("asset"), 0o644); err != nil {
				t.Fatal(err)
			}
			assetRoot := "linked"
			licensePath := "linked/LICENSE"
			if licenseThroughLink {
				assetRoot = "assets"
				if err := os.MkdirAll(filepath.Join(root, assetRoot), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, assetRoot, "asset.bin"), []byte("asset"), 0o644); err != nil {
					t.Fatal(err)
				}
				licensePath = "linked/LICENSE"
			}
			if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			registry := AssetSourceRegistry{SchemaVersion: SchemaVersion, Collections: []AssetSource{{
				ID: "asset:linked", Name: "linked", Version: "1", License: "MIT",
				Source: "https://example.test/linked", Root: assetRoot, LicensePath: licensePath,
			}}}
			if _, err := BuildAssetInventory(root, registry); err == nil || !strings.Contains(err.Error(), "symlink") {
				t.Fatalf("expected ancestor symlink rejection，got %v", err)
			}
		})
	}
}

func TestLoadNativeRegistryPinsAndHydratesLicenseEvidence(t *testing.T) {
	root := t.TempDir()
	writeNativeRegistryFixture(t, root, testMITLicense, "MIT")
	registry, err := LoadNativeRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Components) != 1 || len(registry.Components[0].LicenseEvidence) != 1 {
		t.Fatalf("native evidence was not hydrated: %+v", registry.Components)
	}
	if got := registry.Components[0].Properties["licenseEvidenceSource"]; !strings.Contains(got, "0123456789abcdef") {
		t.Fatalf("immutable evidence source was not retained: %q", got)
	}

	writeTestFile(t, root, "compliance/evidence/LICENSE", "mutated")
	if _, err := LoadNativeRegistry(root); err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("expected evidence drift failure，got %v", err)
	}
}

func TestLoadNativeRegistryRejectsWrongLicenseAndAncestorSymlink(t *testing.T) {
	t.Run("wrong license body", func(t *testing.T) {
		root := t.TempDir()
		writeNativeRegistryFixture(t, root, "Apache License\nVersion 2.0\n", "MIT")
		if _, err := LoadNativeRegistry(root); err == nil || !strings.Contains(err.Error(), `identifies "Apache-2.0"，want "MIT"`) {
			t.Fatalf("expected mismatched evidence failure，got %v", err)
		}
	})
	t.Run("symlinked ancestor", func(t *testing.T) {
		root := t.TempDir()
		writeNativeRegistryFixture(t, root, testMITLicense, "MIT")
		outside := t.TempDir()
		writeTestFile(t, outside, "LICENSE", testMITLicense)
		if err := os.RemoveAll(filepath.Join(root, "compliance", "evidence")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(root, "compliance", "evidence")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if _, err := LoadNativeRegistry(root); err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("expected symlinked evidence ancestor failure，got %v", err)
		}
	})
}

func writeNativeRegistryFixture(t *testing.T, root, evidenceText, declaredLicense string) {
	t.Helper()
	normalized, err := normalizeText([]byte(evidenceText))
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, "compliance/evidence/LICENSE", normalized)
	writeJSONFile(t, filepath.Join(root, "compliance", "native-components.json"), NativeRegistry{
		SchemaVersion: SchemaVersion,
		Components: []Component{{
			ID: "native:test", Type: "native", Name: "native fixture", License: declaredLicense,
			Scope: "runtime", Source: "https://example.test/native/0123456789abcdef", DistributionPath: "native.dll",
		}},
		LicenseSources: []NativeLicenseSource{{
			ComponentID: "native:test", Path: "compliance/evidence/LICENSE", SHA256: sha256Hex([]byte(normalized)),
			Source: "https://example.test/native/0123456789abcdef/LICENSE",
		}},
	})
}
