package themeasset

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDiscoverValidPortablePackage(t *testing.T) {
	spec := validFixtureSpec(filepath.Join("testdata", "valid"))
	report, err := Discover(spec)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if got, want := len(report.Assets), 4; got != want {
		t.Fatalf("len(Assets) = %d，want %d: %#v", got, want, report.Assets)
	}
	for _, asset := range report.Assets {
		if !asset.Referenced {
			t.Errorf("asset %q was not discovered as referenced", asset.Path)
		}
		if len(asset.SHA256) != 64 {
			t.Errorf("asset %q SHA256 length = %d，want 64", asset.Path, len(asset.SHA256))
		}
	}

	foundSeparatedURL := false
	foundBuiltin := false
	for _, ref := range report.References {
		if ref.Raw == "../assets/images/logo.png?revision=1#hero" {
			foundSeparatedURL = ref.Target == "assets/images/logo.png" && ref.Query == "revision=1" && ref.Fragment == "hero"
		}
		if ref.Kind == KindBuiltin && ref.Target == "@marp/default" {
			foundBuiltin = true
		}
	}
	if !foundSeparatedURL {
		t.Fatal("query and fragment were not separated from the canonical lookup path")
	}
	if !foundBuiltin {
		t.Fatal("Marp built-in theme import was not classified deterministically")
	}
}

func TestDiscoverIsDeterministic(t *testing.T) {
	spec := validFixtureSpec(filepath.Join("testdata", "valid"))
	first, err := Discover(spec)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Discover(spec)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("reports differ:\nfirst=%#v\nsecond=%#v", first, second)
	}
}

func TestValidFixtureMatchesFutureManifestShape(t *testing.T) {
	root := filepath.Join("testdata", "valid")
	source, err := os.ReadFile(filepath.Join(root, "karte-format.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Markdown struct {
			Layout string   `yaml:"layout"`
			Styles []string `yaml:"styles"`
		} `yaml:"markdown"`
		Marp struct {
			Themes []string `yaml:"themes"`
		} `yaml:"marp"`
		Assets struct {
			Directory string `yaml:"directory"`
		} `yaml:"assets"`
	}
	if err := yaml.Unmarshal(source, &manifest); err != nil {
		t.Fatal(err)
	}
	entries := append([]string{manifest.Markdown.Layout}, manifest.Markdown.Styles...)
	entries = append(entries, manifest.Marp.Themes...)
	got := Spec{PackageRoot: root, AssetDirectory: manifest.Assets.Directory, Entrypoints: entries}
	want := validFixtureSpec(root)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("manifest-derived spec = %#v，want %#v", got, want)
	}
}

func TestDiscoverRejectsUnsafeCSSReferences(t *testing.T) {
	tests := []struct {
		name string
		css  string
		code string
	}{
		{name: "remote quoted URL", css: `.x { background: url("https://example.test/a.png"); }`, code: "external-reference"},
		{name: "remote import", css: `@import "https://example.test/a.css";`, code: "external-reference"},
		{name: "data URL", css: `.x { background: url(data:image/png;base64，AAAA); }`, code: "embedded-reference"},
		{name: "package traversal", css: `.x { background: url(../../outside.png); }`, code: "package-escape"},
		{name: "encoded traversal", css: `.x { background: url(..%2f..%2foutside.png); }`, code: "encoded-path"},
		{name: "missing image", css: `.x { background: url(../assets/images/missing.png); }`, code: "missing-reference"},
		{name: "CSS escape", css: `.x { background: u\\72l(../assets/images/paper.webp); }`, code: "unsupported-css"},
		{name: "local font", css: `@font-face { font-family: "X"; src: local("X"); }`, code: "unsupported-css"},
		{name: "non-WOFF2 font", css: `@font-face { font-family: "X"; src: url(../assets/fonts/x.ttf) format("truetype"); }`, code: "unsupported-css"},
		{name: "unterminated comment", css: `/* url(https://example.test/a.png)`, code: "unsupported-css"},
		{name: "ambiguous image set", css: `.x { background: image-set("../assets/images/paper.webp" 1x); }`, code: "unsupported-css"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := makeMinimalPackage(t, `<main>{{CONTENT}}</main>`, test.css)
			_, err := Discover(minimalSpec(root))
			assertViolationCode(t, err, test.code)
		})
	}
}

