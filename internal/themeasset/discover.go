// Package themeasset validates and inventories portable assets in a Karte
// Format package. Manifest parsing remains owned by KarteRenderer; callers pass
// the already validated package entrypoints and assets directory to Discover.
package themeasset

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const (
	DefaultMaxFiles      = 256
	DefaultMaxTotalBytes = int64(32 << 20)
	DefaultMaxFileBytes  = int64(8 << 20)
	DefaultMaxTextBytes  = int64(1 << 20)
)

// Limits bounds both validation work and the in-memory package snapshot.
type Limits struct {
	MaxFiles      int
	MaxTotalBytes int64
	MaxFileBytes  int64
	MaxTextBytes  int64
}

// Spec describes the portions of a validated Karte Format manifest needed for
// asset discovery. Paths use canonical, package-relative forward slashes.
type Spec struct {
	PackageRoot    string
	AssetDirectory string
	Entrypoints    []string
	Limits         Limits
}

// Kind is the portable resource kind inferred from the reference context and
// file extension.
type Kind string

const (
	KindStylesheet Kind = "stylesheet"
	KindFont       Kind = "font"
	KindImage      Kind = "image"
	KindBuiltin    Kind = "marp-builtin"
	KindFragment   Kind = "fragment"
	KindOther      Kind = "other"
)

// Asset is one package-local file below assets.directory. SHA256 covers the
// exact bytes read from the confined package snapshot.
type Asset struct {
	Path       string `json:"path"`
	Kind       Kind   `json:"kind"`
	Bytes      int64  `json:"bytes"`
	SHA256     string `json:"sha256"`
	Referenced bool   `json:"referenced"`
}

// Reference records a reference after URL query and fragment data have been
// separated from the canonical file lookup target.
type Reference struct {
	Owner    string `json:"owner"`
	Context  string `json:"context"`
	Raw      string `json:"raw"`
	Target   string `json:"target,omitempty"`
	Query    string `json:"query,omitempty"`
	Fragment string `json:"fragment,omitempty"`
	Kind     Kind   `json:"kind"`
}

// Report is deterministic: entries, assets, and references are sorted.
type Report struct {
	Entrypoints []string    `json:"entrypoints"`
	Assets      []Asset     `json:"assets"`
	References  []Reference `json:"references"`
	TotalFiles  int         `json:"totalFiles"`
	TotalBytes  int64       `json:"totalBytes"`
}

// Violation is a stable, testable contract failure.
type Violation struct {
	Code      string
	Owner     string
	Reference string
	Detail    string
}

func (v Violation) Error() string {
	location := v.Owner
	if v.Reference != "" {
		location += " reference " + fmt.Sprintf("%q", v.Reference)
	}
	if location == "" {
		return v.Code + ": " + v.Detail
	}
	return v.Code + ": " + location + ": " + v.Detail
}

// ValidationError contains every independently detectable violation in stable
// order. Callers can use errors.As to inspect Codes without parsing text.
type ValidationError struct {
	Violations []Violation
}

func (e *ValidationError) Error() string {
	if e == nil || len(e.Violations) == 0 {
		return "theme asset validation failed"
	}
	if len(e.Violations) == 1 {
		return e.Violations[0].Error()
	}
	return fmt.Sprintf("theme asset validation failed with %d violations: %s", len(e.Violations), e.Violations[0].Error())
}

type packageSnapshot struct {
	files      map[string][]byte
	info       map[string]fs.FileInfo
	hashes     map[string]string
	totalFiles int
	totalBytes int64
}

type referenceContext string

const (
	contextCSSURL       referenceContext = "css-url"
	contextCSSImport    referenceContext = "css-import"
	contextFontFace     referenceContext = "font-face"
	contextHTMLSrc      referenceContext = "html-src"
	contextHTMLHref     referenceContext = "html-href"
	contextHTMLSrcset   referenceContext = "html-srcset"
	contextHTMLStyle    referenceContext = "html-style"
	contextHTMLStyleTag referenceContext = "html-style-tag"
)

type rawReference struct {
	owner    string
	context  referenceContext
	raw      string
	expected Kind
}

