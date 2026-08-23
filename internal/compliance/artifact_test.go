package compliance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuditArtifactVerifiesAssetsNativeMetadataAndComplianceFiles(t *testing.T) {
	repositoryRoot, artifactRoot := writeArtifactFixture(t)
	if err := AuditArtifact(repositoryRoot, artifactRoot, "windows", time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("valid fixture failed artifact audit: %v", err)
	}
}

func TestAuditArtifactFailsClosedForArtifactDriftAndMissingCoverage(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*testing.T, string, string)
		wantString string
	}{
		{
			name: "native-hash",
			mutate: func(t *testing.T, _, artifactRoot string) {
				writeTestFile(t, artifactRoot, "native.dll", "mutated")
			},
			wantString: "content/hash mismatch",
		},
		{
			name: "untracked-native",
			mutate: func(t *testing.T, _, artifactRoot string) {
				writeTestFile(t, artifactRoot, "untracked.dll", "binary")
			},
			wantString: "untracked native artifact file untracked.dll",
		},
		{
			name: "model-hash",
			mutate: func(t *testing.T, _, artifactRoot string) {
				writeTestFile(t, artifactRoot, "karte_data/model/model.onnx", "mutated")
			},
			wantString: "content/hash mismatch",
		},
		{
			name: "untracked-model",
			mutate: func(t *testing.T, _, artifactRoot string) {
				writeTestFile(t, artifactRoot, "karte_data/model/extra.onnx", "extra")
			},
			wantString: "untracked model artifact file karte_data/model/extra.onnx",
		},
		{
			name: "compliance-drift",
			mutate: func(t *testing.T, _, artifactRoot string) {
				writeTestFile(t, artifactRoot, "LICENSE", "not the repository license")
			},
			wantString: "does not match repository LICENSE",
		},
		{
			name: "missing-asset-record",
			mutate: func(t *testing.T, _, artifactRoot string) {
				path := filepath.Join(artifactRoot, "compliance", ArtifactAssetManifestName)
				var manifest ArtifactAssetManifest
				if err := LoadJSONFile(path, &manifest); err != nil {
					t.Fatal(err)
				}
				manifest.Files = manifest.Files[:1]
				writeJSONFile(t, path, manifest)
			},
			wantString: "is not covered by artifact asset manifest",
		},
		{
			name: "native-placeholder",
			mutate: func(t *testing.T, _, artifactRoot string) {
				path := filepath.Join(artifactRoot, "compliance", NativeBuildManifestName)
				var manifest NativeBuildManifest
				if err := LoadJSONFile(path, &manifest); err != nil {
					t.Fatal(err)
				}
				manifest.Packages[0].PackageVersion = "MSYS2 runner package"
				writeJSONFile(t, path, manifest)
			},
			wantString: "contains unresolved package metadata",
		},
		{
			name: "unapproved-package-source",
			mutate: func(t *testing.T, _, artifactRoot string) {
				path := filepath.Join(artifactRoot, "compliance", NativeBuildManifestName)
				var manifest NativeBuildManifest
				if err := LoadJSONFile(path, &manifest); err != nil {
					t.Fatal(err)
				}
				manifest.Packages[0].PackageSource = "https://evil.example/mingw-w64-x86_64-fixture-1.2.3-1-any.pkg.tar.zst"
				writeJSONFile(t, path, manifest)
			},
			wantString: "outside approved prefix",
		},
		{
			name: "wrong-native-license",
			mutate: func(t *testing.T, _, artifactRoot string) {
				writeTestFile(t, artifactRoot, "licenses/native/LICENSE", "SPDX-License-Identifier: MIT\n\nApache License\nVersion 2.0\n")
				path := filepath.Join(artifactRoot, "compliance", NativeBuildManifestName)
				var manifest NativeBuildManifest
				if err := LoadJSONFile(path, &manifest); err != nil {
					t.Fatal(err)
				}
				hash, _, err := hashRegularFile(artifactRoot, "licenses/native/LICENSE")
				if err != nil {
					t.Fatal(err)
				}
				manifest.Packages[0].LicenseSHA256 = hash
				writeJSONFile(t, path, manifest)
			},
			wantString: "license text identifies \"Apache-2.0\"，want declared \"MIT\"",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repositoryRoot, artifactRoot := writeArtifactFixture(t)
			test.mutate(t, repositoryRoot, artifactRoot)
			err := AuditArtifact(repositoryRoot, artifactRoot, "windows", time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC))
			if err == nil || !strings.Contains(err.Error(), test.wantString) {
				t.Fatalf("expected %q，got %v", test.wantString, err)
			}
		})
	}
}

