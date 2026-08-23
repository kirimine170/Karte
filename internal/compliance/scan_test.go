package compliance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testMITLicense = `MIT License

Copyright (c) Test

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction.
`

const testProjectMITLicense = canonicalProjectLicense

const testGPL3License = `GNU GENERAL PUBLIC LICENSE
Version 3, 29 June 2007
`

const testApacheLicense = `Apache License
Version 2.0, January 2004
`

func TestNormalizeTextPropagatesScannerError(t *testing.T) {
	_, err := normalizeText([]byte(strings.Repeat("x", 4*1024*1024+1)))
	if err == nil {
		t.Fatal("expected an overlong license line to fail instead of being truncated")
	}
}

func TestLicenseRecognitionRejectsAmbiguousAndContradictoryEvidence(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		text     string
		bodyOnly bool
	}{
		{name: "Apache and GPL bodies", filename: "LICENSE", text: testApacheLicense + testGPL3License, bodyOnly: true},
		{name: "MIT and GPL bodies", filename: "LICENSE", text: testMITLicense + testGPL3License, bodyOnly: true},
		{name: "MIT plus unknown restriction", filename: "LICENSE", text: testMITLicense + "Additional restriction: commercial use is prohibited.\n", bodyOnly: true},
		{name: "bundled marker cannot bless GPL", filename: "LICENSE", text: "Licenses of bundled dependencies\n" + testApacheLicense + testGPL3License, bodyOnly: true},
		{name: "forged SPDX MIT with GPL body", filename: "LICENSE", text: "SPDX-License-Identifier: MIT\n" + testGPL3License},
		{name: "Apache filename with GPL body", filename: "LICENSE-Apache", text: testGPL3License},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := recognizeLicense(test.filename, []byte(test.text))
			if test.bodyOnly {
				got = recognizeLicenseBody([]byte(test.text))
			}
			if got != ambiguousLicenseExpression {
				t.Fatalf("recognized %q，want %q", got, ambiguousLicenseExpression)
			}
		})
	}
}

func TestLicenseRecognitionAcceptsKnownExplicitPermissiveCompoundBody(t *testing.T) {
	text := "This project is covered by two different licenses.\n" + testApacheLicense + testMITLicense
	if got := recognizeLicenseBody([]byte(text)); got != "Apache-2.0 AND MIT" {
		t.Fatalf("recognized %q，want known compound expression", got)
	}
}

func TestLicenseRecognitionAcceptsExplicitCompoundHeaderOnlyWhenBodyTermsMatch(t *testing.T) {
	text := "SPDX-License-Identifier: Unlicense OR MIT\n" + testMITLicense +
		"This is free and unencumbered software released into the public domain.\n"
	if got := recognizeLicense("LICENSE", []byte(text)); got != "Unlicense OR MIT" {
		t.Fatalf("recognized %q，want explicit compound expression", got)
	}
}

