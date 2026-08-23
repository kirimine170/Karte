package compliance

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

func RenderThirdPartyNotices(components []Component) ([]byte, error) {
	ordered := append([]Component(nil), components...)
	sortComponents(ordered)

	type evidenceRecord struct {
		Evidence LicenseEvidence
		Licenses []string
	}
	evidenceByHash := make(map[string]*evidenceRecord)
	canonicalByLicense := make(map[string]LicenseEvidence)
	for _, component := range ordered {
		for _, evidence := range component.LicenseEvidence {
			if evidence.Kind != "license" && evidence.Kind != "notice" {
				return nil, fmt.Errorf("%s has unsupported evidence kind %q", component.ID, evidence.Kind)
			}
			if !sha256Pattern.MatchString(evidence.SHA256) || strings.TrimSpace(evidence.Text) == "" || sha256Hex([]byte(evidence.Text)) != evidence.SHA256 {
				return nil, fmt.Errorf("%s has incomplete %s evidence %q", component.ID, evidence.Kind, evidence.Path)
			}
			record := evidenceByHash[evidence.SHA256]
			if record == nil {
				record = &evidenceRecord{Evidence: evidence}
				evidenceByHash[evidence.SHA256] = record
			}
			if evidence.Kind == "license" {
				detected := recognizeLicense(evidence.Path, []byte(evidence.Text))
				terms, parseErr := parseLicenseExpression(detected)
				if parseErr == nil {
					for _, term := range terms {
						base := licenseBaseIdentifier(term)
						if _, ok := canonicalByLicense[base]; !ok {
							canonicalByLicense[base] = evidence
						}
					}
				}
			}
		}
	}

	componentEvidence := make(map[string][]string, len(ordered))
	for _, component := range ordered {
		hasLicenseEvidence := hasValidLicenseEvidence(component)
		for _, evidence := range component.LicenseEvidence {
			componentEvidence[component.ID] = append(componentEvidence[component.ID], evidence.SHA256)
		}
		if !hasLicenseEvidence {
			mapping := component.Properties["redistributionLicenseMapping"]
			licenses, err := parseLicenseExpression(mapping)
			if err == nil {
				for _, license := range licenses {
					if evidence, ok := canonicalByLicense[licenseBaseIdentifier(license)]; ok {
						componentEvidence[component.ID] = append(componentEvidence[component.ID], evidence.SHA256)
					}
				}
			}
		}
		sort.Strings(componentEvidence[component.ID])
		for _, hash := range componentEvidence[component.ID] {
			record := evidenceByHash[hash]
			if record != nil {
				record.Licenses = append(record.Licenses, component.ID)
			}
		}
	}

	var output bytes.Buffer
	output.WriteString("# Third-Party Notices\n\n")
	output.WriteString("This file is generated deterministically by `go run ./cmd/licensegate generate`．Do not edit it directly．\n\n")
	output.WriteString("Karte itself is licensed under the MIT License in `LICENSE`．The table below inventories checksum-pinned Go modules，npm packages，native runtimes，fonts，PDF resources，and the bundled ASR model．A listed component is not policy approval by itself; `licensegate audit` remains fail-closed for denied or missing licenses．\n\n")
	output.WriteString("| Type | Component | Version | Scope | License | Source | Redistributed license／notice |\n")
	output.WriteString("| --- | --- | --- | --- | --- | --- | --- |\n")
	for _, component := range ordered {
		refs := make([]string, 0, len(componentEvidence[component.ID]))
		for _, hash := range componentEvidence[component.ID] {
			refs = append(refs, fmt.Sprintf("[text `%s`](#license-text-%s)", hash[:12], hash))
		}
		if len(refs) == 0 {
			if component.Scope == "build" {
				refs = append(refs, "not redistributed (build-only)")
			} else {
				refs = append(refs, "**missing — policy blocker**")
			}
		}
		fmt.Fprintf(&output, "| %s | `%s` | %s | %s | `%s` | [upstream](%s) | %s |\n",
			escapeMarkdown(component.Type), escapeMarkdown(component.Name), escapeMarkdown(component.Version),
			escapeMarkdown(component.Scope), escapeMarkdown(component.License), component.Source,
			strings.Join(refs, "，"))
	}
	output.WriteString("\n## License and notice texts\n\n")
	hashes := make([]string, 0, len(evidenceByHash))
	for hash := range evidenceByHash {
		hashes = append(hashes, hash)
	}
	sort.Strings(hashes)
	for _, hash := range hashes {
		record := evidenceByHash[hash]
		sort.Strings(record.Licenses)
		fmt.Fprintf(&output, "<a id=\"license-text-%s\"></a>\n\n### `%s`\n\n", hash, hash)
		fmt.Fprintf(&output, "- Kind: `%s`\n- Source path: `%s`\n- Used by: %s\n\n", record.Evidence.Kind, record.Evidence.Path, strings.Join(record.Licenses, "，"))
		output.WriteString("```text\n")
		output.WriteString(strings.TrimRight(record.Evidence.Text, "\n"))
		output.WriteString("\n```\n\n")
	}
	return output.Bytes(), nil
}

