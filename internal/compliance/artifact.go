package compliance

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	ArtifactAssetManifestName = "artifact-assets.json"
	NativeBuildManifestName   = "native-build.json"
)

type ArtifactAssetManifest struct {
	SchemaVersion int                      `json:"schemaVersion"`
	Platform      string                   `json:"platform"`
	Files         []ArtifactAssetEntry     `json:"files"`
	Exclusions    []ArtifactAssetExclusion `json:"exclusions,omitempty"`
}

type ArtifactAssetExclusion struct {
	ComponentID string `json:"componentId"`
	Reason      string `json:"reason"`
}

type ArtifactAssetEntry struct {
	ComponentID  string `json:"componentId"`
	SourcePath   string `json:"sourcePath"`
	ArtifactPath string `json:"artifactPath"`
	Bytes        int64  `json:"bytes"`
	SHA256       string `json:"sha256"`
}

type NativeBuildManifest struct {
	SchemaVersion  int                            `json:"schemaVersion"`
	Platform       string                         `json:"platform"`
	Packages       []NativeBuildRecord            `json:"packages"`
	SystemRuntimes []NativeSystemRuntimeReference `json:"systemRuntimes,omitempty"`
}

type NativeSystemRuntimeReference struct {
	ComponentID string `json:"componentId"`
	Reason      string `json:"reason"`
}

type NativeBuildRecord struct {
	ComponentID           string            `json:"componentId"`
	PackageManager        string            `json:"packageManager"`
	PackageName           string            `json:"packageName"`
	PackageVersion        string            `json:"packageVersion"`
	PackageSource         string            `json:"packageSource"`
	PackageSourceSHA256   string            `json:"packageSourceSha256,omitempty"`
	PackageQuery          string            `json:"packageQuery,omitempty"`
	PackageMetadataPath   string            `json:"packageMetadataPath,omitempty"`
	PackageMetadataSHA256 string            `json:"packageMetadataSha256,omitempty"`
	LicensePath           string            `json:"licensePath"`
	LicenseSHA256         string            `json:"licenseSha256"`
	Files                 []NativeBuildFile `json:"files"`
	Properties            map[string]string `json:"properties,omitempty"`
}

type NativeBuildFile struct {
	ArtifactPath  string `json:"artifactPath"`
	Bytes         int64  `json:"bytes"`
	SHA256        string `json:"sha256"`
	SymlinkTarget string `json:"symlinkTarget,omitempty"`
}

type NativePackageMetadata struct {
	SchemaVersion       int    `json:"schemaVersion"`
	PackageQuery        string `json:"packageQuery"`
	PackageSource       string `json:"packageSource"`
	PackageSourceSHA256 string `json:"packageSourceSha256"`
}

