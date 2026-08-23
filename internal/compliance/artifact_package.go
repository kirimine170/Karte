package compliance

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PackageArtifactAssets copies auditable source copies for frontend resources
// that Wails embeds，maps raw model files to their real artifact locations，and
// writes the deterministic artifact asset manifest consumed by AuditArtifact.
func PackageArtifactAssets(repositoryRoot, artifactRoot, platform string) error {
	if platform != "darwin" && platform != "windows" && platform != "linux" {
		return fmt.Errorf("unsupported artifact platform %q", platform)
	}
	var inventory AssetInventory
	if err := LoadJSONFile(filepath.Join(repositoryRoot, filepath.FromSlash(AssetInventoryPath)), &inventory); err != nil {
		return err
	}
	if inventory.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported asset inventory schemaVersion %d", inventory.SchemaVersion)
	}
	manifest := ArtifactAssetManifest{SchemaVersion: SchemaVersion, Platform: platform}
	evidenceRootRelative := filepath.ToSlash(filepath.Join(artifactComplianceRoot(platform), "asset-files"))
	evidenceRoot, err := confinedPath(artifactRoot, evidenceRootRelative)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(evidenceRoot); err != nil {
		return fmt.Errorf("clean artifact asset evidence: %w", err)
	}
	for _, collection := range inventory.Collections {
		if reason := collection.ArtifactExclusions[platform]; reason != "" {
			manifest.Exclusions = append(manifest.Exclusions, ArtifactAssetExclusion{ComponentID: collection.ID, Reason: reason})
			continue
		}
		isModel := strings.Contains(collection.ID, "asr-model")
		for _, file := range collection.Files {
			sourceHash, sourceBytes, err := hashRegularFile(repositoryRoot, file.Path)
			if err != nil {
				return fmt.Errorf("source asset %s: %w", file.Path, err)
			}
			if sourceHash != file.SHA256 || sourceBytes != file.Bytes {
				return fmt.Errorf("source asset %s does not match compliance/assets.json", file.Path)
			}
			artifactPath := filepath.ToSlash(filepath.Join(evidenceRootRelative, filepath.FromSlash(file.Path)))
			if isModel {
				artifactPath, err = modelArtifactPath(platform, file.Path)
				if err != nil {
					return fmt.Errorf("model asset %s: %w", file.Path, err)
				}
			} else if err := copyConfinedRegularFile(repositoryRoot, file.Path, artifactRoot, artifactPath); err != nil {
				return fmt.Errorf("package asset evidence %s: %w", file.Path, err)
			}
			artifactHash, artifactBytes, err := hashRegularFile(artifactRoot, artifactPath)
			if err != nil {
				return fmt.Errorf("packaged asset %s: %w", artifactPath, err)
			}
			if artifactHash != file.SHA256 || artifactBytes != file.Bytes {
				return fmt.Errorf("packaged asset %s differs from inventory", artifactPath)
			}
			manifest.Files = append(manifest.Files, ArtifactAssetEntry{
				ComponentID: collection.ID, SourcePath: file.Path, ArtifactPath: artifactPath,
				Bytes: file.Bytes, SHA256: file.SHA256,
			})
		}
	}
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].SourcePath < manifest.Files[j].SourcePath })
	sort.Slice(manifest.Exclusions, func(i, j int) bool { return manifest.Exclusions[i].ComponentID < manifest.Exclusions[j].ComponentID })
	manifestPath, err := confinedPath(artifactRoot, filepath.ToSlash(filepath.Join(artifactComplianceRoot(platform), ArtifactAssetManifestName)))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(manifestPath), 0o755); err != nil {
		return err
	}
	data, err := MarshalDeterministicJSON(manifest)
	if err != nil {
		return err
	}
	if err := os.WriteFile(manifestPath, data, 0o644); err != nil {
		return err
	}
	if platform == "linux" {
		if err := packageLinuxSystemRuntimeManifest(repositoryRoot, artifactRoot); err != nil {
			return err
		}
	}
	if violations := validateArtifactAssets(artifactRoot, platform, inventory, manifest); len(violations) > 0 {
		sort.Strings(violations)
		return fmt.Errorf("packaged artifact asset validation failed: %s", strings.Join(violations, "\n"))
	}
	return nil
}

func packageLinuxSystemRuntimeManifest(repositoryRoot, artifactRoot string) error {
	var registry NativeRegistry
	if err := LoadJSONFile(filepath.Join(repositoryRoot, "compliance", "native-components.json"), &registry); err != nil {
		return err
	}
	manifest := NativeBuildManifest{SchemaVersion: SchemaVersion, Platform: "linux"}
	for _, component := range registry.Components {
		if nativeComponentPlatform(component) != "linux" || component.Scope != "system-runtime" {
			continue
		}
		reason := component.Properties["artifactCoverage"]
		if strings.TrimSpace(reason) == "" {
			return fmt.Errorf("Linux system runtime %s has no artifactCoverage reason", component.ID)
		}
		manifest.SystemRuntimes = append(manifest.SystemRuntimes, NativeSystemRuntimeReference{ComponentID: component.ID, Reason: reason})
	}
	sort.Slice(manifest.SystemRuntimes, func(i, j int) bool {
		return manifest.SystemRuntimes[i].ComponentID < manifest.SystemRuntimes[j].ComponentID
	})
	data, err := MarshalDeterministicJSON(manifest)
	if err != nil {
		return err
	}
	path, err := confinedPath(artifactRoot, filepath.ToSlash(filepath.Join(artifactComplianceRoot("linux"), NativeBuildManifestName)))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func modelArtifactPath(platform, sourcePath string) (string, error) {
	const templatePrefix = "templates/karte_data_template/"
	if !strings.HasPrefix(sourcePath, templatePrefix) {
		return "", fmt.Errorf("model source is outside %s", templatePrefix)
	}
	relative := strings.TrimPrefix(sourcePath, templatePrefix)
	switch platform {
	case "darwin":
		return filepath.ToSlash(filepath.Join("Contents", "Resources", "karte_data_template", filepath.FromSlash(relative))), nil
	case "windows":
		return filepath.ToSlash(filepath.Join("karte_data", filepath.FromSlash(relative))), nil
	default:
		return "", errorsNewLinuxModelContract()
	}
}

func errorsNewLinuxModelContract() error {
	return fmt.Errorf("Linux model omission requires a registry artifactExclusions.linux reason")
}

func copyConfinedRegularFile(sourceRoot, sourceRelative, destinationRoot, destinationRelative string) error {
	source, err := confinedPath(sourceRoot, sourceRelative)
	if err != nil {
		return err
	}
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !sourceInfo.Mode().IsRegular() {
		return fmt.Errorf("source is not a regular file: %s", sourceRelative)
	}
	destination, err := confinedPath(destinationRoot, destinationRelative)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	if destinationInfo, err := os.Lstat(destination); err == nil && !destinationInfo.Mode().IsRegular() {
		return fmt.Errorf("destination is not a regular file: %s", destinationRelative)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
