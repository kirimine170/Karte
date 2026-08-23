package compliance

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type commandRunner func(context.Context, string, ...string) ([]byte, error)

type goModule struct {
	Path     string    `json:"Path"`
	Version  string    `json:"Version"`
	Main     bool      `json:"Main"`
	Dir      string    `json:"Dir"`
	Sum      string    `json:"Sum"`
	GoModSum string    `json:"GoModSum"`
	Indirect bool      `json:"Indirect"`
	Replace  *goModule `json:"Replace"`
}

type npmLock struct {
	Name            string                     `json:"name"`
	Version         string                     `json:"version"`
	LockfileVersion int                        `json:"lockfileVersion"`
	Requires        bool                       `json:"requires"`
	Packages        map[string]npmPackageEntry `json:"packages"`
}

type npmPackageEntry struct {
	Name                 string            `json:"name"`
	Version              string            `json:"version"`
	Resolved             string            `json:"resolved"`
	Integrity            string            `json:"integrity"`
	License              string            `json:"license"`
	Dev                  bool              `json:"dev"`
	Optional             bool              `json:"optional"`
	Dependencies         map[string]string `json:"dependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
	devDependencies      map[string]string
}

type ScanResult struct {
	Components []Component
	Problems   []string
}

var spdxHeaderPattern = regexp.MustCompile(`(?im)SPDX-License-Identifier:\s*([^\r\n` + "`" + `]+)`)
var gpl3TextPattern = regexp.MustCompile(`(?is)gnu general public license\s+version 3[,，]\s*29 june 2007`)
var agpl3TextPattern = regexp.MustCompile(`(?is)gnu affero general public license\s+version 3[,，]\s*19 november 2007`)
var lgpl3TextPattern = regexp.MustCompile(`(?is)gnu lesser general public license\s+version 3[,，]\s*29 june 2007`)

const ambiguousLicenseExpression = "LicenseRef-Ambiguous"

func ScanRepository(ctx context.Context, repositoryRoot string) (ScanResult, error) {
	if err := validateProjectLicense(repositoryRoot); err != nil {
		return ScanResult{}, err
	}
	runner := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		command := exec.CommandContext(ctx, name, args...)
		command.Dir = repositoryRoot
		output, err := command.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, output)
		}
		return output, nil
	}
	runtimeModules, err := supportedDesktopGoModules(ctx, repositoryRoot)
	if err != nil {
		return ScanResult{}, err
	}
	goResult, err := scanGoDependencies(ctx, repositoryRoot, runner, runtimeModules)
	if err != nil {
		return ScanResult{}, err
	}

	var overrides NPMOverrideRegistry
	if err := LoadJSONFile(filepath.Join(repositoryRoot, "compliance", "npm-license-overrides.json"), &overrides); err != nil {
		return ScanResult{}, err
	}
	npmResult, err := scanNPMDependencies(repositoryRoot, overrides)
	if err != nil {
		return ScanResult{}, err
	}

	var assetRegistry AssetSourceRegistry
	if err := LoadJSONFile(filepath.Join(repositoryRoot, "compliance", "asset-sources.json"), &assetRegistry); err != nil {
		return ScanResult{}, err
	}
	assetInventory, err := BuildAssetInventory(repositoryRoot, assetRegistry)
	if err != nil {
		return ScanResult{}, err
	}
	assetComponents, err := assetInventory.Components(repositoryRoot)
	if err != nil {
		return ScanResult{}, err
	}

	nativeRegistry, err := LoadNativeRegistry(repositoryRoot)
	if err != nil {
		return ScanResult{}, err
	}

	result := ScanResult{
		Components: append(append(append(goResult.Components, npmResult.Components...), assetComponents...), nativeRegistry.Components...),
		Problems:   append(goResult.Problems, npmResult.Problems...),
	}
	sortComponents(result.Components)
	sort.Strings(result.Problems)
	return result, nil
}