func TestRecognizeLicenseBodyIgnoresClaimedSPDXAndRequiresGCCTexts(t *testing.T) {
	gplOrLater := `GNU GENERAL PUBLIC LICENSE
Version 3, 29 June 2007

This program is free software: you can redistribute it and/or modify it under
the terms of the GNU General Public License as published by the Free Software
Foundation, either version 3 of the License, or (at your option) any later version.
`
	gccException := `GCC RUNTIME LIBRARY EXCEPTION
Version 3.1, 31 March 2009
`
	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "forged MIT header does not override Apache body", text: "SPDX-License-Identifier: MIT\n\nApache License\nVersion 2.0\n", want: "Apache-2.0"},
		{name: "forged Apache header does not override MIT body", text: "SPDX-License-Identifier: Apache-2.0\n\n" + testMITLicense, want: "MIT"},
		{name: "GPL or later without exception", text: gplOrLater, want: "GPL-3.0-or-later"},
		{name: "GCC exception without GPL text", text: gccException, want: "NOASSERTION"},
		{name: "GPL or later with GCC exception", text: gplOrLater + "\n" + gccException, want: "GPL-3.0-or-later WITH GCC-exception-3.1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := recognizeLicenseBody([]byte(test.text)); got != test.want {
				t.Fatalf("recognizeLicenseBody() = %q，want %q", got, test.want)
			}
		})
	}
}

func TestAuditArtifactAcceptsOnlyManifestedConfinedNativeSymlinks(t *testing.T) {
	repositoryRoot, artifactRoot := writeArtifactFixture(t)
	if err := os.Rename(filepath.Join(artifactRoot, "native.dll"), filepath.Join(artifactRoot, "native.1.dll")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("native.1.dll", filepath.Join(artifactRoot, "native.dll")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	hash, size, err := hashRegularFile(artifactRoot, "native.1.dll")
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(artifactRoot, "compliance", NativeBuildManifestName)
	var manifest NativeBuildManifest
	if err := LoadJSONFile(manifestPath, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Packages[0].Files = []NativeBuildFile{
		{ArtifactPath: "native.1.dll", Bytes: size, SHA256: hash},
		{ArtifactPath: "native.dll", Bytes: size, SHA256: hash, SymlinkTarget: "native.1.dll"},
	}
	writeJSONFile(t, manifestPath, manifest)
	if err := AuditArtifact(repositoryRoot, artifactRoot, "windows", time.Now()); err != nil {
		t.Fatalf("manifested confined native symlink failed audit: %v", err)
	}
	if err := os.Remove(filepath.Join(artifactRoot, "native.dll")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../outside.dll", filepath.Join(artifactRoot, "native.dll")); err != nil {
		t.Fatal(err)
	}
	manifest.Packages[0].Files[1].SymlinkTarget = "../outside.dll"
	writeJSONFile(t, manifestPath, manifest)
	err = AuditArtifact(repositoryRoot, artifactRoot, "windows", time.Now())
	if err == nil || !strings.Contains(err.Error(), "escapes artifact root") {
		t.Fatalf("expected escaping native symlink failure，got %v", err)
	}
}

func TestAuditArtifactRejectsSymlinkedLicenseAncestor(t *testing.T) {
	repositoryRoot, artifactRoot := writeArtifactFixture(t)
	licenseDirectory := filepath.Join(artifactRoot, "licenses", "native")
	if err := os.RemoveAll(licenseDirectory); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "LICENSE"), []byte(testMITLicense), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, licenseDirectory); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	err := AuditArtifact(repositoryRoot, artifactRoot, "windows", time.Now())
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlinked license ancestor rejection，got %v", err)
	}
}