func AuditArtifact(repositoryRoot, artifactRoot, platform string, now time.Time) error {
	if platform != "darwin" && platform != "windows" && platform != "linux" {
		return fmt.Errorf("unsupported artifact platform %q", platform)
	}
	if err := validateProjectLicense(repositoryRoot); err != nil {
		return err
	}
	if _, err := confinedPath(artifactRoot, artifactComplianceRoot(platform)); err != nil {
		return fmt.Errorf("resolve artifact compliance directory: %w", err)
	}
	var violations []string
	violations = append(violations, verifyArtifactComplianceFiles(repositoryRoot, artifactRoot, platform)...)
	if err := validateArtifactComponentPolicy(repositoryRoot, now); err != nil {
		violations = append(violations, strings.Split(err.Error(), "\n")...)
	}

	var assets AssetInventory
	if err := LoadJSONFile(filepath.Join(repositoryRoot, filepath.FromSlash(AssetInventoryPath)), &assets); err != nil {
		return err
	}
	var assetManifest ArtifactAssetManifest
	assetManifestPath := filepath.Join(artifactComplianceRoot(platform), ArtifactAssetManifestName)
	assetManifestAbsolute, err := confinedPath(artifactRoot, assetManifestPath)
	if err != nil {
		return err
	}
	if err := LoadJSONFile(assetManifestAbsolute, &assetManifest); err != nil {
		violations = append(violations, fmt.Sprintf("artifact asset manifest is missing or invalid: %v", err))
	} else if assetErrors := validateArtifactAssets(artifactRoot, platform, assets, assetManifest); len(assetErrors) > 0 {
		violations = append(violations, assetErrors...)
	}

	nativeRegistry, err := LoadNativeRegistry(repositoryRoot)
	if err != nil {
		return err
	}
	var nativeManifest NativeBuildManifest
	nativeManifestPath := filepath.Join(artifactComplianceRoot(platform), NativeBuildManifestName)
	nativeManifestAbsolute, err := confinedPath(artifactRoot, nativeManifestPath)
	if err != nil {
		return err
	}
	if err := LoadJSONFile(nativeManifestAbsolute, &nativeManifest); err != nil {
		violations = append(violations, fmt.Sprintf("native build manifest is missing or invalid: %v", err))
	} else {
		resolved, nativeErrors := validateNativeBuildManifest(artifactRoot, platform, nativeRegistry, nativeManifest)
		violations = append(violations, nativeErrors...)
		var policy Policy
		if err := LoadJSONFile(filepath.Join(repositoryRoot, "compliance", "policy.json"), &policy); err != nil {
			return err
		}
		var exceptions ExceptionRegistry
		if err := LoadJSONFile(filepath.Join(repositoryRoot, "compliance", "license-exceptions.json"), &exceptions); err != nil {
			return err
		}
		if err := ValidateComponentLicenses(policy, exceptions, resolved, now); err != nil {
			violations = append(violations, strings.Split(err.Error(), "\n")...)
		}
		if err := validateRedistributionEvidence(policy, resolved); err != nil {
			violations = append(violations, strings.Split(err.Error(), "\n")...)
		}
	}
	if len(violations) == 0 {
		return nil
	}
	sort.Strings(violations)
	return errors.New(strings.Join(uniqueStrings(violations), "\n"))
}

func validateArtifactComponentPolicy(repositoryRoot string, now time.Time) error {
	var inventory struct {
		SchemaVersion int         `json:"schemaVersion"`
		Components    []Component `json:"components"`
	}
	if err := LoadJSONFile(filepath.Join(repositoryRoot, filepath.FromSlash(ComponentInventoryPath)), &inventory); err != nil {
		return fmt.Errorf("load component inventory for artifact policy: %w", err)
	}
	if inventory.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported component inventory schemaVersion %d", inventory.SchemaVersion)
	}
	var policy Policy
	if err := LoadJSONFile(filepath.Join(repositoryRoot, "compliance", "policy.json"), &policy); err != nil {
		return err
	}
	var exceptions ExceptionRegistry
	if err := LoadJSONFile(filepath.Join(repositoryRoot, "compliance", "license-exceptions.json"), &exceptions); err != nil {
		return err
	}
	return ValidateComponentLicenses(policy, exceptions, inventory.Components, now)
}

func artifactComplianceRoot(platform string) string {
	if platform == "darwin" {
		return "Contents/Resources/compliance"
	}
	return "compliance"
}

func artifactDocumentRoot(platform string) string {
	if platform == "darwin" {
		return "Contents/Resources"
	}
	return "."
}

func verifyArtifactComplianceFiles(repositoryRoot, artifactRoot, platform string) []string {
	documentRoot := artifactDocumentRoot(platform)
	complianceRoot := artifactComplianceRoot(platform)
	expected := map[string]string{
		filepath.ToSlash(filepath.Join(documentRoot, "LICENSE")):                "LICENSE",
		filepath.ToSlash(filepath.Join(documentRoot, "THIRD_PARTY_NOTICES.md")): NoticesPath,
		filepath.ToSlash(filepath.Join(documentRoot, "bom.cdx.json")):           SBOMPath,
		filepath.ToSlash(filepath.Join(complianceRoot, "assets.json")):          AssetInventoryPath,
		filepath.ToSlash(filepath.Join(complianceRoot, "components.json")):      ComponentInventoryPath,
	}
	var violations []string
	for artifactPath, repositoryPath := range expected {
		repositoryHash, repositoryBytes, err := hashRegularFile(repositoryRoot, repositoryPath)
		if err != nil {
			violations = append(violations, fmt.Sprintf("repository compliance file %s is unavailable: %v", repositoryPath, err))
			continue
		}
		artifactHash, artifactBytes, err := hashRegularFile(artifactRoot, artifactPath)
		if err != nil {
			violations = append(violations, fmt.Sprintf("artifact compliance file %s is unavailable: %v", artifactPath, err))
			continue
		}
		if artifactHash != repositoryHash || artifactBytes != repositoryBytes {
			violations = append(violations, fmt.Sprintf("artifact compliance file %s does not match repository %s", artifactPath, repositoryPath))
		}
	}
	return violations
}

