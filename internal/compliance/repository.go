package compliance

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const canonicalProjectLicense = `MIT License

Copyright (c) 2024 kirimine170

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
`

const (
	AssetInventoryPath     = "compliance/assets.json"
	ComponentInventoryPath = "compliance/components.json"
	NoticesPath            = "THIRD_PARTY_NOTICES.md"
	SBOMPath               = "bom.cdx.json"
)

type GeneratedFiles struct {
	Assets     []byte
	Components []byte
	Notices    []byte
	SBOM       []byte
}

func GenerateRepositoryFiles(ctx context.Context, repositoryRoot string) (GeneratedFiles, ScanResult, error) {
	if err := validateProjectLicense(repositoryRoot); err != nil {
		return GeneratedFiles{}, ScanResult{}, err
	}
	var assetRegistry AssetSourceRegistry
	if err := LoadJSONFile(filepath.Join(repositoryRoot, "compliance", "asset-sources.json"), &assetRegistry); err != nil {
		return GeneratedFiles{}, ScanResult{}, err
	}
	assetInventory, err := BuildAssetInventory(repositoryRoot, assetRegistry)
	if err != nil {
		return GeneratedFiles{}, ScanResult{}, err
	}
	assetJSON, err := MarshalDeterministicJSON(assetInventory)
	if err != nil {
		return GeneratedFiles{}, ScanResult{}, err
	}
	result, err := ScanRepository(ctx, repositoryRoot)
	if err != nil {
		return GeneratedFiles{}, ScanResult{}, err
	}
	componentJSON, err := RenderComponentInventory(result.Components)
	if err != nil {
		return GeneratedFiles{}, ScanResult{}, err
	}
	notices, err := RenderThirdPartyNotices(result.Components)
	if err != nil {
		return GeneratedFiles{}, ScanResult{}, err
	}
	sbom, err := RenderCycloneDX(result.Components)
	if err != nil {
		return GeneratedFiles{}, ScanResult{}, err
	}
	return GeneratedFiles{Assets: assetJSON, Components: componentJSON, Notices: notices, SBOM: sbom}, result, nil
}

func validateProjectLicense(repositoryRoot string) error {
	absoluteRoot, err := filepath.Abs(repositoryRoot)
	if err != nil {
		return fmt.Errorf("resolve repository root: %w", err)
	}
	rootInfo, err := os.Lstat(absoluteRoot)
	if err != nil {
		return fmt.Errorf("inspect repository root: %w", err)
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("repository root symlink is not allowed for project license validation")
	}
	licensePath, err := confinedPath(absoluteRoot, "LICENSE")
	if err != nil {
		return fmt.Errorf("project LICENSE path: %w", err)
	}
	licenseInfo, err := os.Lstat(licensePath)
	if err != nil {
		return fmt.Errorf("project LICENSE is missing or unavailable: %w", err)
	}
	if !licenseInfo.Mode().IsRegular() {
		return errors.New("project LICENSE must be a confined regular non-symlink file")
	}
	licenseBytes, err := os.ReadFile(licensePath)
	if err != nil {
		return fmt.Errorf("read project LICENSE: %w", err)
	}
	normalized, err := normalizeText(licenseBytes)
	if err != nil {
		return fmt.Errorf("normalize project LICENSE: %w", err)
	}
	if detected := recognizeLicenseBody([]byte(normalized)); detected != "MIT" {
		return fmt.Errorf("project LICENSE text identifies %q，want MIT", detected)
	}
	if normalized != canonicalProjectLicense {
		return errors.New("project LICENSE must exactly match the canonical Karte MIT license text and copyright notice")
	}
	return nil
}