func scanGoDependencies(ctx context.Context, repositoryRoot string, runner commandRunner, runtimeModules map[string][]string) (ScanResult, error) {
	moduleJSON, err := runner(ctx, "go", "list", "-m", "-json", "all")
	if err != nil {
		return ScanResult{}, err
	}
	modules, err := parseGoModules(bytes.NewReader(moduleJSON))
	if err != nil {
		return ScanResult{}, err
	}
	result := ScanResult{}
	for _, module := range modules {
		if module.Main {
			continue
		}
		resolved := module
		if module.Replace != nil {
			resolved = *module.Replace
		}
		name := resolved.Path
		version := resolved.Version
		if name == "" {
			name = module.Path
		}
		if version == "" {
			version = module.Version
		}
		if resolved.Dir == "" && name != "" && version != "" {
			downloadJSON, downloadErr := runner(ctx, "go", "mod", "download", "-json", name+"@"+version)
			if downloadErr != nil {
				return ScanResult{}, fmt.Errorf("resolve module evidence for %s@%s: %w", name, version, downloadErr)
			}
			var downloaded goModule
			if err := json.Unmarshal(downloadJSON, &downloaded); err != nil {
				return ScanResult{}, fmt.Errorf("decode module download metadata for %s@%s: %w", name, version, err)
			}
			if resolved.Sum != "" && downloaded.Sum != resolved.Sum {
				return ScanResult{}, fmt.Errorf("module download checksum mismatch for %s@%s", name, version)
			}
			resolved.Dir = downloaded.Dir
			resolved.Sum = downloaded.Sum
			resolved.GoModSum = downloaded.GoModSum
		}
		targets := runtimeModules[module.Path]
		if len(targets) == 0 {
			targets = runtimeModules[resolved.Path]
		}
		scope := "build"
		if len(targets) > 0 {
			scope = "runtime"
		}
		component := Component{
			ID:      "go:" + name + "@" + version,
			Type:    "go",
			Name:    name,
			Version: version,
			License: "NOASSERTION",
			Scope:   scope,
			Source:  moduleSourceURL(name),
			Properties: map[string]string{
				"requestedModule": module.Path + "@" + module.Version,
				"goSum":           resolved.Sum,
				"goModSum":        resolved.GoModSum,
			},
		}
		if len(targets) > 0 {
			component.Properties["desktopTargets"] = strings.Join(targets, "，")
		} else {
			component.Properties["desktopTargets"] = "none (tool-only or test-only module)"
		}
		if resolved.Sum == "" && resolved.Version != "" {
			result.Problems = append(result.Problems, fmt.Sprintf("%s has no go.sum module checksum", component.ID))
		}
		license, evidence, err := scanLicenseEvidence(resolved.Dir, "go/"+name+"@"+version)
		if err != nil {
			return ScanResult{}, fmt.Errorf("scan %s licenses: %w", component.ID, err)
		}
		component.License = license
		component.LicenseEvidence = evidence
		if license == "NOASSERTION" {
			result.Problems = append(result.Problems, fmt.Sprintf("%s has no recognized root license file", component.ID))
		}
		result.Components = append(result.Components, component)
	}
	return result, nil
}

type desktopTarget struct {
	GOOS   string
	GOARCH string
}

var supportedDesktopTargets = []desktopTarget{
	{GOOS: "darwin", GOARCH: "amd64"},
	{GOOS: "darwin", GOARCH: "arm64"},
	{GOOS: "linux", GOARCH: "amd64"},
	{GOOS: "windows", GOARCH: "amd64"},
}

func supportedDesktopGoModules(ctx context.Context, repositoryRoot string) (map[string][]string, error) {
	targetSets := make(map[string]map[string]struct{})
	for _, target := range supportedDesktopTargets {
		command := exec.CommandContext(ctx, "go", "list", "-mod=readonly", "-deps", "-f", "{{with .Module}}{{.Path}}{{end}}", ".")
		command.Dir = repositoryRoot
		command.Env = targetEnvironment(os.Environ(), target)
		output, err := command.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("go list desktop dependencies for %s/%s: %w\n%s", target.GOOS, target.GOARCH, err, output)
		}
		targetName := target.GOOS + "/" + target.GOARCH
		for _, line := range strings.Split(string(output), "\n") {
			modulePath := strings.TrimSpace(line)
			if modulePath == "" {
				continue
			}
			if targetSets[modulePath] == nil {
				targetSets[modulePath] = make(map[string]struct{})
			}
			targetSets[modulePath][targetName] = struct{}{}
		}
	}
	result := make(map[string][]string, len(targetSets))
	for modulePath, targets := range targetSets {
		for target := range targets {
			result[modulePath] = append(result[modulePath], target)
		}
		sort.Strings(result[modulePath])
	}
	return result, nil
}

func targetEnvironment(environment []string, target desktopTarget) []string {
	result := make([]string, 0, len(environment)+3)
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if key == "GOOS" || key == "GOARCH" || key == "CGO_ENABLED" {
			continue
		}
		result = append(result, entry)
	}
	return append(result, "GOOS="+target.GOOS, "GOARCH="+target.GOARCH, "CGO_ENABLED=1")
}