func TestDiscoverRejectsUnsafeHTMLReferences(t *testing.T) {
	tests := []struct {
		name string
		html string
		code string
	}{
		{name: "remote source", html: `<img src="https://example.test/a.png">{{CONTENT}}`, code: "external-reference"},
		{name: "data source", html: `<img src="data:image/png;base64，AAAA">{{CONTENT}}`, code: "embedded-reference"},
		{name: "remote srcset", html: `<img src="../assets/images/paper.webp" srcset="https://example.test/a.png 2x">{{CONTENT}}`, code: "external-reference"},
		{name: "remote stylesheet", html: `<link rel="stylesheet" href="//example.test/a.css">{{CONTENT}}`, code: "external-reference"},
		{name: "active script", html: `<script src="../assets/code.js"></script>{{CONTENT}}`, code: "active-html"},
		{name: "event handler", html: `<img src="../assets/images/paper.webp" onerror="fetch('/leak')">{{CONTENT}}`, code: "event-handler-forbidden"},
		{name: "base URL", html: `<base href="../assets/"><main>{{CONTENT}}</main>`, code: "base-url-forbidden"},
		{name: "style attribute", html: `<main style="background:url(https://example.test/a.png)">{{CONTENT}}</main>`, code: "external-reference"},
		{name: "invalid srcset", html: `<img srcset="../assets/images/paper.webp 1x extra">{{CONTENT}}`, code: "invalid-srcset"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := makeMinimalPackage(t, test.html, `.x { color: black; }`)
			_, err := Discover(minimalSpec(root))
			assertViolationCode(t, err, test.code)
		})
	}
}

func TestDiscoverRejectsSymlinksAndBoundsMemory(t *testing.T) {
	t.Run("internal symlink", func(t *testing.T) {
		root := makeMinimalPackage(t, `<img src="../assets/images/link.png">{{CONTENT}}`, `.x { color: black; }`)
		target := filepath.Join(root, "assets", "images", "paper.webp")
		link := filepath.Join(root, "assets", "images", "link.png")
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		_, err := Discover(minimalSpec(root))
		if err == nil || !strings.Contains(err.Error(), "symlink-not-allowed") {
			t.Fatalf("Discover() error = %v，want symlink-not-allowed", err)
		}
	})

	t.Run("root symlink", func(t *testing.T) {
		root := makeMinimalPackage(t, `<main>{{CONTENT}}</main>`, `.x { color: black; }`)
		link := filepath.Join(t.TempDir(), "linked-package")
		if err := os.Symlink(root, link); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		spec := minimalSpec(link)
		_, err := Discover(spec)
		if err == nil || !strings.Contains(err.Error(), "must be a real directory") {
			t.Fatalf("Discover() error = %v，want root symlink rejection", err)
		}
	})

	t.Run("file size", func(t *testing.T) {
		root := makeMinimalPackage(t, `<main>{{CONTENT}}</main>`, `.x { color: black; }`)
		spec := minimalSpec(root)
		spec.Limits = Limits{MaxFiles: 20, MaxFileBytes: 8, MaxTextBytes: 8, MaxTotalBytes: 1024}
		_, err := Discover(spec)
		if err == nil || !strings.Contains(err.Error(), "file-size-limit") {
			t.Fatalf("Discover() error = %v，want file-size-limit", err)
		}
	})

	t.Run("file count", func(t *testing.T) {
		root := makeMinimalPackage(t, `<main>{{CONTENT}}</main>`, `.x { color: black; }`)
		spec := minimalSpec(root)
		spec.Limits = Limits{MaxFiles: 2, MaxFileBytes: 1024, MaxTextBytes: 1024, MaxTotalBytes: 4096}
		_, err := Discover(spec)
		if err == nil || !strings.Contains(err.Error(), "file-count-limit") {
			t.Fatalf("Discover() error = %v，want file-count-limit", err)
		}
	})

	t.Run("total bytes", func(t *testing.T) {
		root := makeMinimalPackage(t, `<main>{{CONTENT}}</main>`, `.x { color: black; }`)
		spec := minimalSpec(root)
		spec.Limits = Limits{MaxFiles: 20, MaxFileBytes: 1024, MaxTextBytes: 1024, MaxTotalBytes: 32}
		_, err := Discover(spec)
		if err == nil || !strings.Contains(err.Error(), "total-size-limit") {
			t.Fatalf("Discover() error = %v，want total-size-limit", err)
		}
	})

	t.Run("text bytes", func(t *testing.T) {
		root := makeMinimalPackage(t, `<main>{{CONTENT}}</main>`, `.x { color: black; }`)
		spec := minimalSpec(root)
		spec.Limits = Limits{MaxFiles: 20, MaxFileBytes: 1024, MaxTextBytes: 8, MaxTotalBytes: 4096}
		_, err := Discover(spec)
		assertViolationCode(t, err, "text-file-too-large")
	})
}