func TestArtifactAssetExclusionsRequireRepositoryPlatformContract(t *testing.T) {
	reason := "Linux artifacts intentionally omit this optional model and require user-provided data."
	inventory := AssetInventory{SchemaVersion: SchemaVersion, Collections: []AssetCollection{{
		ID: "asset:test-asr-model", Root: "sources/model",
		ArtifactExclusions: map[string]string{"linux": reason},
		Files:              []AssetFile{{Path: "sources/model/model.onnx", Bytes: 1, SHA256: strings.Repeat("a", 64)}},
	}}}
	linux := ArtifactAssetManifest{SchemaVersion: SchemaVersion, Platform: "linux", Exclusions: []ArtifactAssetExclusion{{
		ComponentID: "asset:test-asr-model", Reason: reason,
	}}}
	if violations := validateArtifactAssets(t.TempDir(), "linux", inventory, linux); len(violations) != 0 {
		t.Fatalf("registry-authorized Linux exclusion failed: %v", violations)
	}
	darwin := linux
	darwin.Platform = "darwin"
	if violations := validateArtifactAssets(t.TempDir(), "darwin", inventory, darwin); !containsViolation(violations, "not authorized") {
		t.Fatalf("self-declared macOS model exclusion should fail，got %v", violations)
	}
}