func validateArtifactAssets(artifactRoot, platform string, inventory AssetInventory, manifest ArtifactAssetManifest) []string {
	var violations []string
	if manifest.SchemaVersion != SchemaVersion {
		return []string{fmt.Sprintf("unsupported artifact asset schemaVersion %d", manifest.SchemaVersion)}
	}
	if manifest.Platform != platform {
		return []string{fmt.Sprintf("artifact asset platform %q does not match %q", manifest.Platform, platform)}
	}
	expected := make(map[string]struct {
		component string
		file      AssetFile
	})
	modelRoots := make(map[string]string)
	knownComponents := make(map[string]struct{})
	allowedExclusions := make(map[string]string)
	for _, collection := range inventory.Collections {
		knownComponents[collection.ID] = struct{}{}
		if reason := collection.ArtifactExclusions[platform]; reason != "" {
			allowedExclusions[collection.ID] = reason
		}
		for _, file := range collection.Files {
			expected[file.Path] = struct {
				component string
				file      AssetFile
			}{component: collection.ID, file: file}
		}
		if strings.Contains(collection.ID, "asr-model") {
			modelRoots[collection.ID] = collection.Root
		}
	}
	excluded := make(map[string]struct{})
	for index, exclusion := range manifest.Exclusions {
		if _, ok := knownComponents[exclusion.ComponentID]; !ok {
			violations = append(violations, fmt.Sprintf("artifact asset exclusions[%d] references unknown component %s", index, exclusion.ComponentID))
			continue
		}
		allowedReason, allowed := allowedExclusions[exclusion.ComponentID]
		if !allowed || exclusion.Reason != allowedReason {
			violations = append(violations, fmt.Sprintf("artifact asset exclusions[%d] is not authorized by the repository platform contract", index))
			continue
		}
		if _, duplicate := excluded[exclusion.ComponentID]; duplicate {
			violations = append(violations, fmt.Sprintf("duplicate artifact exclusion for component %s", exclusion.ComponentID))
		}
		excluded[exclusion.ComponentID] = struct{}{}
	}
	seenSources := make(map[string]struct{})
	seenArtifacts := make(map[string]string)
	coveredModelFiles := make(map[string]map[string]struct{})
	modelArtifactRoots := make(map[string]string)
	for index, entry := range manifest.Files {
		prefix := fmt.Sprintf("artifact asset files[%d]", index)
		if err := validatePortableRelativePath(entry.SourcePath); err != nil {
			violations = append(violations, fmt.Sprintf("%s sourcePath: %v", prefix, err))
			continue
		}
		if err := validatePortableRelativePath(entry.ArtifactPath); err != nil {
			violations = append(violations, fmt.Sprintf("%s artifactPath: %v", prefix, err))
			continue
		}
		want, ok := expected[entry.SourcePath]
		if !ok {
			violations = append(violations, fmt.Sprintf("%s inventories unknown source %s", prefix, entry.SourcePath))
			continue
		}
		if _, isExcluded := excluded[want.component]; isExcluded {
			violations = append(violations, fmt.Sprintf("component %s is both mapped and excluded", want.component))
		}
		if _, duplicate := seenSources[entry.SourcePath]; duplicate {
			violations = append(violations, fmt.Sprintf("duplicate artifact mapping for source %s", entry.SourcePath))
			continue
		}
		seenSources[entry.SourcePath] = struct{}{}
		if owner, duplicate := seenArtifacts[entry.ArtifactPath]; duplicate {
			violations = append(violations, fmt.Sprintf("artifact path %s is mapped by both %s and %s", entry.ArtifactPath, owner, entry.SourcePath))
			continue
		}
		seenArtifacts[entry.ArtifactPath] = entry.SourcePath
		if entry.ComponentID != want.component || entry.SHA256 != want.file.SHA256 || entry.Bytes != want.file.Bytes {
			violations = append(violations, fmt.Sprintf("%s metadata does not match repository inventory for %s", prefix, entry.SourcePath))
		}
		hash, size, err := hashRegularFile(artifactRoot, entry.ArtifactPath)
		if err != nil {
			violations = append(violations, fmt.Sprintf("artifact asset %s is unavailable: %v", entry.ArtifactPath, err))
		} else if hash != entry.SHA256 || size != entry.Bytes {
			violations = append(violations, fmt.Sprintf("artifact asset %s content/hash mismatch", entry.ArtifactPath))
		}
		if sourceRoot, isModel := modelRoots[entry.ComponentID]; isModel {
			relative, relErr := filepath.Rel(filepath.FromSlash(sourceRoot), filepath.FromSlash(entry.SourcePath))
			if relErr != nil || relative == "." || strings.HasPrefix(relative, "..") {
				violations = append(violations, fmt.Sprintf("model source %s escapes registered root %s", entry.SourcePath, sourceRoot))
				continue
			}
			artifactSuffix := filepath.ToSlash(relative)
			if !strings.HasSuffix(entry.ArtifactPath, artifactSuffix) {
				violations = append(violations, fmt.Sprintf("model artifact path %s does not preserve source relative path %s", entry.ArtifactPath, artifactSuffix))
				continue
			}
			artifactModelRoot := strings.TrimSuffix(entry.ArtifactPath, artifactSuffix)
			artifactModelRoot = strings.TrimSuffix(artifactModelRoot, "/")
			if prior := modelArtifactRoots[entry.ComponentID]; prior != "" && prior != artifactModelRoot {
				violations = append(violations, fmt.Sprintf("model component %s maps to multiple artifact roots", entry.ComponentID))
			} else {
				modelArtifactRoots[entry.ComponentID] = artifactModelRoot
			}
			if coveredModelFiles[entry.ComponentID] == nil {
				coveredModelFiles[entry.ComponentID] = make(map[string]struct{})
			}
			coveredModelFiles[entry.ComponentID][entry.ArtifactPath] = struct{}{}
		}
	}
	for sourcePath, want := range expected {
		if _, ok := seenSources[sourcePath]; !ok {
			if _, isExcluded := excluded[want.component]; isExcluded {
				continue
			}
			violations = append(violations, fmt.Sprintf("repository asset %s is not covered by artifact asset manifest", sourcePath))
		}
	}
	evidenceRootRelative := filepath.ToSlash(filepath.Join(artifactComplianceRoot(platform), "asset-files"))
	evidenceRoot, evidenceRootErr := confinedPath(artifactRoot, evidenceRootRelative)
	if evidenceRootErr == nil {
		if _, statErr := os.Stat(evidenceRoot); statErr == nil {
			absoluteArtifactRoot, absoluteErr := filepath.Abs(artifactRoot)
			if absoluteErr != nil {
				violations = append(violations, fmt.Sprintf("make artifact root absolute: %v", absoluteErr))
				absoluteArtifactRoot = artifactRoot
			}
			canonicalArtifactRoot, resolveErr := filepath.EvalSymlinks(absoluteArtifactRoot)
			if resolveErr != nil {
				violations = append(violations, fmt.Sprintf("resolve artifact root: %v", resolveErr))
				canonicalArtifactRoot = absoluteArtifactRoot
			}
			walkErr := filepath.WalkDir(evidenceRoot, func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if entry.Type()&os.ModeSymlink != 0 {
					return fmt.Errorf("artifact asset evidence symlink is not allowed: %s", path)
				}
				if entry.IsDir() {
					return nil
				}
				relative, err := filepath.Rel(canonicalArtifactRoot, path)
				if err != nil {
					return err
				}
				relative = filepath.ToSlash(relative)
				if _, covered := seenArtifacts[relative]; !covered {
					return fmt.Errorf("untracked artifact asset evidence file %s", relative)
				}
				return nil
			})
			if walkErr != nil {
				violations = append(violations, walkErr.Error())
			}
		}
	} else {
		violations = append(violations, fmt.Sprintf("artifact asset evidence root: %v", evidenceRootErr))
	}
	for componentID, modelRoot := range modelArtifactRoots {
		absolute, err := confinedPath(artifactRoot, modelRoot)
		if err != nil {
			violations = append(violations, fmt.Sprintf("model artifact root %s: %v", modelRoot, err))
			continue
		}
		err = filepath.WalkDir(absolute, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlink is not allowed: %s", path)
			}
			if entry.IsDir() {
				return nil
			}
			relative, err := filepath.Rel(absolute, path)
			if err != nil {
				return err
			}
			artifactPath := filepath.ToSlash(filepath.Join(filepath.FromSlash(modelRoot), relative))
			if _, ok := coveredModelFiles[componentID][artifactPath]; !ok {
				return fmt.Errorf("untracked model artifact file %s", artifactPath)
			}
			return nil
		})
		if err != nil {
			violations = append(violations, err.Error())
		}
	}
	return violations
}