func parseGoModules(reader io.Reader) ([]goModule, error) {
	decoder := json.NewDecoder(reader)
	var modules []goModule
	for {
		var module goModule
		err := decoder.Decode(&module)
		if errors.Is(err, io.EOF) {
			return modules, nil
		}
		if err != nil {
			return nil, fmt.Errorf("decode go list module: %w", err)
		}
		modules = append(modules, module)
	}
}

func scanNPMDependencies(repositoryRoot string, overrides NPMOverrideRegistry) (ScanResult, error) {
	if overrides.SchemaVersion != SchemaVersion {
		return ScanResult{}, fmt.Errorf("unsupported npm override schemaVersion %d", overrides.SchemaVersion)
	}
	lockPath := filepath.Join(repositoryRoot, "frontend", "package-lock.json")
	var lock npmLock
	if err := LoadJSONFile(lockPath, &lock); err != nil {
		return ScanResult{}, err
	}
	if lock.LockfileVersion != 3 {
		return ScanResult{}, fmt.Errorf("frontend/package-lock.json must use lockfileVersion 3，got %d", lock.LockfileVersion)
	}
	runtimePaths, buildPaths, closureProblems := npmDependencyClosures(lock)
	resolvedSources := make(map[string]struct{}, len(lock.Packages))
	for _, entry := range lock.Packages {
		if entry.Resolved != "" {
			resolvedSources[entry.Resolved] = struct{}{}
		}
	}
	overrideIndex := make(map[string]NPMLicenseOverride, len(overrides.Overrides))
	for _, override := range overrides.Overrides {
		if err := validatePortableRelativePath(override.Path); err != nil || !strings.HasPrefix(override.Path, "node_modules/") {
			return ScanResult{}, fmt.Errorf("invalid npm override path %q", override.Path)
		}
		if strings.TrimSpace(override.Version) == "" {
			return ScanResult{}, fmt.Errorf("npm override %s has missing version", override.Path)
		}
		if _, err := parseLicenseExpression(override.License); err != nil || override.License == "NOASSERTION" {
			return ScanResult{}, fmt.Errorf("npm override %s has invalid license %q", override.Path, override.License)
		}
		if err := validatePortableRelativePath(override.EvidencePath); err != nil || !sha256Pattern.MatchString(override.EvidenceSHA256) {
			return ScanResult{}, fmt.Errorf("npm override %s has invalid evidence path or SHA-256", override.Path)
		}
		parsedEvidenceSource, sourceErr := url.Parse(override.EvidenceSource)
		if sourceErr != nil || parsedEvidenceSource.Scheme != "https" || parsedEvidenceSource.Host == "" || containsResolutionPlaceholder(override.EvidenceSource) {
			return ScanResult{}, fmt.Errorf("npm override %s has invalid or unpinned evidenceSource", override.Path)
		}
		if _, pinnedByLock := resolvedSources[override.EvidenceSource]; !pinnedByLock {
			return ScanResult{}, fmt.Errorf("npm override %s evidenceSource is not pinned by package-lock.json", override.Path)
		}
		key := override.Path + "\x00" + override.Version
		if _, ok := overrideIndex[key]; ok {
			return ScanResult{}, fmt.Errorf("duplicate npm license override for %s@%s", override.Path, override.Version)
		}
		overrideIndex[key] = override
	}

	result := ScanResult{Problems: closureProblems}
	usedOverrides := make(map[string]struct{}, len(overrideIndex))
	paths := make([]string, 0, len(lock.Packages))
	for packagePath := range lock.Packages {
		if packagePath != "" {
			paths = append(paths, packagePath)
		}
	}
	sort.Strings(paths)
	for _, packagePath := range paths {
		entry := lock.Packages[packagePath]
		name := npmPackageName(packagePath)
		component := Component{
			ID:      "npm:" + packagePath + "@" + entry.Version,
			Type:    "npm",
			Name:    name,
			Version: entry.Version,
			License: strings.TrimSpace(entry.License),
			Scope:   "build",
			Source:  entry.Resolved,
			Properties: map[string]string{
				"installPath": packagePath,
				"integrity":   entry.Integrity,
			},
		}
		if _, ok := runtimePaths[packagePath]; ok {
			component.Scope = "runtime"
			if entry.Optional {
				component.Scope = "optional"
			}
		} else if _, ok := buildPaths[packagePath]; !ok {
			if !entry.Optional {
				result.Problems = append(result.Problems, fmt.Sprintf("%s is orphaned from root dependency closures", component.ID))
			}
		}
		if entry.Optional {
			component.Properties["optional"] = "true"
		}
		if entry.Integrity == "" || (!strings.HasPrefix(entry.Integrity, "sha512-") && !strings.HasPrefix(entry.Integrity, "sha256-")) {
			result.Problems = append(result.Problems, fmt.Sprintf("%s has missing or unsupported npm integrity", component.ID))
		}
		if !strings.HasPrefix(entry.Resolved, "https://registry.npmjs.org/") {
			result.Problems = append(result.Problems, fmt.Sprintf("%s has unapproved npm resolved URL %q", component.ID, entry.Resolved))
		}

		evidenceDir, pathErr := confinedPath(repositoryRoot, filepath.ToSlash(filepath.Join("frontend", filepath.FromSlash(packagePath))))
		if pathErr != nil {
			return ScanResult{}, fmt.Errorf("resolve %s evidence directory: %w", component.ID, pathErr)
		}
		license, evidence, err := scanLicenseEvidence(evidenceDir, "npm/"+packagePath+"@"+entry.Version)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return ScanResult{}, fmt.Errorf("scan %s license evidence: %w", component.ID, err)
		}
		component.LicenseEvidence = evidence
		overrideKey := packagePath + "\x00" + entry.Version
		override, hasOverride := overrideIndex[overrideKey]
		if hasOverride {
			usedOverrides[overrideKey] = struct{}{}
		}
		if component.License != "" {
			if _, err := parseLicenseExpression(component.License); err != nil {
				result.Problems = append(result.Problems, fmt.Sprintf("%s package-lock license is malformed: %v", component.ID, err))
			}
		}
		if license != "NOASSERTION" && hasOverride {
			result.Problems = append(result.Problems, fmt.Sprintf("%s override is not allowed because recognized local evidence identifies %q", component.ID, license))
			if component.License == "" {
				component.License = license
			} else if !licenseExpressionContains(license, component.License) {
				result.Problems = append(result.Problems, fmt.Sprintf("%s local license %q does not match package-lock %q", component.ID, license, component.License))
			}
		} else if hasOverride {
			if component.License == "" {
				component.License = override.License
			} else if !equivalentLicenseExpressions(component.License, override.License) {
				result.Problems = append(result.Problems, fmt.Sprintf("%s override license %q does not match package-lock %q", component.ID, override.License, component.License))
			}
			if _, _, hashErr := hashRegularFile(repositoryRoot, override.EvidencePath); hashErr != nil {
				result.Problems = append(result.Problems, fmt.Sprintf("%s override evidence is unavailable: %v", component.ID, hashErr))
			} else {
				text, readErr := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(override.EvidencePath)))
				if readErr != nil {
					return ScanResult{}, readErr
				}
				normalized, normalizeErr := normalizeText(text)
				if normalizeErr != nil {
					return ScanResult{}, fmt.Errorf("normalize %s override evidence: %w", component.ID, normalizeErr)
				}
				if sha256Hex([]byte(normalized)) != override.EvidenceSHA256 {
					result.Problems = append(result.Problems, fmt.Sprintf("%s override evidence SHA-256 mismatch", component.ID))
				} else if detected := recognizeLicense(override.EvidencePath, []byte(normalized)); !equivalentLicenseExpressions(detected, override.License) {
					result.Problems = append(result.Problems, fmt.Sprintf("%s override evidence identifies %q，want %q", component.ID, detected, override.License))
				} else {
					component.LicenseEvidence = append(component.LicenseEvidence, LicenseEvidence{
						Kind: "license", Path: override.EvidencePath,
						SHA256: override.EvidenceSHA256, Text: normalized,
					})
					component.Properties["licenseEvidenceSource"] = override.EvidenceSource
				}
			}
		} else if license != "NOASSERTION" {
			if component.License == "" {
				component.License = license
			} else if !licenseExpressionContains(license, component.License) {
				result.Problems = append(result.Problems, fmt.Sprintf("%s local license %q does not match package-lock %q", component.ID, license, component.License))
			} else if !equivalentLicenseExpressions(license, component.License) {
				// A package may redistribute separately licensed source in its
				// root license file. Preserve every detected term in the SBOM.
				component.Properties["packageLockLicense"] = component.License
				component.License = license
			}
		} else if component.License == "" {
			component.License = "NOASSERTION"
			result.Problems = append(result.Problems, fmt.Sprintf("%s has no package-lock license and no recognized local evidence or override", component.ID))
		}
		result.Components = append(result.Components, component)
	}
	var unusedOverrides []string
	for key, override := range overrideIndex {
		if _, used := usedOverrides[key]; !used {
			unusedOverrides = append(unusedOverrides, override.Path+"@"+override.Version)
		}
	}
	if len(unusedOverrides) > 0 {
		sort.Strings(unusedOverrides)
		return ScanResult{}, fmt.Errorf("stale or unused npm license overrides: %s", strings.Join(unusedOverrides, "，"))
	}
	return result, nil
}