// Discover validates the package boundary and returns a portable asset
// inventory. The function snapshots bounded regular-file contents through
// os.Root before parsing, so parsing cannot be redirected outside the package
// by a later path lookup.
func Discover(spec Spec) (Report, error) {
	limits := normalizeLimits(spec.Limits)
	snapshot, err := snapshotPackage(spec.PackageRoot, limits)
	if err != nil {
		return Report{}, err
	}

	report := Report{
		TotalFiles: snapshot.totalFiles,
		TotalBytes: snapshot.totalBytes,
	}
	violations := make([]Violation, 0)

	assetDirectory, pathViolation := validateManifestPath("assets.directory", spec.AssetDirectory)
	if pathViolation != nil {
		violations = append(violations, *pathViolation)
	} else if info, ok := snapshot.info[assetDirectory]; !ok || !info.IsDir() {
		violations = append(violations, Violation{
			Code:   "missing-assets-directory",
			Owner:  assetDirectory,
			Detail: "assets.directory must name an existing package directory",
		})
	}

	entrySet := make(map[string]struct{}, len(spec.Entrypoints))
	for _, entry := range spec.Entrypoints {
		cleaned, violation := validateManifestPath("entrypoint", entry)
		if violation != nil {
			violations = append(violations, *violation)
			continue
		}
		if _, duplicate := entrySet[cleaned]; duplicate {
			violations = append(violations, Violation{
				Code:   "duplicate-entrypoint",
				Owner:  cleaned,
				Detail: "entrypoints must be unique",
			})
			continue
		}
		entrySet[cleaned] = struct{}{}
		report.Entrypoints = append(report.Entrypoints, cleaned)
		if _, ok := snapshot.files[cleaned]; !ok {
			violations = append(violations, Violation{
				Code:   "missing-entrypoint",
				Owner:  cleaned,
				Detail: "entrypoint must name a regular package file",
			})
		}
	}
	if len(spec.Entrypoints) == 0 {
		violations = append(violations, Violation{
			Code:   "missing-entrypoints",
			Detail: "at least one layout or stylesheet entrypoint is required",
		})
	}
	sort.Strings(report.Entrypoints)

	assetsPrefix := assetDirectory + "/"
	assetIndex := make(map[string]int)
	if pathViolation == nil {
		for filePath, contents := range snapshot.files {
			if !strings.HasPrefix(filePath, assetsPrefix) {
				continue
			}
			if violation := validatePortableFilePath(filePath); violation != nil {
				violations = append(violations, *violation)
			}
			asset := Asset{
				Path:   filePath,
				Kind:   kindFromExtension(filePath),
				Bytes:  int64(len(contents)),
				SHA256: snapshot.hashes[filePath],
			}
			report.Assets = append(report.Assets, asset)
		}
		sort.Slice(report.Assets, func(i, j int) bool { return report.Assets[i].Path < report.Assets[j].Path })
		for i := range report.Assets {
			assetIndex[report.Assets[i].Path] = i
		}
	}

	queue := append([]string(nil), report.Entrypoints...)
	scanned := make(map[string]bool)
	for len(queue) > 0 {
		owner := queue[0]
		queue = queue[1:]
		if scanned[owner] {
			continue
		}
		scanned[owner] = true
		contents, ok := snapshot.files[owner]
		if !ok {
			continue
		}
		if int64(len(contents)) > limits.MaxTextBytes {
			violations = append(violations, Violation{
				Code:   "text-file-too-large",
				Owner:  owner,
				Detail: fmt.Sprintf("HTML and CSS inputs are limited to %d bytes", limits.MaxTextBytes),
			})
			continue
		}

		var refs []rawReference
		switch strings.ToLower(path.Ext(owner)) {
		case ".html", ".htm":
			var parseViolations []Violation
			refs, parseViolations = scanHTML(owner, contents)
			violations = append(violations, parseViolations...)
		case ".css":
			var scanErr error
			refs, scanErr = scanCSS(owner, string(contents), contextCSSURL)
			if scanErr != nil {
				violations = append(violations, Violation{
					Code:   "unsupported-css",
					Owner:  owner,
					Detail: scanErr.Error(),
				})
				continue
			}
		default:
			violations = append(violations, Violation{
				Code:   "unsupported-entrypoint",
				Owner:  owner,
				Detail: "entrypoint must be HTML or CSS",
			})
			continue
		}

		for _, raw := range refs {
			if raw.context == contextCSSImport && isMarpBuiltinImport(owner, raw.raw) {
				report.References = append(report.References, Reference{
					Owner:   owner,
					Context: string(raw.context),
					Raw:     raw.raw,
					Target:  "@marp/" + raw.raw,
					Kind:    KindBuiltin,
				})
				continue
			}

			resolved, violation := resolveLocalReference(owner, raw)
			if violation != nil {
				violations = append(violations, *violation)
				continue
			}
			if resolved.Kind == KindFragment {
				report.References = append(report.References, resolved)
				continue
			}

			targetKind, targetViolations := validateReferenceTarget(raw, resolved.Target, assetDirectory, snapshot)
			resolved.Kind = targetKind
			report.References = append(report.References, resolved)
			violations = append(violations, targetViolations...)
			if len(targetViolations) != 0 {
				continue
			}
			if targetKind == KindStylesheet && !scanned[resolved.Target] {
				queue = append(queue, resolved.Target)
			}
			if index, ok := assetIndex[resolved.Target]; ok {
				report.Assets[index].Referenced = true
			}
		}
	}

	sort.Slice(report.References, func(i, j int) bool {
		a, b := report.References[i], report.References[j]
		if a.Owner != b.Owner {
			return a.Owner < b.Owner
		}
		if a.Context != b.Context {
			return a.Context < b.Context
		}
		if a.Raw != b.Raw {
			return a.Raw < b.Raw
		}
		return a.Target < b.Target
	})
	sortViolations(violations)
	if len(violations) != 0 {
		return report, &ValidationError{Violations: violations}
	}
	return report, nil
}