func validateNativeBuildManifest(artifactRoot, platform string, registry NativeRegistry, manifest NativeBuildManifest) ([]Component, []string) {
	var violations []string
	if manifest.SchemaVersion != SchemaVersion {
		return nil, []string{fmt.Sprintf("unsupported native build schemaVersion %d", manifest.SchemaVersion)}
	}
	if manifest.Platform != platform {
		return nil, []string{fmt.Sprintf("native build platform %q does not match %q", manifest.Platform, platform)}
	}
	requirements := make(map[string]Component)
	systemRequirements := make(map[string]Component)
	for _, component := range registry.Components {
		if nativeComponentPlatform(component) != platform {
			continue
		}
		if component.Scope == "system-runtime" {
			systemRequirements[component.ID] = component
		} else {
			requirements[component.ID] = component
		}
	}
	seenSystem := make(map[string]struct{})
	for index, reference := range manifest.SystemRuntimes {
		requirement, ok := systemRequirements[reference.ComponentID]
		if !ok {
			violations = append(violations, fmt.Sprintf("native systemRuntimes[%d] references unknown or bundled component %s", index, reference.ComponentID))
			continue
		}
		if _, duplicate := seenSystem[reference.ComponentID]; duplicate {
			violations = append(violations, fmt.Sprintf("duplicate system runtime reference for %s", reference.ComponentID))
		}
		seenSystem[reference.ComponentID] = struct{}{}
		if reference.Reason == "" || reference.Reason != requirement.Properties["artifactCoverage"] {
			violations = append(violations, fmt.Sprintf("system runtime %s reason does not match registry artifactCoverage", reference.ComponentID))
		}
	}
	for componentID := range systemRequirements {
		if _, ok := seenSystem[componentID]; !ok {
			violations = append(violations, fmt.Sprintf("system runtime %s is not explicitly separated from artifact coverage", componentID))
		}
	}
	seen := make(map[string]struct{})
	coveredNativeFiles := make(map[string]struct{})
	nativeFileOwners := make(map[string]string)
	symlinkTargets := make(map[string]string)
	var resolved []Component
	for index, record := range manifest.Packages {
		prefix := fmt.Sprintf("native packages[%d]", index)
		requirement, ok := requirements[record.ComponentID]
		if !ok {
			violations = append(violations, fmt.Sprintf("%s references unknown or wrong-platform component %s", prefix, record.ComponentID))
			continue
		}
		if _, duplicate := seen[record.ComponentID]; duplicate {
			violations = append(violations, fmt.Sprintf("duplicate native build record for %s", record.ComponentID))
			continue
		}
		seen[record.ComponentID] = struct{}{}
		for field, value := range map[string]string{
			"packageManager": record.PackageManager, "packageName": record.PackageName,
			"packageVersion": record.PackageVersion, "packageSource": record.PackageSource,
			"licensePath": record.LicensePath, "licenseSha256": record.LicenseSHA256,
		} {
			if strings.TrimSpace(value) == "" {
				violations = append(violations, fmt.Sprintf("%s.%s is missing", prefix, field))
			}
		}
		if containsResolutionPlaceholder(record.PackageVersion) || containsResolutionPlaceholder(record.PackageSource) {
			violations = append(violations, fmt.Sprintf("%s contains unresolved package metadata", prefix))
		}
		if !strings.HasPrefix(record.PackageSource, "https://") {
			violations = append(violations, fmt.Sprintf("%s packageSource must be an immutable https source", prefix))
		}
		if platform == "windows" && record.PackageManager == "pacman" && !strings.Contains(record.PackageName, "mingw-w64-") {
			violations = append(violations, fmt.Sprintf("%s pacman packageName must be the exact MinGW package", prefix))
		}
		if requiredManager := requirement.Properties["packageManager"]; requiredManager != "" && record.PackageManager != requiredManager {
			violations = append(violations, fmt.Sprintf("%s packageManager %q does not match requirement %q", prefix, record.PackageManager, requiredManager))
		}
		if requiredName := requirement.Properties["packageName"]; requiredName != "" && record.PackageName != requiredName {
			violations = append(violations, fmt.Sprintf("%s packageName %q does not match requirement %q", prefix, record.PackageName, requiredName))
		}
		if requiredSource := requirement.Properties["packageSource"]; requiredSource != "" && record.PackageSource != requiredSource {
			violations = append(violations, fmt.Sprintf("%s packageSource %q does not match pinned requirement %q", prefix, record.PackageSource, requiredSource))
		}
		if requiredPrefix := requirement.Properties["packageSourcePrefix"]; requiredPrefix != "" && !strings.HasPrefix(record.PackageSource, requiredPrefix) {
			violations = append(violations, fmt.Sprintf("%s packageSource %q is outside approved prefix %q", prefix, record.PackageSource, requiredPrefix))
		}
		if requirement.Properties["packageManager"] == "source-archive" {
			if record.PackageSourceSHA256 != requirement.Hashes["SHA-256"] {
				violations = append(violations, fmt.Sprintf("%s source archive checksum does not match pinned registry SHA-256", prefix))
			}
		}
		if requirement.Version != "" && record.PackageVersion != requirement.Version {
			violations = append(violations, fmt.Sprintf("%s packageVersion %q does not match pinned requirement %q", prefix, record.PackageVersion, requirement.Version))
		}
		if record.PackageManager == "pacman" {
			if record.PackageQuery != record.PackageName+" "+record.PackageVersion {
				violations = append(violations, fmt.Sprintf("%s packageQuery must equal exact pacman -Q output", prefix))
			}
			if !sha256Pattern.MatchString(record.PackageSourceSHA256) {
				violations = append(violations, fmt.Sprintf("%s packageSourceSha256 must capture the exact package archive checksum", prefix))
			}
			parsedSource, sourceErr := url.Parse(record.PackageSource)
			sourceFile := ""
			if sourceErr == nil {
				sourceFile = filepath.Base(parsedSource.Path)
			}
			if sourceErr != nil || !strings.Contains(sourceFile, record.PackageName) || !strings.Contains(sourceFile, record.PackageVersion) {
				violations = append(violations, fmt.Sprintf("%s packageSource must name the exact package and version", prefix))
			}
			if !sha256Pattern.MatchString(record.PackageMetadataSHA256) || record.PackageMetadataPath == "" {
				violations = append(violations, fmt.Sprintf("%s packageMetadataSha256 must hash the captured package database record", prefix))
			} else {
				metadataHash, _, metadataHashErr := hashRegularFile(artifactRoot, record.PackageMetadataPath)
				if metadataHashErr != nil || metadataHash != record.PackageMetadataSHA256 {
					violations = append(violations, fmt.Sprintf("%s captured package metadata file/hash mismatch", prefix))
				} else {
					metadataPath, pathErr := confinedPath(artifactRoot, record.PackageMetadataPath)
					var metadata NativePackageMetadata
					if pathErr != nil {
						violations = append(violations, fmt.Sprintf("%s package metadata path: %v", prefix, pathErr))
					} else if metadataErr := LoadJSONFile(metadataPath, &metadata); metadataErr != nil {
						violations = append(violations, fmt.Sprintf("%s package metadata JSON: %v", prefix, metadataErr))
					} else if metadata.SchemaVersion != SchemaVersion || metadata.PackageQuery != record.PackageQuery || metadata.PackageSource != record.PackageSource || metadata.PackageSourceSHA256 != record.PackageSourceSHA256 {
						violations = append(violations, fmt.Sprintf("%s captured package metadata does not match manifest", prefix))
					}
				}
			}
		}
		licenseHash, _, licenseErr := hashRegularFile(artifactRoot, record.LicensePath)
		if licenseErr != nil {
			violations = append(violations, fmt.Sprintf("%s license is unavailable: %v", prefix, licenseErr))
		} else if licenseHash != record.LicenseSHA256 {
			violations = append(violations, fmt.Sprintf("%s license hash mismatch", prefix))
		}
		licenseText, readErr := readAndNormalizeArtifactFile(artifactRoot, record.LicensePath)
		if readErr != nil {
			violations = append(violations, fmt.Sprintf("%s license text: %v", prefix, readErr))
		} else if recognized := recognizeLicenseBody([]byte(licenseText)); recognized != requirement.License {
			violations = append(violations, fmt.Sprintf("%s license text identifies %q，want declared %q", prefix, recognized, requirement.License))
		}
		if len(record.Files) == 0 {
			violations = append(violations, fmt.Sprintf("%s files must not be empty", prefix))
		}
		aggregateParts := make([]string, 0, len(record.Files))
		for fileIndex, file := range record.Files {
			filePrefix := fmt.Sprintf("%s.files[%d]", prefix, fileIndex)
			if err := validatePortableRelativePath(file.ArtifactPath); err != nil {
				violations = append(violations, fmt.Sprintf("%s: %v", filePrefix, err))
				continue
			}
			matched, matchErr := filepath.Match(filepath.FromSlash(requirement.DistributionPath), filepath.FromSlash(file.ArtifactPath))
			if matchErr != nil || !matched {
				violations = append(violations, fmt.Sprintf("%s path %s does not match requirement %s", filePrefix, file.ArtifactPath, requirement.DistributionPath))
			}
			if _, duplicate := coveredNativeFiles[file.ArtifactPath]; duplicate {
				violations = append(violations, fmt.Sprintf("native artifact file %s has multiple owners", file.ArtifactPath))
			}
			coveredNativeFiles[file.ArtifactPath] = struct{}{}
			nativeFileOwners[file.ArtifactPath] = record.ComponentID
			finalPath, hash, size, isSymlink, err := inspectNativeBuildFile(artifactRoot, file)
			if err != nil {
				violations = append(violations, fmt.Sprintf("%s is unavailable: %v", filePrefix, err))
			} else if hash != file.SHA256 || size != file.Bytes {
				violations = append(violations, fmt.Sprintf("%s content/hash mismatch", filePrefix))
			}
			if isSymlink {
				symlinkTargets[file.ArtifactPath] = finalPath
			}
			aggregateParts = append(aggregateParts, file.ArtifactPath+"\x00"+file.SymlinkTarget+"\x00"+fmt.Sprint(file.Bytes)+"\x00"+file.SHA256)
		}
		sort.Strings(aggregateParts)
		resolvedComponent := requirement
		resolvedComponent.Version = record.PackageVersion
		resolvedComponent.Hashes = map[string]string{"SHA-256": sha256Hex([]byte(strings.Join(aggregateParts, "\n")))}
		resolvedComponent.Properties = cloneStringMap(requirement.Properties)
		resolvedComponent.Properties["artifactResolved"] = "true"
		resolvedComponent.Properties["packageManager"] = record.PackageManager
		resolvedComponent.Properties["packageName"] = record.PackageName
		resolvedComponent.Properties["packageSource"] = record.PackageSource
		resolvedComponent.LicenseEvidence = []LicenseEvidence{{
			Kind: "license", Path: record.LicensePath,
			SHA256: sha256Hex([]byte(licenseText)), Text: licenseText,
		}}
		resolved = append(resolved, resolvedComponent)
	}
	for linkPath, targetPath := range symlinkTargets {
		targetOwner, covered := nativeFileOwners[targetPath]
		if !covered {
			violations = append(violations, fmt.Sprintf("native symlink %s resolves to unmanifested target %s", linkPath, targetPath))
			continue
		}
		if targetOwner != nativeFileOwners[linkPath] {
			violations = append(violations, fmt.Sprintf("native symlink %s crosses component ownership to %s", linkPath, targetPath))
		}
	}
	for componentID := range requirements {
		if _, ok := seen[componentID]; !ok {
			violations = append(violations, fmt.Sprintf("native component %s has no build-time package/hash/license record", componentID))
		}
	}
	violations = append(violations, findUntrackedNativeFiles(artifactRoot, coveredNativeFiles)...)
	sortComponents(resolved)
	return resolved, violations
}

