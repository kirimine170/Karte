package compliance

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateProjectLicenseRequiresMITRegularFileAndTrustedRoot(t *testing.T) {
	t.Run("valid MIT", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, root, "LICENSE", testProjectMITLicense)
		if err := validateProjectLicense(root); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("wrong copyright holder and year", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, root, "LICENSE", testMITLicense)
		err := validateProjectLicense(root)
		if err == nil || !strings.Contains(err.Error(), "exactly match the canonical") {
			t.Fatalf("expected project copyright failure，got %v", err)
		}
	})
	t.Run("canonical MIT plus additional restriction", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, root, "LICENSE", testProjectMITLicense+"Additional restriction: commercial use is prohibited.\n")
		err := validateProjectLicense(root)
		if err == nil || !strings.Contains(err.Error(), `identifies "LicenseRef-Ambiguous"，want MIT`) {
			t.Fatalf("expected added restriction failure，got %v", err)
		}
	})
	t.Run("missing", func(t *testing.T) {
		err := validateProjectLicense(t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "missing or unavailable") {
			t.Fatalf("expected missing LICENSE failure，got %v", err)
		}
	})
	t.Run("forged SPDX with wrong body", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, root, "LICENSE", "SPDX-License-Identifier: MIT\n\nApache License\nVersion 2.0\n")
		err := validateProjectLicense(root)
		if err == nil || !strings.Contains(err.Error(), `identifies "Apache-2.0"，want MIT`) {
			t.Fatalf("expected wrong project license failure，got %v", err)
		}
	})
	t.Run("symlinked LICENSE", func(t *testing.T) {
		root := t.TempDir()
		outside := filepath.Join(t.TempDir(), "LICENSE")
		if err := os.WriteFile(outside, []byte(testProjectMITLicense), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(root, "LICENSE")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		err := validateProjectLicense(root)
		if err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("expected symlinked LICENSE failure，got %v", err)
		}
	})
	t.Run("symlinked repository root", func(t *testing.T) {
		root := t.TempDir()
		writeTestFile(t, root, "LICENSE", testProjectMITLicense)
		linkedRoot := filepath.Join(t.TempDir(), "repository")
		if err := os.Symlink(root, linkedRoot); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		err := validateProjectLicense(linkedRoot)
		if err == nil || !strings.Contains(err.Error(), "repository root symlink") {
			t.Fatalf("expected symlinked repository root failure，got %v", err)
		}
	})
}

func TestComplianceRenderingIsDeterministicAcrossInputOrder(t *testing.T) {
	normalized, err := normalizeText([]byte(testMITLicense))
	if err != nil {
		t.Fatal(err)
	}
	evidence := LicenseEvidence{Kind: "license", Path: "LICENSE", SHA256: sha256Hex([]byte(normalized)), Text: normalized}
	first := testComponent("npm:first", "MIT")
	first.LicenseEvidence = []LicenseEvidence{evidence}
	second := testComponent("npm:second", "MIT")
	second.LicenseEvidence = []LicenseEvidence{evidence}
	left := []Component{second, first}
	right := []Component{first, second}
	for name, render := range map[string]func([]Component) ([]byte, error){
		"notices":   RenderThirdPartyNotices,
		"sbom":      RenderCycloneDX,
		"inventory": RenderComponentInventory,
	} {
		t.Run(name, func(t *testing.T) {
			leftBytes, err := render(left)
			if err != nil {
				t.Fatal(err)
			}
			rightBytes, err := render(right)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(leftBytes, rightBytes) {
				t.Fatalf("%s output depends on scan order", name)
			}
			if name == "notices" && !bytes.Contains(leftBytes, []byte("Permission is hereby granted")) {
				t.Fatal("notices omitted the redistributed license text")
			}
		})
	}
}

func TestValidateRedistributionEvidenceRejectsNoticeOnlyAndHashMismatch(t *testing.T) {
	normalized, err := normalizeText([]byte(testMITLicense))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		evidence   LicenseEvidence
		wantString string
	}{
		{
			name: "notice-only",
			evidence: LicenseEvidence{
				Kind: "notice", Path: "NOTICE", SHA256: sha256Hex([]byte(normalized)), Text: normalized,
			},
			wantString: "no verified license evidence",
		},
		{
			name: "hash-mismatch",
			evidence: LicenseEvidence{
				Kind: "license", Path: "LICENSE", SHA256: strings.Repeat("0", 64), Text: normalized,
			},
			wantString: "content/hash mismatch",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			component := testComponent("npm:test", "MIT")
			component.LicenseEvidence = []LicenseEvidence{test.evidence}
			err := validateRedistributionEvidence(testPolicy(), []Component{component})
			if err == nil || !strings.Contains(err.Error(), test.wantString) {
				t.Fatalf("expected %q，got %v", test.wantString, err)
			}
		})
	}
}

func TestValidateRedistributionEvidenceRequiresExplicitMatchingMapping(t *testing.T) {
	normalized, err := normalizeText([]byte(testMITLicense))
	if err != nil {
		t.Fatal(err)
	}
	canonical := testComponent("npm:canonical", "MIT")
	canonical.LicenseEvidence = []LicenseEvidence{{
		Kind: "license", Path: "npm/canonical/LICENSE",
		SHA256: sha256Hex([]byte(normalized)), Text: normalized,
	}}
	mapped := testComponent("npm:mapped", "MIT")
	mapped.Properties = map[string]string{"redistributionLicenseMapping": "MIT"}
	if err := validateRedistributionEvidence(testPolicy(), []Component{canonical, mapped}); err != nil {
		t.Fatalf("explicit mapping should pass: %v", err)
	}
	mapped.Properties["redistributionLicenseMapping"] = "Apache-2.0"
	err = validateRedistributionEvidence(testPolicy(), []Component{canonical, mapped})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched mapping should fail，got %v", err)
	}
}

func TestValidateRedistributionEvidenceDoesNotDuplicateDeniedPolicyFailure(t *testing.T) {
	component := testComponent("native:gcc", "GPL-3.0-or-later WITH GCC-exception-3.1")
	if err := validateRedistributionEvidence(testPolicy(), []Component{component}); err != nil {
		t.Fatalf("denied component should be reported by license policy only，got %v", err)
	}
}

func TestValidateRedistributionEvidenceExcludesBuildOnlyComponents(t *testing.T) {
	component := testComponent("npm:build-tool", "MIT")
	component.Scope = "build"
	if err := validateRedistributionEvidence(testPolicy(), []Component{component}); err != nil {
		t.Fatalf("build-only component is not redistributed，got %v", err)
	}
	component.Scope = "runtime"
	if err := validateRedistributionEvidence(testPolicy(), []Component{component}); err == nil {
		t.Fatal("runtime component without evidence must fail")
	}
}

func TestRenderThirdPartyNoticesUsesOnlyExplicitCanonicalMapping(t *testing.T) {
	normalized, err := normalizeText([]byte(testMITLicense))
	if err != nil {
		t.Fatal(err)
	}
	canonical := testComponent("npm:canonical", "MIT")
	canonical.LicenseEvidence = []LicenseEvidence{{
		Kind: "license", Path: "npm/canonical/LICENSE",
		SHA256: sha256Hex([]byte(normalized)), Text: normalized,
	}}
	mapped := testComponent("npm:mapped", "MIT")
	mapped.Properties = map[string]string{"redistributionLicenseMapping": "MIT"}
	notices, err := RenderThirdPartyNotices([]Component{mapped, canonical})
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(notices), sha256Hex([]byte(normalized))); count < 3 {
		t.Fatalf("canonical text hash was not associated with both components，count=%d", count)
	}
}