func normalizeLimits(limits Limits) Limits {
	if limits.MaxFiles <= 0 {
		limits.MaxFiles = DefaultMaxFiles
	}
	if limits.MaxTotalBytes <= 0 {
		limits.MaxTotalBytes = DefaultMaxTotalBytes
	}
	if limits.MaxFileBytes <= 0 {
		limits.MaxFileBytes = DefaultMaxFileBytes
	}
	if limits.MaxTextBytes <= 0 {
		limits.MaxTextBytes = DefaultMaxTextBytes
	}
	return limits
}

func snapshotPackage(packageRoot string, limits Limits) (packageSnapshot, error) {
	if strings.TrimSpace(packageRoot) == "" {
		return packageSnapshot{}, fmt.Errorf("theme package root is empty")
	}
	absRoot, err := filepath.Abs(packageRoot)
	if err != nil {
		return packageSnapshot{}, fmt.Errorf("resolve theme package root: %w", err)
	}
	rootInfo, err := os.Lstat(absRoot)
	if err != nil {
		return packageSnapshot{}, fmt.Errorf("lstat theme package root: %w", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return packageSnapshot{}, fmt.Errorf("theme package root must be a real directory，not a symlink")
	}
	root, err := os.OpenRoot(absRoot)
	if err != nil {
		return packageSnapshot{}, fmt.Errorf("open confined theme package root: %w", err)
	}
	defer root.Close()
	openedRootInfo, err := root.Stat(".")
	if err != nil {
		return packageSnapshot{}, fmt.Errorf("stat confined theme package root: %w", err)
	}
	if !openedRootInfo.IsDir() || !os.SameFile(rootInfo, openedRootInfo) {
		return packageSnapshot{}, fmt.Errorf("theme package root changed while opening")
	}

	snapshot := packageSnapshot{
		files:  make(map[string][]byte),
		info:   map[string]fs.FileInfo{".": rootInfo},
		hashes: make(map[string]string),
	}
	caseFolded := make(map[string]string)
	err = fs.WalkDir(root.FS(), ".", func(fsPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if fsPath == "." {
			return nil
		}
		rel := path.Clean(filepath.ToSlash(fsPath))
		rootName := filepath.FromSlash(rel)
		info, err := root.Lstat(rootName)
		if err != nil {
			return fmt.Errorf("lstat package path %s: %w", rel, err)
		}
		snapshot.info[rel] = info
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink-not-allowed: %s", rel)
		}
		folded := strings.ToLower(rel)
		if previous, exists := caseFolded[folded]; exists && previous != rel {
			return fmt.Errorf("case-collision: %s conflicts with %s", rel, previous)
		}
		caseFolded[folded] = rel
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular-file: %s", rel)
		}
		snapshot.totalFiles++
		if snapshot.totalFiles > limits.MaxFiles {
			return fmt.Errorf("file-count-limit: package exceeds %d files", limits.MaxFiles)
		}
		if info.Size() > limits.MaxFileBytes {
			return fmt.Errorf("file-size-limit: %s exceeds %d bytes", rel, limits.MaxFileBytes)
		}

		file, err := root.Open(rootName)
		if err != nil {
			return fmt.Errorf("open package file %s: %w", rel, err)
		}
		openedInfo, statErr := file.Stat()
		if statErr != nil {
			_ = file.Close()
			return fmt.Errorf("stat opened package file %s: %w", rel, statErr)
		}
		if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
			_ = file.Close()
			return fmt.Errorf("package file changed while opening: %s", rel)
		}
		contents, readErr := io.ReadAll(io.LimitReader(file, limits.MaxFileBytes+1))
		closeErr := file.Close()
		if readErr != nil {
			return fmt.Errorf("read package file %s: %w", rel, readErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close package file %s: %w", rel, closeErr)
		}
		if int64(len(contents)) > limits.MaxFileBytes {
			return fmt.Errorf("file-size-limit: %s exceeds %d bytes", rel, limits.MaxFileBytes)
		}
		afterInfo, err := root.Lstat(rootName)
		if err != nil || !os.SameFile(openedInfo, afterInfo) {
			return fmt.Errorf("package file changed while reading: %s", rel)
		}
		snapshot.totalBytes += int64(len(contents))
		if snapshot.totalBytes > limits.MaxTotalBytes {
			return fmt.Errorf("total-size-limit: package exceeds %d bytes", limits.MaxTotalBytes)
		}
		digest := sha256.Sum256(contents)
		snapshot.files[rel] = contents
		snapshot.hashes[rel] = hex.EncodeToString(digest[:])
		return nil
	})
	if err != nil {
		return packageSnapshot{}, fmt.Errorf("snapshot theme package: %w", err)
	}
	return snapshot, nil
}