func nativeComponentPlatform(component Component) string {
	if platform := component.Properties["platform"]; platform != "" {
		return platform
	}
	switch {
	case strings.HasSuffix(component.ID, "-macos"):
		return "darwin"
	case strings.HasSuffix(component.ID, "-windows"):
		return "windows"
	case strings.HasSuffix(component.ID, "-linux-system"):
		return "linux"
	default:
		return ""
	}
}

func inspectNativeBuildFile(artifactRoot string, record NativeBuildFile) (string, string, int64, bool, error) {
	path, info, err := lstatArtifactPath(artifactRoot, record.ArtifactPath)
	if err != nil {
		return "", "", 0, false, err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		if record.SymlinkTarget != "" {
			return "", "", 0, false, errors.New("manifest declares symlinkTarget for a regular file")
		}
		hash, size, err := hashRegularFile(artifactRoot, record.ArtifactPath)
		return record.ArtifactPath, hash, size, false, err
	}
	actualTarget, err := os.Readlink(path)
	if err != nil {
		return "", "", 0, true, err
	}
	if record.SymlinkTarget == "" || actualTarget != record.SymlinkTarget {
		return "", "", 0, true, fmt.Errorf("symlink target %q does not match manifest %q", actualTarget, record.SymlinkTarget)
	}
	current := record.ArtifactPath
	for depth := 0; depth < 32; depth++ {
		currentPath, currentInfo, err := lstatArtifactPath(artifactRoot, current)
		if err != nil {
			return "", "", 0, true, err
		}
		if currentInfo.Mode()&os.ModeSymlink == 0 {
			hash, size, err := hashRegularFile(artifactRoot, current)
			return current, hash, size, true, err
		}
		target, err := os.Readlink(currentPath)
		if err != nil {
			return "", "", 0, true, err
		}
		if filepath.IsAbs(target) {
			return "", "", 0, true, fmt.Errorf("absolute native symlink target is not allowed: %s", target)
		}
		next := filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(filepath.FromSlash(current)), target)))
		if err := validatePortableRelativePath(next); err != nil {
			return "", "", 0, true, fmt.Errorf("native symlink escapes artifact root: %w", err)
		}
		current = next
	}
	return "", "", 0, true, errors.New("native symlink chain exceeds 32 links")
}

