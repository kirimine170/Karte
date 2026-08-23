package compliance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type NPMOverrideRegistry struct {
	SchemaVersion int                  `json:"schemaVersion"`
	Overrides     []NPMLicenseOverride `json:"overrides"`
}

type NPMLicenseOverride struct {
	Path           string `json:"path"`
	Version        string `json:"version"`
	License        string `json:"license"`
	EvidencePath   string `json:"evidencePath"`
	EvidenceSHA256 string `json:"evidenceSha256"`
	EvidenceSource string `json:"evidenceSource"`
}

type AssetSourceRegistry struct {
	SchemaVersion int           `json:"schemaVersion"`
	Collections   []AssetSource `json:"collections"`
}

type AssetSource struct {
	ID                 string            `json:"id"`
	Name               string            `json:"name"`
	Version            string            `json:"version"`
	License            string            `json:"license"`
	Source             string            `json:"source"`
	SecondarySource    string            `json:"secondarySource,omitempty"`
	Root               string            `json:"root"`
	Include            []string          `json:"include,omitempty"`
	LicensePath        string            `json:"licensePath"`
	ArtifactExclusions map[string]string `json:"artifactExclusions,omitempty"`
}

type AssetInventory struct {
	SchemaVersion int               `json:"schemaVersion"`
	Collections   []AssetCollection `json:"collections"`
}

type AssetCollection struct {
	ID                 string            `json:"id"`
	Name               string            `json:"name"`
	Version            string            `json:"version"`
	License            string            `json:"license"`
	Source             string            `json:"source"`
	SecondarySource    string            `json:"secondarySource,omitempty"`
	Root               string            `json:"root"`
	LicensePath        string            `json:"licensePath"`
	LicenseSHA256      string            `json:"licenseSha256"`
	Files              []AssetFile       `json:"files"`
	ArtifactExclusions map[string]string `json:"artifactExclusions,omitempty"`
}

type AssetFile struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type NativeRegistry struct {
	SchemaVersion  int                   `json:"schemaVersion"`
	Components     []Component           `json:"components"`
	LicenseSources []NativeLicenseSource `json:"licenseSources,omitempty"`
}

type NativeLicenseSource struct {
	ComponentID string `json:"componentId"`
	Path        string `json:"path"`
	SHA256      string `json:"sha256"`
	Source      string `json:"source"`
}

func LoadJSONFile(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func LoadNativeRegistry(repositoryRoot string) (NativeRegistry, error) {
	var registry NativeRegistry
	registryPath, err := confinedPath(repositoryRoot, "compliance/native-components.json")
	if err != nil {
		return NativeRegistry{}, err
	}
	if err := LoadJSONFile(registryPath, &registry); err != nil {
		return NativeRegistry{}, err
	}
	if registry.SchemaVersion != SchemaVersion {
		return NativeRegistry{}, fmt.Errorf("unsupported native schemaVersion %d", registry.SchemaVersion)
	}
	components := make(map[string]*Component, len(registry.Components))
	for index := range registry.Components {
		component := &registry.Components[index]
		if _, duplicate := components[component.ID]; duplicate {
			return NativeRegistry{}, fmt.Errorf("duplicate native component %q", component.ID)
		}
		components[component.ID] = component
	}
	seenSources := make(map[string]struct{}, len(registry.LicenseSources))
	for index, source := range registry.LicenseSources {
		prefix := fmt.Sprintf("native licenseSources[%d]", index)
		component := components[source.ComponentID]
		if component == nil {
			return NativeRegistry{}, fmt.Errorf("%s references unknown component %q", prefix, source.ComponentID)
		}
		if _, duplicate := seenSources[source.ComponentID]; duplicate {
			return NativeRegistry{}, fmt.Errorf("%s duplicates component %q", prefix, source.ComponentID)
		}
		seenSources[source.ComponentID] = struct{}{}
		if err := validatePortableRelativePath(source.Path); err != nil {
			return NativeRegistry{}, fmt.Errorf("%s path: %w", prefix, err)
		}
		if !sha256Pattern.MatchString(source.SHA256) {
			return NativeRegistry{}, fmt.Errorf("%s has invalid normalized SHA-256", prefix)
		}
		parsedSource, parseErr := url.Parse(source.Source)
		if parseErr != nil || parsedSource.Scheme != "https" || parsedSource.Host == "" || containsResolutionPlaceholder(source.Source) {
			return NativeRegistry{}, fmt.Errorf("%s has invalid or unpinned https source %q", prefix, source.Source)
		}
		absolute, err := confinedPath(repositoryRoot, source.Path)
		if err != nil {
			return NativeRegistry{}, fmt.Errorf("%s: %w", prefix, err)
		}
		if _, _, err := hashRegularFile(repositoryRoot, source.Path); err != nil {
			return NativeRegistry{}, fmt.Errorf("%s evidence: %w", prefix, err)
		}
		data, err := os.ReadFile(absolute)
		if err != nil {
			return NativeRegistry{}, fmt.Errorf("%s evidence: %w", prefix, err)
		}
		normalized, err := normalizeText(data)
		if err != nil {
			return NativeRegistry{}, fmt.Errorf("%s normalize evidence: %w", prefix, err)
		}
		if actual := sha256Hex([]byte(normalized)); actual != source.SHA256 {
			return NativeRegistry{}, fmt.Errorf("%s normalized evidence SHA-256 mismatch", prefix)
		}
		detected := recognizeLicenseBody([]byte(normalized))
		if !equivalentLicenseExpressions(detected, component.License) {
			return NativeRegistry{}, fmt.Errorf("%s evidence identifies %q，want %q", prefix, detected, component.License)
		}
		component.LicenseEvidence = append(component.LicenseEvidence, LicenseEvidence{
			Kind: "license", Path: source.Path, SHA256: source.SHA256, Text: normalized,
		})
		if component.Properties == nil {
			component.Properties = make(map[string]string)
		}
		component.Properties["licenseEvidenceSource"] = source.Source
	}
	return registry, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("unexpected trailing JSON value")
	}
	return err
}