func npmDependencyClosures(lock npmLock) (map[string]struct{}, map[string]struct{}, []string) {
	runtime := make(map[string]struct{})
	build := make(map[string]struct{})
	root, ok := lock.Packages[""]
	if !ok {
		return runtime, build, []string{"frontend/package-lock.json is missing the root package"}
	}
	var problems []string
	var visit func(string, map[string]struct{})
	visit = func(packagePath string, destination map[string]struct{}) {
		if _, ok := destination[packagePath]; ok {
			return
		}
		entry, ok := lock.Packages[packagePath]
		if !ok {
			problems = append(problems, fmt.Sprintf("package-lock dependency path %s is missing", packagePath))
			return
		}
		destination[packagePath] = struct{}{}
		children := make(map[string]string, len(entry.Dependencies)+len(entry.OptionalDependencies))
		for name, version := range entry.Dependencies {
			children[name] = version
		}
		for name, version := range entry.OptionalDependencies {
			children[name] = version
		}
		names := make([]string, 0, len(children))
		for name := range children {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			resolved, ok := resolveNPMLockPath(lock.Packages, packagePath, name)
			if !ok {
				if !entry.Optional {
					problems = append(problems, fmt.Sprintf("package-lock cannot resolve %s from %s", name, displayNPMParent(packagePath)))
				}
				continue
			}
			visit(resolved, destination)
		}
	}
	seedDependencies := func(dependencies map[string]string, destination map[string]struct{}) {
		names := make([]string, 0, len(dependencies))
		for name := range dependencies {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			resolved, ok := resolveNPMLockPath(lock.Packages, "", name)
			if !ok {
				problems = append(problems, fmt.Sprintf("package-lock cannot resolve root dependency %s", name))
				continue
			}
			visit(resolved, destination)
		}
	}
	seedDependencies(root.Dependencies, runtime)
	seedDependencies(root.OptionalDependencies, runtime)
	seedDependencies(root.DevDependencies(), build)
	return runtime, build, problems
}