func TestScanGoDependenciesSeparatesDesktopRuntimeFromToolOnly(t *testing.T) {
	root := t.TempDir()
	runtimeDir := filepath.Join(root, "runtime")
	toolDir := filepath.Join(root, "tool")
	for _, directory := range []string{runtimeDir, toolDir} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "LICENSE"), []byte(testMITLicense), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	modules := []goModule{
		{Path: "karte", Main: true},
		{Path: "example.test/runtime", Version: "v1.0.0", Dir: runtimeDir, Sum: "h1:runtime", GoModSum: "h1:runtime-mod"},
		{Path: "example.test/tool", Version: "v2.0.0", Dir: toolDir, Sum: "h1:tool", GoModSum: "h1:tool-mod"},
	}
	var moduleJSON strings.Builder
	encoder := json.NewEncoder(&moduleJSON)
	for _, module := range modules {
		if err := encoder.Encode(module); err != nil {
			t.Fatal(err)
		}
	}
	runner := func(_ context.Context, name string, arguments ...string) ([]byte, error) {
		if name != "go" || strings.Join(arguments, " ") != "list -m -json all" {
			t.Fatalf("unexpected command %s %v", name, arguments)
		}
		return []byte(moduleJSON.String()), nil
	}
	result, err := scanGoDependencies(context.Background(), root, runner, map[string][]string{
		"example.test/runtime": {"darwin/arm64", "windows/amd64"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Components) != 2 {
		t.Fatalf("components = %d，want 2", len(result.Components))
	}
	byName := map[string]Component{}
	for _, component := range result.Components {
		byName[component.Name] = component
	}
	if got := byName["example.test/runtime"].Scope; got != "runtime" {
		t.Fatalf("runtime scope = %q", got)
	}
	if got := byName["example.test/runtime"].Properties["desktopTargets"]; got != "darwin/arm64，windows/amd64" {
		t.Fatalf("runtime targets = %q", got)
	}
	if got := byName["example.test/tool"].Scope; got != "build" {
		t.Fatalf("tool scope = %q", got)
	}
}

func TestNPMDependencyClosuresKeepProductionAndDevelopmentSeparate(t *testing.T) {
	lock := npmLock{Packages: map[string]npmPackageEntry{
		"": {
			Dependencies:    map[string]string{"runtime": "1"},
			devDependencies: map[string]string{"tool": "1"},
		},
		"node_modules/runtime":                      {Dependencies: map[string]string{"shared": "1"}},
		"node_modules/shared":                       {},
		"node_modules/tool":                         {Dependencies: map[string]string{"tool-child": "1"}},
		"node_modules/tool/node_modules/tool-child": {},
	}}
	runtimePaths, buildPaths, problems := npmDependencyClosures(lock)
	if len(problems) != 0 {
		t.Fatalf("closure problems: %v", problems)
	}
	for _, path := range []string{"node_modules/runtime", "node_modules/shared"} {
		if _, ok := runtimePaths[path]; !ok {
			t.Errorf("runtime closure is missing %s", path)
		}
	}
	if _, ok := runtimePaths["node_modules/tool"]; ok {
		t.Fatal("dev-only tool leaked into the production closure")
	}
	for _, path := range []string{"node_modules/tool", "node_modules/tool/node_modules/tool-child"} {
		if _, ok := buildPaths[path]; !ok {
			t.Errorf("build closure is missing %s", path)
		}
	}
}

func TestScanNPMDependenciesDoesNotInventRedistributionMapping(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "frontend", "node_modules", "no-license"), 0o755); err != nil {
		t.Fatal(err)
	}
	lock := `{
  "name": "fixture",
  "version": "1.0.0",
  "lockfileVersion": 3,
  "packages": {
    "": {"dependencies": {"no-license": "1.0.0"}},
    "node_modules/no-license": {
      "version": "1.0.0",
      "license": "MIT",
      "resolved": "https://registry.npmjs.org/no-license/-/no-license-1.0.0.tgz",
      "integrity": "sha512-Zml4dHVyZQ=="
    }
  }
}`
	if err := os.WriteFile(filepath.Join(root, "frontend", "package-lock.json"), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := scanNPMDependencies(root, NPMOverrideRegistry{SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Components) != 1 {
		t.Fatalf("components = %d", len(result.Components))
	}
	if mapping := result.Components[0].Properties["redistributionLicenseMapping"]; mapping != "" {
		t.Fatalf("scanner invented redistribution mapping %q", mapping)
	}
}

func TestScanNPMDependenciesAppliesExplicitEvidenceToDeclaredLicense(t *testing.T) {
	root := t.TempDir()
	evidencePath := "frontend/node_modules/runtime/README.md"
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, evidencePath)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, evidencePath), []byte(testMITLicense), 0o644); err != nil {
		t.Fatal(err)
	}
	lock := `{
  "name": "fixture",
  "version": "1.0.0",
  "lockfileVersion": 3,
  "packages": {
    "": {"dependencies": {"runtime": "1.0.0"}},
    "node_modules/runtime": {
      "version": "1.0.0",
      "license": "MIT",
      "resolved": "https://registry.npmjs.org/runtime/-/runtime-1.0.0.tgz",
      "integrity": "sha512-Zml4dHVyZQ=="
    }
  }
}`
	if err := os.WriteFile(filepath.Join(root, "frontend", "package-lock.json"), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	normalized, err := normalizeText([]byte(testMITLicense))
	if err != nil {
		t.Fatal(err)
	}
	override := NPMLicenseOverride{
		Path: "node_modules/runtime", Version: "1.0.0", License: "MIT",
		EvidencePath: evidencePath, EvidenceSHA256: sha256Hex([]byte(normalized)),
		EvidenceSource: "https://registry.npmjs.org/runtime/-/runtime-1.0.0.tgz",
	}
	result, err := scanNPMDependencies(root, NPMOverrideRegistry{SchemaVersion: SchemaVersion, Overrides: []NPMLicenseOverride{override}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Problems) != 0 || len(result.Components) != 1 || !hasValidLicenseEvidence(result.Components[0]) {
		t.Fatalf("explicit override did not hydrate evidence: %+v", result)
	}
	if got := result.Components[0].Properties["licenseEvidenceSource"]; got != override.EvidenceSource {
		t.Fatalf("evidence source = %q，want %q", got, override.EvidenceSource)
	}
}

func TestScanNPMDependenciesRejectsStaleUnusedOverride(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "frontend"), 0o755); err != nil {
		t.Fatal(err)
	}
	lock := `{
  "name": "fixture",
  "version": "1.0.0",
  "lockfileVersion": 3,
  "packages": {
    "": {"dependencies": {"runtime": "1.0.0"}},
    "node_modules/runtime": {
      "version": "1.0.0",
      "license": "MIT",
      "resolved": "https://registry.npmjs.org/runtime/-/runtime-1.0.0.tgz",
      "integrity": "sha512-Zml4dHVyZQ=="
    }
  }
}`
	if err := os.WriteFile(filepath.Join(root, "frontend", "package-lock.json"), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	override := NPMLicenseOverride{
		Path: "node_modules/stale", Version: "9.9.9", License: "MIT",
		EvidencePath: "frontend/evidence/LICENSE", EvidenceSHA256: strings.Repeat("0", 64),
		EvidenceSource: "https://registry.npmjs.org/runtime/-/runtime-1.0.0.tgz",
	}
	_, err := scanNPMDependencies(root, NPMOverrideRegistry{SchemaVersion: SchemaVersion, Overrides: []NPMLicenseOverride{override}})
	if err == nil || !strings.Contains(err.Error(), "stale or unused npm license overrides: node_modules/stale@9.9.9") {
		t.Fatalf("expected stale override failure，got %v", err)
	}
}