func RenderCycloneDX(components []Component) ([]byte, error) {
	ordered := append([]Component(nil), components...)
	sortComponents(ordered)
	type licenseChoice struct {
		Expression string `json:"expression,omitempty"`
		License    *struct {
			Name string `json:"name"`
		} `json:"license,omitempty"`
	}
	type property struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	type hash struct {
		Algorithm string `json:"alg"`
		Content   string `json:"content"`
	}
	type reference struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	}
	type bomComponent struct {
		Type               string          `json:"type"`
		BOMRef             string          `json:"bom-ref"`
		Group              string          `json:"group,omitempty"`
		Name               string          `json:"name"`
		Version            string          `json:"version,omitempty"`
		Scope              string          `json:"scope,omitempty"`
		PURL               string          `json:"purl,omitempty"`
		Licenses           []licenseChoice `json:"licenses"`
		Hashes             []hash          `json:"hashes,omitempty"`
		ExternalReferences []reference     `json:"externalReferences,omitempty"`
		Properties         []property      `json:"properties,omitempty"`
	}
	bomComponents := make([]bomComponent, 0, len(ordered))
	for _, component := range ordered {
		entry := bomComponent{
			Type:    cycloneDXType(component.Type),
			BOMRef:  component.ID,
			Name:    component.Name,
			Version: component.Version,
			Scope:   cycloneDXScope(component.Scope),
			PURL:    componentPURL(component),
			ExternalReferences: []reference{{
				Type: "website", URL: component.Source,
			}},
		}
		if component.License == "NOASSERTION" || strings.TrimSpace(component.License) == "" {
			entry.Licenses = []licenseChoice{{License: &struct {
				Name string `json:"name"`
			}{Name: "NOASSERTION"}}}
		} else {
			entry.Licenses = []licenseChoice{{Expression: component.License}}
		}
		hashNames := make([]string, 0, len(component.Hashes))
		for name := range component.Hashes {
			if name == "SHA-256" || name == "SHA-512" || name == "SHA-1" {
				hashNames = append(hashNames, name)
			}
		}
		sort.Strings(hashNames)
		for _, name := range hashNames {
			entry.Hashes = append(entry.Hashes, hash{Algorithm: name, Content: component.Hashes[name]})
		}
		propertyNames := make([]string, 0, len(component.Properties)+len(component.Hashes))
		for name := range component.Properties {
			propertyNames = append(propertyNames, name)
		}
		for name := range component.Hashes {
			if name != "SHA-256" && name != "SHA-512" && name != "SHA-1" {
				propertyNames = append(propertyNames, "integrity:"+name)
			}
		}
		sort.Strings(propertyNames)
		for _, name := range propertyNames {
			value := component.Properties[name]
			if strings.HasPrefix(name, "integrity:") {
				value = component.Hashes[strings.TrimPrefix(name, "integrity:")]
			}
			entry.Properties = append(entry.Properties, property{Name: "karte:" + name, Value: value})
		}
		bomComponents = append(bomComponents, entry)
	}
	canonical, err := json.Marshal(bomComponents)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(canonical)
	serialBytes := append([]byte(nil), digest[:16]...)
	serialBytes[6] = (serialBytes[6] & 0x0f) | 0x50
	serialBytes[8] = (serialBytes[8] & 0x3f) | 0x80
	serial := fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", serialBytes[0:4], serialBytes[4:6], serialBytes[6:8], serialBytes[8:10], serialBytes[10:16])
	bom := struct {
		BOMFormat    string         `json:"bomFormat"`
		SpecVersion  string         `json:"specVersion"`
		SerialNumber string         `json:"serialNumber"`
		Version      int            `json:"version"`
		Metadata     map[string]any `json:"metadata"`
		Components   []bomComponent `json:"components"`
	}{
		BOMFormat: "CycloneDX", SpecVersion: "1.5", SerialNumber: serial, Version: 1,
		Metadata: map[string]any{
			"component": map[string]any{
				"type": "application", "bom-ref": "application:karte", "name": "Karte",
				"licenses": []any{map[string]any{"license": map[string]string{"id": "MIT"}}},
			},
			"tools": map[string]any{"components": []any{map[string]any{
				"type": "application", "name": "karte-licensegate", "version": "1",
			}}},
		},
		Components: bomComponents,
	}
	return MarshalDeterministicJSON(bom)
}

func RenderComponentInventory(components []Component) ([]byte, error) {
	ordered := append([]Component(nil), components...)
	sortComponents(ordered)
	return MarshalDeterministicJSON(struct {
		SchemaVersion int         `json:"schemaVersion"`
		Components    []Component `json:"components"`
	}{SchemaVersion: SchemaVersion, Components: ordered})
}

func escapeMarkdown(value string) string {
	return strings.ReplaceAll(value, "|", "\\|")
}

func cycloneDXType(componentType string) string {
	switch componentType {
	case "asset", "model":
		return "data"
	default:
		return "library"
	}
}

func cycloneDXScope(scope string) string {
	switch scope {
	case "build":
		return "excluded"
	case "optional":
		return "optional"
	default:
		return "required"
	}
}

func componentPURL(component Component) string {
	switch component.Type {
	case "go":
		return "pkg:golang/" + strings.TrimPrefix(url.PathEscape(component.Name), "%2F") + "@" + url.PathEscape(component.Version)
	case "npm":
		return "pkg:npm/" + url.PathEscape(component.Name) + "@" + url.PathEscape(component.Version)
	default:
		return ""
	}
}

func sha256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