func (entry npmPackageEntry) DevDependencies() map[string]string {
	// Root devDependencies is decoded separately by UnmarshalJSON below.
	return entry.devDependencies
}

func (entry *npmPackageEntry) UnmarshalJSON(data []byte) error {
	type alias npmPackageEntry
	var decoded struct {
		alias
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*entry = npmPackageEntry(decoded.alias)
	entry.devDependencies = decoded.DevDependencies
	return nil
}

func resolveNPMLockPath(packages map[string]npmPackageEntry, parentPath, dependency string) (string, bool) {
	current := parentPath
	for {
		candidate := "node_modules/" + dependency
		if current != "" {
			candidate = current + "/node_modules/" + dependency
		}
		if _, ok := packages[candidate]; ok {
			return candidate, true
		}
		if current == "" {
			return "", false
		}
		current = npmParentInstallPath(current)
	}
}

func npmParentInstallPath(packagePath string) string {
	index := strings.LastIndex(packagePath, "/node_modules/")
	if index < 0 {
		return ""
	}
	return packagePath[:index]
}

func npmPackageName(packagePath string) string {
	index := strings.LastIndex(packagePath, "node_modules/")
	if index < 0 {
		return packagePath
	}
	return packagePath[index+len("node_modules/"):]
}

func displayNPMParent(path string) string {
	if path == "" {
		return "root"
	}
	return path
}

func scanLicenseEvidence(directory, displayRoot string) (string, []LicenseEvidence, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "NOASSERTION", nil, err
	}
	var candidates []string
	var noticeCandidates []string
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		lower := strings.ToLower(entry.Name())
		switch {
		case strings.HasPrefix(lower, "license"), strings.HasPrefix(lower, "licence"), strings.HasPrefix(lower, "copying"):
			candidates = append(candidates, entry.Name())
		case strings.HasPrefix(lower, "notice"):
			noticeCandidates = append(noticeCandidates, entry.Name())
		}
	}
	sort.Strings(candidates)
	sort.Strings(noticeCandidates)
	var evidence []LicenseEvidence
	var expressions []string
	for _, name := range candidates {
		path := filepath.Join(directory, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return "NOASSERTION", nil, err
		}
		normalized, err := normalizeText(data)
		if err != nil {
			return "NOASSERTION", nil, fmt.Errorf("normalize %s: %w", path, err)
		}
		evidence = append(evidence, LicenseEvidence{
			Kind: "license", Path: filepath.ToSlash(filepath.Join(displayRoot, name)),
			SHA256: sha256Hex([]byte(normalized)), Text: normalized,
		})
		expression := recognizeLicense(name, data)
		if expression == "NOASSERTION" {
			return "NOASSERTION", evidence, nil
		}
		expressions = append(expressions, expression)
	}
	for _, name := range noticeCandidates {
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			return "NOASSERTION", nil, err
		}
		normalized, err := normalizeText(data)
		if err != nil {
			return "NOASSERTION", nil, fmt.Errorf("normalize %s: %w", name, err)
		}
		evidence = append(evidence, LicenseEvidence{
			Kind: "notice", Path: filepath.ToSlash(filepath.Join(displayRoot, name)),
			SHA256: sha256Hex([]byte(normalized)), Text: normalized,
		})
	}
	if len(expressions) == 0 {
		return "NOASSERTION", evidence, nil
	}
	unique := make(map[string]struct{}, len(expressions))
	for _, expression := range expressions {
		unique[expression] = struct{}{}
	}
	if len(unique) == 1 {
		for expression := range unique {
			return expression, evidence, nil
		}
	}
	// Some projects ship one authoritative compound SPDX declaration together
	// with the individual license texts. Prefer that declaration instead of
	// duplicating its terms while combining every evidence file.
	var compound []string
	for expression := range unique {
		if strings.Contains(expression, " AND ") || strings.Contains(expression, " OR ") || strings.Contains(expression, " WITH ") {
			compound = append(compound, expression)
		}
	}
	if len(compound) == 1 {
		return compound[0], evidence, nil
	}
	values := make([]string, 0, len(unique))
	for expression := range unique {
		values = append(values, expression)
	}
	sort.Strings(values)
	return strings.Join(values, " AND "), evidence, nil
}