func writeArtifactFixture(t *testing.T) (string, string) {
	t.Helper()
	repositoryRoot := t.TempDir()
	artifactRoot := t.TempDir()

	writeTestFile(t, repositoryRoot, "LICENSE", testProjectMITLicense)
	writeTestFile(t, repositoryRoot, NoticesPath, "# notices\n")
	writeTestFile(t, repositoryRoot, SBOMPath, "{}\n")
	writeJSONFile(t, filepath.Join(repositoryRoot, filepath.FromSlash(ComponentInventoryPath)), struct {
		SchemaVersion int         `json:"schemaVersion"`
		Components    []Component `json:"components"`
	}{SchemaVersion: SchemaVersion, Components: []Component{}})
	writeTestFile(t, repositoryRoot, "sources/model/LICENSE", testMITLicense)
	writeTestFile(t, repositoryRoot, "sources/model/model.onnx", "model bytes")

	licenseHash, licenseBytes, err := hashRegularFile(repositoryRoot, "sources/model/LICENSE")
	if err != nil {
		t.Fatal(err)
	}
	modelHash, modelBytes, err := hashRegularFile(repositoryRoot, "sources/model/model.onnx")
	if err != nil {
		t.Fatal(err)
	}
	assetInventory := AssetInventory{SchemaVersion: SchemaVersion, Collections: []AssetCollection{{
		ID: "asset:test-asr-model", Name: "fixture model", Version: "1", License: "MIT",
		Source: "https://example.test/model/tree/immutable", Root: "sources/model",
		LicensePath: "sources/model/LICENSE", LicenseSHA256: licenseHash,
		Files: []AssetFile{
			{Path: "sources/model/LICENSE", Bytes: licenseBytes, SHA256: licenseHash},
			{Path: "sources/model/model.onnx", Bytes: modelBytes, SHA256: modelHash},
		},
	}}}
	writeJSONFile(t, filepath.Join(repositoryRoot, filepath.FromSlash(AssetInventoryPath)), assetInventory)
	writeJSONFile(t, filepath.Join(repositoryRoot, "compliance", "native-components.json"), NativeRegistry{
		SchemaVersion: SchemaVersion,
		Components: []Component{{
			ID: "native:test-windows", Type: "native", Name: "fixture native", License: "MIT", Scope: "runtime",
			Source: "https://example.test/native", DistributionPath: "native*.dll",
			Properties: map[string]string{
				"platform": "windows", "packageManager": "pacman", "packageName": "mingw-w64-x86_64-fixture",
				"packageSourcePrefix": "https://repo.msys2.org/mingw/mingw64/",
			},
		}},
	})
	writeJSONFile(t, filepath.Join(repositoryRoot, "compliance", "policy.json"), Policy{
		SchemaVersion: SchemaVersion, AllowedLicenses: []string{"MIT"},
		DeniedLicenseFamilies: []string{"GPL", "AGPL"}, ExceptionRequiredLicenseFamilies: []string{"LGPL"},
	})
	writeJSONFile(t, filepath.Join(repositoryRoot, "compliance", "license-exceptions.json"), ExceptionRegistry{SchemaVersion: SchemaVersion})

	for artifactPath, repositoryPath := range map[string]string{
		"LICENSE": "LICENSE", "THIRD_PARTY_NOTICES.md": NoticesPath, "bom.cdx.json": SBOMPath,
		"compliance/assets.json": AssetInventoryPath, "compliance/components.json": ComponentInventoryPath,
	} {
		data, err := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(repositoryPath)))
		if err != nil {
			t.Fatal(err)
		}
		writeTestBytes(t, artifactRoot, artifactPath, data)
	}
	writeTestFile(t, artifactRoot, "karte_data/model/LICENSE", testMITLicense)
	writeTestFile(t, artifactRoot, "karte_data/model/model.onnx", "model bytes")
	artifactAssetManifest := ArtifactAssetManifest{SchemaVersion: SchemaVersion, Platform: "windows", Files: []ArtifactAssetEntry{
		{ComponentID: "asset:test-asr-model", SourcePath: "sources/model/LICENSE", ArtifactPath: "karte_data/model/LICENSE", Bytes: licenseBytes, SHA256: licenseHash},
		{ComponentID: "asset:test-asr-model", SourcePath: "sources/model/model.onnx", ArtifactPath: "karte_data/model/model.onnx", Bytes: modelBytes, SHA256: modelHash},
	}}
	writeJSONFile(t, filepath.Join(artifactRoot, "compliance", ArtifactAssetManifestName), artifactAssetManifest)

	writeTestFile(t, artifactRoot, "native.dll", "native bytes")
	writeTestFile(t, artifactRoot, "licenses/native/LICENSE", testMITLicense)
	nativeHash, nativeBytes, err := hashRegularFile(artifactRoot, "native.dll")
	if err != nil {
		t.Fatal(err)
	}
	nativeLicenseHash, _, err := hashRegularFile(artifactRoot, "licenses/native/LICENSE")
	if err != nil {
		t.Fatal(err)
	}
	packageSource := "https://repo.msys2.org/mingw/mingw64/mingw-w64-x86_64-fixture-1.2.3-1-any.pkg.tar.zst"
	packageSourceHash := strings.Repeat("b", 64)
	packageMetadataPath := "compliance/packages/mingw-w64-x86_64-fixture.json"
	writeJSONFile(t, filepath.Join(artifactRoot, filepath.FromSlash(packageMetadataPath)), NativePackageMetadata{
		SchemaVersion: SchemaVersion, PackageQuery: "mingw-w64-x86_64-fixture 1.2.3-1",
		PackageSource: packageSource, PackageSourceSHA256: packageSourceHash,
	})
	packageMetadataHash, _, err := hashRegularFile(artifactRoot, packageMetadataPath)
	if err != nil {
		t.Fatal(err)
	}
	nativeManifest := NativeBuildManifest{SchemaVersion: SchemaVersion, Platform: "windows", Packages: []NativeBuildRecord{{
		ComponentID: "native:test-windows", PackageManager: "pacman",
		PackageName: "mingw-w64-x86_64-fixture", PackageVersion: "1.2.3-1",
		PackageSource: packageSource, PackageSourceSHA256: packageSourceHash,
		PackageQuery:        "mingw-w64-x86_64-fixture 1.2.3-1",
		PackageMetadataPath: packageMetadataPath, PackageMetadataSHA256: packageMetadataHash,
		LicensePath: "licenses/native/LICENSE", LicenseSHA256: nativeLicenseHash,
		Files: []NativeBuildFile{{ArtifactPath: "native.dll", Bytes: nativeBytes, SHA256: nativeHash}},
	}}}
	writeJSONFile(t, filepath.Join(artifactRoot, "compliance", NativeBuildManifestName), nativeManifest)
	return repositoryRoot, artifactRoot
}

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	data, err := MarshalDeterministicJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeTestFile(t *testing.T, root, relative, value string) {
	t.Helper()
	writeTestBytes(t, root, relative, []byte(value))
}

func writeTestBytes(t *testing.T, root, relative string, value []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, value, 0o644); err != nil {
		t.Fatal(err)
	}
}

func containsViolation(violations []string, substring string) bool {
	for _, violation := range violations {
		if strings.Contains(violation, substring) {
			return true
		}
	}
	return false
}