func TestScanNPMDependenciesRejectsOverrideWhenLocalLicenseContradictsLock(t *testing.T) {
	root := t.TempDir()
	localPath := filepath.Join(root, "frontend", "node_modules", "runtime", "LICENSE")
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localPath, []byte(testGPL3License), 0o644); err != nil {
		t.Fatal(err)
	}
	evidencePath := "frontend/override-evidence.txt"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(evidencePath)), []byte(testMITLicense), 0o644); err != nil {
		t.Fatal(err)
	}
	lock := `{
  "name": "fixture",
  "version": "1.0.0",
  "lockfileVersion": 3,
  "packages": {
    "": {"dependencies": {"runtime": "1.0.0"}},
    "node_modules/runtime": {
      "version": "1.0.0",
      "license": "MIT",
      "resolved": "https://registry.npmjs.org/runtime/-/runtime-1.0.0.tgz",
      "integrity": "sha512-Zml4dHVyZQ=="
    }
  }
}`
	if err := os.WriteFile(filepath.Join(root, "frontend", "package-lock.json"), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	normalized, err := normalizeText([]byte(testMITLicense))
	if err != nil {
		t.Fatal(err)
	}
	override := NPMLicenseOverride{
		Path: "node_modules/runtime", Version: "1.0.0", License: "MIT",
		EvidencePath: evidencePath, EvidenceSHA256: sha256Hex([]byte(normalized)),
		EvidenceSource: "https://registry.npmjs.org/runtime/-/runtime-1.0.0.tgz",
	}
	result, err := scanNPMDependencies(root, NPMOverrideRegistry{SchemaVersion: SchemaVersion, Overrides: []NPMLicenseOverride{override}})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(result.Problems, "\n")
	for _, want := range []string{"override is not allowed", `local license "GPL-3.0-only" does not match package-lock "MIT"`} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing problem %q in %q", want, joined)
		}
	}
	if len(result.Components) != 1 || len(result.Components[0].LicenseEvidence) != 1 {
		t.Fatalf("override evidence masked local GPL evidence: %+v", result.Components)
	}
}

func TestScanNPMDependenciesUsesRecognizedLocalEvidenceWhenLockOmitsLicense(t *testing.T) {
	root := t.TempDir()
	licensePath := filepath.Join(root, "frontend", "node_modules", "runtime", "LICENSE")
	if err := os.MkdirAll(filepath.Dir(licensePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(licensePath, []byte(testMITLicense), 0o644); err != nil {
		t.Fatal(err)
	}
	lock := `{
  "name": "fixture",
  "version": "1.0.0",
  "lockfileVersion": 3,
  "packages": {
    "": {"dependencies": {"runtime": "1.0.0"}},
    "node_modules/runtime": {
      "version": "1.0.0",
      "resolved": "https://registry.npmjs.org/runtime/-/runtime-1.0.0.tgz",
      "integrity": "sha512-Zml4dHVyZQ=="
    }
  }
}`
	if err := os.WriteFile(filepath.Join(root, "frontend", "package-lock.json"), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := scanNPMDependencies(root, NPMOverrideRegistry{SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Problems) != 0 || len(result.Components) != 1 || result.Components[0].License != "MIT" {
		t.Fatalf("recognized local evidence was not authoritative: %+v", result)
	}
}

func TestTargetEnvironmentReplacesHostTargetSettings(t *testing.T) {
	got := targetEnvironment([]string{"PATH=/bin", "GOOS=plan9", "GOARCH=386", "CGO_ENABLED=0"}, desktopTarget{GOOS: "darwin", GOARCH: "arm64"})
	joined := strings.Join(got, "\n")
	for _, forbidden := range []string{"GOOS=plan9", "GOARCH=386", "CGO_ENABLED=0"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("stale setting %q remained in %q", forbidden, joined)
		}
	}
	for _, want := range []string{"GOOS=darwin", "GOARCH=arm64", "CGO_ENABLED=1"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("target setting %q is missing from %q", want, joined)
		}
	}
}