func recognizeLicense(name string, data []byte) string {
	text := string(data)
	headerExpression := ""
	for _, match := range spdxHeaderPattern.FindAllStringSubmatch(text, -1) {
		if len(match) != 2 {
			return ambiguousLicenseExpression
		}
		expression := strings.TrimSpace(match[1])
		if _, err := parseLicenseExpression(expression); err != nil {
			return ambiguousLicenseExpression
		}
		if headerExpression != "" && !equivalentLicenseExpressions(headerExpression, expression) {
			return ambiguousLicenseExpression
		}
		headerExpression = expression
	}

	lowerName := strings.ToLower(name)
	filenameExpression := ""
	switch {
	case strings.Contains(lowerName, "mpl-2.0"):
		filenameExpression = "MPL-2.0"
	case strings.Contains(lowerName, "apache"):
		filenameExpression = "Apache-2.0"
	}

	bodyExpression := recognizeLicenseBody(data)
	if bodyExpression == ambiguousLicenseExpression {
		terms, restricted := recognizeLicenseBodyTerms(data)
		if restricted || headerExpression == "" || len(terms) < 2 ||
			!equivalentLicenseExpressions(headerExpression, strings.Join(terms, " AND ")) {
			return ambiguousLicenseExpression
		}
		if filenameExpression != "" && !equivalentLicenseExpressions(filenameExpression, headerExpression) {
			return ambiguousLicenseExpression
		}
		return headerExpression
	}
	if bodyExpression != "NOASSERTION" {
		for _, claimed := range []string{headerExpression, filenameExpression} {
			if claimed != "" && !equivalentLicenseExpressions(claimed, bodyExpression) {
				return ambiguousLicenseExpression
			}
		}
		return bodyExpression
	}
	if headerExpression != "" && filenameExpression != "" &&
		!equivalentLicenseExpressions(headerExpression, filenameExpression) {
		return ambiguousLicenseExpression
	}
	if headerExpression != "" {
		return headerExpression
	}
	if filenameExpression != "" {
		return filenameExpression
	}
	return "NOASSERTION"
}