func lstatArtifactPath(root, relative string) (string, fs.FileInfo, error) {
	if err := validatePortableRelativePath(relative); err != nil {
		return "", nil, err
	}
	directory := filepath.ToSlash(filepath.Dir(filepath.FromSlash(relative)))
	var parent string
	var err error
	if directory == "." {
		parent, err = filepath.EvalSymlinks(root)
	} else {
		parent, err = confinedPath(root, directory)
	}
	if err != nil {
		return "", nil, err
	}
	path := filepath.Join(parent, filepath.Base(filepath.FromSlash(relative)))
	info, err := os.Lstat(path)
	return path, info, err
}

func findUntrackedNativeFiles(artifactRoot string, covered map[string]struct{}) []string {
	root, err := filepath.EvalSymlinks(artifactRoot)
	if err != nil {
		return []string{fmt.Sprintf("resolve artifact root: %v", err)}
	}
	var violations []string
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !isNativeLibrary(entry.Name()) {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if _, ok := covered[relative]; !ok {
			if entry.Type()&os.ModeSymlink != 0 {
				violations = append(violations, fmt.Sprintf("untracked native artifact symlink %s", relative))
			} else {
				violations = append(violations, fmt.Sprintf("untracked native artifact file %s", relative))
			}
		}
		return nil
	})
	if err != nil {
		violations = append(violations, err.Error())
	}
	return violations
}

func isNativeLibrary(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".dll") || strings.HasSuffix(lower, ".dylib") ||
		strings.HasSuffix(lower, ".so") || strings.Contains(lower, ".so.")
}

func readAndNormalizeArtifactFile(root, relative string) (string, error) {
	path, err := confinedPath(root, relative)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return normalizeText(data)
}

func validatePortableRelativePath(value string) error {
	if strings.TrimSpace(value) == "" || filepath.IsAbs(value) || strings.Contains(value, "\\") || strings.Contains(value, ":") {
		return fmt.Errorf("path must be a non-empty portable relative path: %q", value)
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	if clean != value || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("path escapes or is not clean: %q", value)
	}
	return nil
}

func containsResolutionPlaceholder(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{"placeholder", "runner package", "must be captured", "unresolved", "unknown", "latest"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func cloneStringMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source)+4)
	for key, value := range source {
		result[key] = value
	}
	return result
}