func validateManifestPath(field, value string) (string, *Violation) {
	if value == "" || value != strings.TrimSpace(value) {
		return "", &Violation{Code: "invalid-package-path", Owner: value, Detail: field + " must be non-empty without surrounding whitespace"}
	}
	if strings.ContainsRune(value, '\x00') || strings.Contains(value, "\\") || strings.Contains(value, "%") {
		return "", &Violation{Code: "invalid-package-path", Owner: value, Detail: field + " contains a forbidden escape or separator"}
	}
	if path.IsAbs(value) || hasWindowsDrivePrefix(value) {
		return "", &Violation{Code: "invalid-package-path", Owner: value, Detail: field + " must be package-relative"}
	}
	cleaned := path.Clean(value)
	if cleaned != value || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", &Violation{Code: "invalid-package-path", Owner: value, Detail: field + " must be a canonical package path"}
	}
	if violation := validatePortableFilePath(cleaned); violation != nil {
		violation.Detail = field + ": " + violation.Detail
		return "", violation
	}
	return cleaned, nil
}

func validatePortableFilePath(value string) *Violation {
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." {
			return &Violation{Code: "non-portable-path", Owner: value, Detail: "path contains an empty or dot component"}
		}
		for _, r := range component {
			if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-') {
				return &Violation{Code: "non-portable-path", Owner: value, Detail: "paths must use lowercase ASCII letters，digits，dots，underscores，or hyphens"}
			}
		}
		if component[0] == '.' || component[len(component)-1] == '.' || component[len(component)-1] == ' ' {
			return &Violation{Code: "non-portable-path", Owner: value, Detail: "path components must not start or end with a dot or space"}
		}
		if isWindowsReservedComponent(component) {
			return &Violation{Code: "non-portable-path", Owner: value, Detail: "path contains a Windows reserved device name"}
		}
	}
	return nil
}