// recognizeLicenseBody intentionally ignores SPDX-License-Identifier lines．
// Artifact capture metadata is produced from registry values，so accepting an
// injected header would let arbitrary bytes masquerade as the declared
// license．Native artifact evidence must therefore be identified from the
// redistributed license text itself．
func recognizeLicenseBody(data []byte) string {
	terms, restricted := recognizeLicenseBodyTerms(data)
	if restricted {
		return ambiguousLicenseExpression
	}
	if len(terms) == 0 {
		return "NOASSERTION"
	}
	if len(terms) == 1 {
		return terms[0]
	}
	if expression := knownCompoundLicenseExpression(terms, data); expression != "" {
		return expression
	}
	return ambiguousLicenseExpression
}

func knownCompoundLicenseExpression(terms []string, data []byte) string {
	expression := strings.Join(terms, " AND ")
	switch expression {
	case "Apache-2.0 AND ISC", "BSD-2-Clause AND ISC", "BSD-3-Clause AND MIT", "ISC AND MIT":
		return expression
	case "Apache-2.0 AND MIT":
		if strings.Contains(strings.ToLower(string(data)), "covered by two different licenses") {
			return expression
		}
	}
	lower := strings.ToLower(string(data))
	if strings.Contains(lower, "licenses of bundled dependencies") && !containsCopyleftTerm(terms) {
		return expression
	}
	return ""
}

func containsCopyleftTerm(terms []string) bool {
	for _, term := range terms {
		switch licenseFamily(term) {
		case "AGPL", "GPL", "LGPL":
			return true
		}
	}
	return false
}

func recognizeLicenseBodyTerms(data []byte) ([]string, bool) {
	text := spdxHeaderPattern.ReplaceAllString(string(data), "")
	lower := strings.ToLower(text)
	gplVersion3 := gpl3TextPattern.MatchString(lower)
	gplVersion3OrLater := gplVersion3 &&
		(strings.Contains(lower, "either version 3 of the license， or (at your option) any later version") ||
			strings.Contains(lower, "either version 3 of the license, or (at your option) any later version"))
	gccException31 := strings.Contains(lower, "gcc runtime library exception") &&
		(strings.Contains(lower, "version 3.1， 31 march 2009") || strings.Contains(lower, "version 3.1, 31 march 2009"))

	var terms []string
	add := func(term string, detected bool) {
		if detected {
			terms = append(terms, term)
		}
	}
	add("AGPL-3.0-only", agpl3TextPattern.MatchString(lower))
	add("LGPL-3.0-only", lgpl3TextPattern.MatchString(lower))
	if gplVersion3OrLater && gccException31 {
		terms = append(terms, "GPL-3.0-or-later WITH GCC-exception-3.1")
	} else if gplVersion3OrLater {
		terms = append(terms, "GPL-3.0-or-later")
	} else if gplVersion3 {
		terms = append(terms, "GPL-3.0-only")
	}
	add("MPL-2.0", strings.Contains(lower, "mozilla public license") && strings.Contains(lower, "version 2.0"))
	add("Apache-2.0", strings.Contains(lower, "apache license") && strings.Contains(lower, "version 2.0"))
	add("BlueOak-1.0.0", strings.Contains(lower, "blue oak model license") && strings.Contains(lower, "version 1.0.0"))
	add("CC0-1.0", strings.Contains(lower, "cc0 1.0 universal") && strings.Contains(lower, "creative commons"))
	add("Unlicense", strings.Contains(lower, "free and unencumbered software released into the public domain"))
	add("ISC", strings.Contains(lower, "permission to use, copy, modify") && strings.Contains(lower, "with or without fee"))
	mitText := strings.Contains(lower, "permission is hereby granted, free of charge") && strings.Contains(lower, "the software")
	mitZero := strings.Contains(lower, "mit no attribution") || strings.Contains(lower, "mit-0")
	add("MIT-0", mitText && mitZero)
	add("MIT", mitText && !mitZero)
	bsd := strings.Contains(lower, "redistribution and use in source and binary forms")
	if bsd && (strings.Contains(lower, "neither the name") || strings.Contains(lower, "may not be used to endorse or promote")) {
		terms = append(terms, "BSD-3-Clause")
	} else if bsd {
		terms = append(terms, "BSD-2-Clause")
	}
	add("OFL-1.1", strings.Contains(lower, "sil open font license") && strings.Contains(lower, "version 1.1"))
	sort.Strings(terms)
	restricted := mitText && containsUnknownMITRestriction(lower)
	return uniqueStrings(terms), restricted
}