func TestLegacyDefaultThemeAuditIsExplicit(t *testing.T) {
	repositoryRoot := findRepositoryRoot(t)
	legacyRoot := filepath.Join(repositoryRoot, "themes", "default")
	report, err := Discover(Spec{
		PackageRoot:    legacyRoot,
		AssetDirectory: "assets",
		Entrypoints:    []string{"preview.html", "layout.html"},
	})
	if len(report.Entrypoints) != 2 {
		t.Fatalf("legacy entrypoints = %#v", report.Entrypoints)
	}
	validationErr := validationError(t, err)
	if countViolationCode(validationErr, "external-reference") < 3 {
		t.Fatalf("legacy validation errors do not preserve the known CDN finding: %v", err)
	}
	if countViolationCode(validationErr, "missing-assets-directory") != 1 {
		t.Fatalf("legacy validation errors do not preserve the missing asset directory finding: %v", err)
	}
	if countViolationCode(validationErr, "active-html") < 1 {
		t.Fatalf("legacy validation errors do not preserve active remote script finding: %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(repositoryRoot, "templates", "karte_data_template", "themes", "default")); !os.IsNotExist(statErr) {
		t.Fatalf("legacy default theme unexpectedly entered karte_data_template; update the audit and runtime packaging contract: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(legacyRoot, "karte-format.yaml")); !os.IsNotExist(statErr) {
		t.Fatalf("legacy default theme unexpectedly gained a format manifest; update the migration audit: %v", statErr)
	}
}

func validFixtureSpec(root string) Spec {
	return Spec{
		PackageRoot:    root,
		AssetDirectory: "assets",
		Entrypoints: []string{
			"markdown/layout.html",
			"markdown/base.css",
			"markdown/print.css",
			"marp/karte.css",
		},
	}
}

func minimalSpec(root string) Spec {
	return Spec{
		PackageRoot:    root,
		AssetDirectory: "assets",
		Entrypoints:    []string{"markdown/layout.html", "markdown/base.css"},
	}
}

func makeMinimalPackage(t *testing.T, layout, css string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "fixture")
	for _, directory := range []string{
		filepath.Join(root, "markdown"),
		filepath.Join(root, "assets", "fonts"),
		filepath.Join(root, "assets", "images"),
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"markdown/layout.html":     layout,
		"markdown/base.css":        css,
		"assets/fonts/x.ttf":       "font",
		"assets/fonts/x.woff2":     "font",
		"assets/images/paper.webp": "image",
		"assets/code.js":           "code",
		"karte-format.yaml":        "schemaVersion: 1\n",
	}
	paths := make([]string, 0, len(files))
	for filePath := range files {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)
	for _, filePath := range paths {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(filePath)), []byte(files[filePath]), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func assertViolationCode(t *testing.T, err error, code string) {
	t.Helper()
	validationErr := validationError(t, err)
	if countViolationCode(validationErr, code) == 0 {
		t.Fatalf("Discover() error = %v，want violation code %q", err, code)
	}
}

func validationError(t *testing.T, err error) *ValidationError {
	t.Helper()
	if err == nil {
		t.Fatal("Discover() error = nil，want validation failure")
	}
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("Discover() error type = %T，want *ValidationError: %v", err, err)
	}
	return validationErr
}

func countViolationCode(err *ValidationError, code string) int {
	count := 0
	for _, violation := range err.Violations {
		if violation.Code == code {
			count++
		}
	}
	return count
}

func findRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