func isWindowsReservedComponent(component string) bool {
	base := strings.ToLower(strings.SplitN(component, ".", 2)[0])
	if base == "con" || base == "prn" || base == "aux" || base == "nul" {
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "com") || strings.HasPrefix(base, "lpt")) && base[3] >= '1' && base[3] <= '9' {
		return true
	}
	return false
}

func resolveLocalReference(owner string, raw rawReference) (Reference, *Violation) {
	value := strings.TrimSpace(raw.raw)
	base := Reference{Owner: owner, Context: string(raw.context), Raw: raw.raw, Kind: raw.expected}
	if value == "" {
		return base, &Violation{Code: "empty-reference", Owner: owner, Reference: raw.raw, Detail: "reference is empty"}
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return base, &Violation{Code: "invalid-reference", Owner: owner, Reference: raw.raw, Detail: err.Error()}
	}
	if strings.EqualFold(parsed.Scheme, "data") {
		return base, &Violation{Code: "embedded-reference", Owner: owner, Reference: raw.raw, Detail: "data: resources are outside the portable package inventory"}
	}
	if parsed.Scheme != "" || parsed.Host != "" || parsed.Opaque != "" || strings.HasPrefix(value, "//") {
		return base, &Violation{Code: "external-reference", Owner: owner, Reference: raw.raw, Detail: "remote，absolute，and file URLs are forbidden"}
	}
	if parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment != "" {
		base.Fragment = parsed.Fragment
		base.Kind = KindFragment
		return base, nil
	}
	if parsed.Path == "" {
		return base, &Violation{Code: "invalid-reference", Owner: owner, Reference: raw.raw, Detail: "a local reference must include a file path"}
	}
	if strings.Contains(parsed.EscapedPath(), "%") {
		return base, &Violation{Code: "encoded-path", Owner: owner, Reference: raw.raw, Detail: "percent-encoded local paths are forbidden"}
	}
	if strings.Contains(parsed.Path, "\\") || path.IsAbs(parsed.Path) || hasWindowsDrivePrefix(parsed.Path) {
		return base, &Violation{Code: "absolute-reference", Owner: owner, Reference: raw.raw, Detail: "local references must be owner-relative"}
	}
	target := path.Clean(path.Join(path.Dir(owner), parsed.Path))
	if target == "." || target == ".." || strings.HasPrefix(target, "../") {
		return base, &Violation{Code: "package-escape", Owner: owner, Reference: raw.raw, Detail: "reference escapes the format package"}
	}
	if violation := validatePortableFilePath(target); violation != nil {
		return base, &Violation{Code: violation.Code, Owner: owner, Reference: raw.raw, Detail: violation.Detail}
	}
	base.Target = target
	base.Query = parsed.RawQuery
	base.Fragment = parsed.Fragment
	return base, nil
}