func BuildAssetInventory(repositoryRoot string, registry AssetSourceRegistry) (AssetInventory, error) {
	if registry.SchemaVersion != SchemaVersion {
		return AssetInventory{}, fmt.Errorf("unsupported asset source schemaVersion %d", registry.SchemaVersion)
	}
	inventory := AssetInventory{SchemaVersion: SchemaVersion}
	seenCollections := make(map[string]struct{}, len(registry.Collections))
	ownedFiles := make(map[string]string)
	for _, source := range registry.Collections {
		if err := validateAssetSource(source); err != nil {
			return AssetInventory{}, err
		}
		if _, ok := seenCollections[source.ID]; ok {
			return AssetInventory{}, fmt.Errorf("duplicate asset collection %q", source.ID)
		}
		seenCollections[source.ID] = struct{}{}

		licenseHash, _, err := hashRegularFile(repositoryRoot, source.LicensePath)
		if err != nil {
			return AssetInventory{}, fmt.Errorf("asset collection %s license: %w", source.ID, err)
		}
		collection := AssetCollection{
			ID:                 source.ID,
			Name:               source.Name,
			Version:            source.Version,
			License:            source.License,
			Source:             source.Source,
			SecondarySource:    source.SecondarySource,
			Root:               source.Root,
			LicensePath:        source.LicensePath,
			LicenseSHA256:      licenseHash,
			ArtifactExclusions: cloneStringMap(source.ArtifactExclusions),
		}
		rootPath, err := confinedPath(repositoryRoot, source.Root)
		if err != nil {
			return AssetInventory{}, fmt.Errorf("asset collection %s: %w", source.ID, err)
		}
		err = filepath.WalkDir(rootPath, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if path == rootPath {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("asset symlink is not allowed: %s", path)
			}
			if entry.IsDir() {
				return nil
			}
			relativeWithinCollection, err := filepath.Rel(rootPath, path)
			if err != nil {
				return err
			}
			relative := filepath.ToSlash(filepath.Join(filepath.FromSlash(source.Root), relativeWithinCollection))
			if !matchesAny(filepath.Base(path), source.Include) {
				return nil
			}
			if owner, ok := ownedFiles[relative]; ok {
				return fmt.Errorf("asset file %s is owned by both %s and %s", relative, owner, source.ID)
			}
			ownedFiles[relative] = source.ID
			hash, size, err := hashRegularFile(repositoryRoot, relative)
			if err != nil {
				return err
			}
			collection.Files = append(collection.Files, AssetFile{Path: relative, Bytes: size, SHA256: hash})
			return nil
		})
		if err != nil {
			return AssetInventory{}, fmt.Errorf("scan asset collection %s: %w", source.ID, err)
		}
		if len(collection.Files) == 0 {
			return AssetInventory{}, fmt.Errorf("asset collection %s contains no files", source.ID)
		}
		sort.Slice(collection.Files, func(i, j int) bool { return collection.Files[i].Path < collection.Files[j].Path })
		inventory.Collections = append(inventory.Collections, collection)
	}
	sort.Slice(inventory.Collections, func(i, j int) bool { return inventory.Collections[i].ID < inventory.Collections[j].ID })
	return inventory, nil
}