func containsUnknownMITRestriction(lower string) bool {
	for _, marker := range []string{
		"additional restriction", "commons clause", "commercial use is prohibited",
		"commercial use prohibited", "non-commercial use only", "noncommercial use only",
		"the software may not be sold", "you may not sell the software",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func (inventory AssetInventory) Components(repositoryRoot string) ([]Component, error) {
	var components []Component
	for _, collection := range inventory.Collections {
		licenseText, err := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(collection.LicensePath)))
		if err != nil {
			return nil, err
		}
		normalizedLicense, err := normalizeText(licenseText)
		if err != nil {
			return nil, fmt.Errorf("normalize asset collection %s license: %w", collection.ID, err)
		}
		aggregate := sha256.New()
		for _, file := range collection.Files {
			fmt.Fprintf(aggregate, "%s\x00%d\x00%s\n", file.Path, file.Bytes, file.SHA256)
		}
		componentType := "asset"
		if strings.Contains(collection.ID, "asr-model") {
			componentType = "model"
		}
		component := Component{
			ID:               collection.ID,
			Type:             componentType,
			Name:             collection.Name,
			Version:          collection.Version,
			License:          collection.License,
			Scope:            "runtime",
			Source:           collection.Source,
			DistributionPath: collection.Root,
			Hashes:           map[string]string{"SHA-256": hex.EncodeToString(aggregate.Sum(nil))},
			Properties:       map[string]string{"fileCount": fmt.Sprint(len(collection.Files))},
			LicenseEvidence: []LicenseEvidence{{
				Kind: "license", Path: collection.LicensePath,
				SHA256: sha256Hex([]byte(normalizedLicense)), Text: normalizedLicense,
			}},
		}
		if collection.SecondarySource != "" {
			component.Properties["secondarySource"] = collection.SecondarySource
		}
		for platform, reason := range collection.ArtifactExclusions {
			component.Properties["artifactExclusion:"+platform] = reason
		}
		components = append(components, component)
	}
	return components, nil
}

func moduleSourceURL(modulePath string) string {
	if strings.HasPrefix(modulePath, "github.com/") {
		parts := strings.Split(modulePath, "/")
		if len(parts) >= 3 {
			return "https://github.com/" + parts[1] + "/" + strings.TrimSuffix(parts[2], ".git")
		}
	}
	return "https://" + modulePath
}

func normalizeText(data []byte) (string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, strings.TrimRight(scanner.Text(), " \t"))
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n", nil
}

func validLicenseEvidence(evidence LicenseEvidence) bool {
	return evidence.Kind == "license" && strings.TrimSpace(evidence.Text) != "" &&
		sha256Pattern.MatchString(evidence.SHA256) && sha256Hex([]byte(evidence.Text)) == evidence.SHA256
}

func hasValidLicenseEvidence(component Component) bool {
	wanted, err := parseLicenseExpression(component.License)
	if err != nil || len(wanted) == 0 {
		return false
	}
	covered := make(map[string]struct{})
	for _, evidence := range component.LicenseEvidence {
		if !validLicenseEvidence(evidence) {
			continue
		}
		terms, parseErr := parseLicenseExpression(recognizeLicense(evidence.Path, []byte(evidence.Text)))
		if parseErr != nil {
			continue
		}
		for _, term := range terms {
			covered[term] = struct{}{}
		}
	}
	for _, term := range wanted {
		if _, ok := covered[term]; !ok {
			return false
		}
	}
	return true
}

func equivalentLicenseExpressions(left, right string) bool {
	leftTerms, leftErr := parseLicenseExpression(left)
	rightTerms, rightErr := parseLicenseExpression(right)
	if leftErr != nil || rightErr != nil || len(leftTerms) != len(rightTerms) {
		return false
	}
	sort.Strings(leftTerms)
	sort.Strings(rightTerms)
	return strings.Join(leftTerms, "\x00") == strings.Join(rightTerms, "\x00")
}

func licenseExpressionContains(container, required string) bool {
	containerTerms, containerErr := parseLicenseExpression(container)
	requiredTerms, requiredErr := parseLicenseExpression(required)
	if containerErr != nil || requiredErr != nil || len(requiredTerms) == 0 {
		return false
	}
	available := make(map[string]struct{}, len(containerTerms))
	for _, term := range containerTerms {
		available[term] = struct{}{}
	}
	for _, term := range requiredTerms {
		if _, ok := available[term]; !ok {
			return false
		}
	}
	return true
}

func sortComponents(components []Component) {
	sort.Slice(components, func(i, j int) bool { return components[i].ID < components[j].ID })
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