func validateReferenceTarget(raw rawReference, target, assetDirectory string, snapshot packageSnapshot) (Kind, []Violation) {
	violations := make([]Violation, 0)
	info, exists := snapshot.info[target]
	if !exists || info.IsDir() {
		return raw.expected, []Violation{{
			Code:      "missing-reference",
			Owner:     raw.owner,
			Reference: raw.raw,
			Detail:    "target does not name a regular package file: " + target,
		}}
	}

	kind := raw.expected
	if kind == "" || kind == KindOther {
		kind = kindFromExtension(target)
	}
	extension := strings.ToLower(path.Ext(target))
	assetsPrefix := assetDirectory + "/"

	switch raw.context {
	case contextCSSImport:
		kind = KindStylesheet
		if extension != ".css" {
			violations = append(violations, Violation{Code: "invalid-stylesheet", Owner: raw.owner, Reference: raw.raw, Detail: "@import must target a .css package file"})
		}
	case contextFontFace:
		kind = KindFont
		if extension != ".woff2" {
			violations = append(violations, Violation{Code: "invalid-font", Owner: raw.owner, Reference: raw.raw, Detail: "@font-face must target exactly one WOFF2 file"})
		}
		if !strings.HasPrefix(target, assetsPrefix+"fonts/") {
			violations = append(violations, Violation{Code: "font-outside-assets", Owner: raw.owner, Reference: raw.raw, Detail: "fonts must be below assets.directory/fonts"})
		}
	case contextCSSURL, contextHTMLStyle, contextHTMLStyleTag:
		kind = kindFromExtension(target)
		if kind == KindFont {
			violations = append(violations, Violation{Code: "font-outside-font-face", Owner: raw.owner, Reference: raw.raw, Detail: "fonts may only be referenced by @font-face"})
		} else if kind != KindImage {
			violations = append(violations, Violation{Code: "unsupported-asset-type", Owner: raw.owner, Reference: raw.raw, Detail: "CSS url() must target a supported image or a WOFF2 @font-face source"})
		}
		if kind == KindImage && !strings.HasPrefix(target, assetsPrefix+"images/") {
			violations = append(violations, Violation{Code: "image-outside-assets", Owner: raw.owner, Reference: raw.raw, Detail: "images must be below assets.directory/images"})
		}
	case contextHTMLSrc, contextHTMLSrcset:
		kind = KindImage
		if !isImageExtension(extension) {
			violations = append(violations, Violation{Code: "unsupported-image", Owner: raw.owner, Reference: raw.raw, Detail: "HTML image sources must use png，jpeg，gif，webp，or avif"})
		}
		if !strings.HasPrefix(target, assetsPrefix+"images/") {
			violations = append(violations, Violation{Code: "image-outside-assets", Owner: raw.owner, Reference: raw.raw, Detail: "images must be below assets.directory/images"})
		}
	case contextHTMLHref:
		if raw.expected == KindStylesheet {
			kind = KindStylesheet
			if extension != ".css" {
				violations = append(violations, Violation{Code: "invalid-stylesheet", Owner: raw.owner, Reference: raw.raw, Detail: "stylesheet links must target a .css package file"})
			}
		} else if raw.expected == KindImage {
			kind = KindImage
			if !isImageExtension(extension) || !strings.HasPrefix(target, assetsPrefix+"images/") {
				violations = append(violations, Violation{Code: "unsupported-image", Owner: raw.owner, Reference: raw.raw, Detail: "icon links must target a supported file below assets.directory/images"})
			}
		} else {
			kind = kindFromExtension(target)
			violations = append(violations, Violation{Code: "navigation-reference", Owner: raw.owner, Reference: raw.raw, Detail: "theme templates may not navigate to another file"})
		}
	}
	return kind, violations
}

func kindFromExtension(filePath string) Kind {
	extension := strings.ToLower(path.Ext(filePath))
	switch {
	case extension == ".css":
		return KindStylesheet
	case extension == ".woff2":
		return KindFont
	case isImageExtension(extension):
		return KindImage
	default:
		return KindOther
	}
}

func isImageExtension(extension string) bool {
	switch strings.ToLower(extension) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".avif":
		return true
	default:
		return false
	}
}

func isMarpBuiltinImport(owner, raw string) bool {
	if !strings.HasPrefix(owner, "marp/") {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "default", "gaia", "uncover":
		return true
	default:
		return false
	}
}

func hasWindowsDrivePrefix(value string) bool {
	return len(value) >= 2 && ((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) && value[1] == ':'
}

func sortViolations(violations []Violation) {
	sort.Slice(violations, func(i, j int) bool {
		a, b := violations[i], violations[j]
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		if a.Owner != b.Owner {
			return a.Owner < b.Owner
		}
		if a.Reference != b.Reference {
			return a.Reference < b.Reference
		}
		return a.Detail < b.Detail
	})
}