func ValidateAssetInventory(repositoryRoot string, registry AssetSourceRegistry, expected AssetInventory) error {
	actual, err := BuildAssetInventory(repositoryRoot, registry)
	if err != nil {
		return err
	}
	expectedJSON, err := MarshalDeterministicJSON(expected)
	if err != nil {
		return err
	}
	actualJSON, err := MarshalDeterministicJSON(actual)
	if err != nil {
		return err
	}
	if string(expectedJSON) != string(actualJSON) {
		return errors.New("compliance/assets.json is stale; run licensegate generate")
	}
	return nil
}

func MarshalDeterministicJSON(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func validateAssetSource(source AssetSource) error {
	for field, value := range map[string]string{
		"id": source.ID, "name": source.Name, "version": source.Version,
		"license": source.License, "source": source.Source, "root": source.Root,
		"licensePath": source.LicensePath,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("asset source %s must not be empty", field)
		}
	}
	if !strings.HasPrefix(source.Source, "https://") {
		return fmt.Errorf("asset source %s must use an https source URL", source.ID)
	}
	if source.SecondarySource != "" && !strings.HasPrefix(source.SecondarySource, "https://") {
		return fmt.Errorf("asset source %s secondarySource must use https", source.ID)
	}
	for platform, reason := range source.ArtifactExclusions {
		if platform != "darwin" && platform != "windows" && platform != "linux" {
			return fmt.Errorf("asset source %s has unsupported artifact exclusion platform %q", source.ID, platform)
		}
		if len(strings.TrimSpace(reason)) < 20 {
			return fmt.Errorf("asset source %s artifact exclusion for %s requires a concrete reason", source.ID, platform)
		}
	}
	return nil
}

func hashRegularFile(root, relative string) (string, int64, error) {
	path, err := confinedPath(root, relative)
	if err != nil {
		return "", 0, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", 0, err
	}
	if !info.Mode().IsRegular() {
		return "", 0, fmt.Errorf("not a regular file: %s", relative)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), info.Size(), nil
}

func confinedPath(root, relative string) (string, error) {
	if filepath.IsAbs(relative) {
		return "", fmt.Errorf("absolute path is not allowed: %s", relative)
	}
	clean := filepath.Clean(relative)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes repository root: %s", relative)
	}
	cleanRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	resolvedRoot, err := filepath.EvalSymlinks(cleanRoot)
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	// The caller supplies the trusted anchor. Canonicalize that anchor so macOS
	// aliases such as /var -> /private/var work，then reject every symlink below
	// it while resolving the relative path.
	cleanRoot = filepath.Clean(resolvedRoot)
	candidate := cleanRoot
	parts := strings.Split(filepath.ToSlash(clean), "/")
	for index, part := range parts {
		candidate = filepath.Join(candidate, part)
		info, statErr := os.Lstat(candidate)
		if errors.Is(statErr, os.ErrNotExist) {
			// A non-existent tail cannot contain an existing symlink. The caller
			// will report the missing path if it requires the file to exist.
			break
		}
		if statErr != nil {
			return "", statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("path contains symlink at segment %d: %s", index, relative)
		}
	}
	return filepath.Join(cleanRoot, clean), nil
}

func matchesAny(name string, patterns []string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, pattern := range patterns {
		matched, err := filepath.Match(pattern, name)
		if err == nil && matched {
			return true
		}
	}
	return false
}