func WriteGeneratedFiles(repositoryRoot string, generated GeneratedFiles) error {
	for path, data := range map[string][]byte{
		AssetInventoryPath: generated.Assets, ComponentInventoryPath: generated.Components,
		NoticesPath: generated.Notices, SBOMPath: generated.SBOM,
	} {
		absolute, err := confinedPath(repositoryRoot, path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(absolute, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func VerifyGeneratedFiles(repositoryRoot string, generated GeneratedFiles) error {
	var stale []string
	for path, expected := range map[string][]byte{
		AssetInventoryPath: generated.Assets, ComponentInventoryPath: generated.Components,
		NoticesPath: generated.Notices, SBOMPath: generated.SBOM,
	} {
		actual, err := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(path)))
		if err != nil || !bytes.Equal(actual, expected) {
			stale = append(stale, path)
		}
	}
	if len(stale) == 0 {
		return nil
	}
	sort.Strings(stale)
	return fmt.Errorf("generated compliance files are missing or stale: %s", strings.Join(stale, "，"))
}

func AuditRepository(ctx context.Context, repositoryRoot string, now time.Time) error {
	generated, result, err := GenerateRepositoryFiles(ctx, repositoryRoot)
	if err != nil {
		return err
	}
	var policy Policy
	if err := LoadJSONFile(filepath.Join(repositoryRoot, "compliance", "policy.json"), &policy); err != nil {
		return err
	}
	var exceptions ExceptionRegistry
	if err := LoadJSONFile(filepath.Join(repositoryRoot, "compliance", "license-exceptions.json"), &exceptions); err != nil {
		return err
	}
	var violations []string
	violations = append(violations, result.Problems...)
	if err := ValidateComponentLicenses(policy, exceptions, result.Components, now); err != nil {
		violations = append(violations, strings.Split(err.Error(), "\n")...)
	}
	if err := validateRedistributionEvidence(policy, result.Components); err != nil {
		violations = append(violations, strings.Split(err.Error(), "\n")...)
	}
	if err := VerifyGeneratedFiles(repositoryRoot, generated); err != nil {
		violations = append(violations, err.Error())
	}
	if len(violations) == 0 {
		return nil
	}
	sort.Strings(violations)
	return errors.New(strings.Join(uniqueStrings(violations), "\n"))
}

func validateRedistributionEvidence(policy Policy, components []Component) error {
	canonical := make(map[string]struct{})
	deniedFamilies := stringSet(policy.DeniedLicenseFamilies)
	var violations []string
	for _, component := range components {
		for _, evidence := range component.LicenseEvidence {
			if evidence.Kind != "license" && evidence.Kind != "notice" {
				violations = append(violations, fmt.Sprintf("%s has unsupported evidence kind %q", component.ID, evidence.Kind))
				continue
			}
			if strings.TrimSpace(evidence.Path) == "" || filepath.IsAbs(evidence.Path) || filepath.Clean(evidence.Path) == ".." || strings.HasPrefix(filepath.Clean(evidence.Path), ".."+string(filepath.Separator)) {
				violations = append(violations, fmt.Sprintf("%s has invalid evidence path %q", component.ID, evidence.Path))
				continue
			}
			if !sha256Pattern.MatchString(evidence.SHA256) || sha256Hex([]byte(evidence.Text)) != evidence.SHA256 {
				violations = append(violations, fmt.Sprintf("%s has evidence content/hash mismatch for %q", component.ID, evidence.Path))
				continue
			}
			if evidence.Kind != "license" || strings.TrimSpace(evidence.Text) == "" {
				continue
			}
			license := recognizeLicense(evidence.Path, []byte(evidence.Text))
			licenses, parseErr := parseLicenseExpression(license)
			if parseErr == nil {
				for _, term := range licenses {
					canonical[licenseBaseIdentifier(term)] = struct{}{}
				}
			}
		}
	}
	for _, component := range components {
		if component.License == "NOASSERTION" {
			continue
		}
		if component.Scope == "build" {
			continue
		}
		if hasValidLicenseEvidence(component) {
			continue
		}
		licenses, err := parseLicenseExpression(component.License)
		if err != nil {
			continue
		}
		denied := false
		for _, license := range licenses {
			if _, blocked := deniedFamilies[licenseFamily(license)]; blocked {
				denied = true
				break
			}
		}
		// Policy-denied components cannot be distributed at all．Avoid adding
		// a second，less actionable evidence error while retaining the primary
		// GPL／AGPL blocker from ValidateComponentLicenses．
		if denied {
			continue
		}
		mapping := strings.TrimSpace(component.Properties["redistributionLicenseMapping"])
		if mapping == "" {
			violations = append(violations, fmt.Sprintf("%s has no verified license evidence or explicit redistribution mapping", component.ID))
			continue
		}
		mappedLicenses, mapErr := parseLicenseExpression(mapping)
		if mapErr != nil {
			violations = append(violations, fmt.Sprintf("%s has malformed redistribution mapping %q", component.ID, mapping))
			continue
		}
		want := make([]string, 0, len(licenses))
		for _, license := range licenses {
			want = append(want, licenseBaseIdentifier(license))
		}
		for index := range mappedLicenses {
			mappedLicenses[index] = licenseBaseIdentifier(mappedLicenses[index])
		}
		sort.Strings(want)
		sort.Strings(mappedLicenses)
		if strings.Join(want, "\x00") != strings.Join(mappedLicenses, "\x00") {
			violations = append(violations, fmt.Sprintf("%s redistribution mapping %q does not match license %q", component.ID, mapping, component.License))
			continue
		}
		for _, license := range mappedLicenses {
			if _, ok := canonical[license]; !ok {
				violations = append(violations, fmt.Sprintf("%s redistribution mapping has no verified canonical text for %s", component.ID, license))
			}
		}
	}
	if len(violations) == 0 {
		return nil
	}
	sort.Strings(violations)
	return errors.New(strings.Join(violations, "\n"))
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	result := values[:0]
	for index, value := range values {
		if index == 0 || value != values[index-1] {
			result = append(result, value)
		}
	}
	return result
}
