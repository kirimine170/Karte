package compliance

import (
	"strings"
	"testing"
	"time"
)

func TestValidateComponentLicensesRejectsCopyleftAndUnknownFailClosed(t *testing.T) {
	policy := testPolicy()
	now := time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)
	components := []Component{
		testComponent("gpl", "GPL-3.0-only"),
		testComponent("agpl", "AGPL-3.0-or-later"),
		testComponent("missing", ""),
		testComponent("unknown", "LicenseRef-unreviewed"),
	}

	err := ValidateComponentLicenses(policy, ExceptionRegistry{SchemaVersion: SchemaVersion}, components, now)
	if err == nil {
		t.Fatal("expected license policy violations")
	}
	for _, want := range []string{
		"agpl uses denied license AGPL-3.0-or-later",
		"gpl uses denied license GPL-3.0-only",
		"missing has missing or unknown license metadata",
		"unknown uses unapproved or unknown license LicenseRef-unreviewed",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestValidateComponentLicensesRequiresCompleteUnexpiredLGPLException(t *testing.T) {
	policy := testPolicy()
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	component := testComponent("native:example", "LGPL-2.1-only")
	component.DistributionPath = "Frameworks/libexample.dylib"
	component.Type = "native"
	component = Component{
		ID:               component.ID,
		Type:             component.Type,
		Name:             component.Name,
		Scope:            component.Scope,
		Source:           component.Source,
		License:          "LGPL-2.1-only",
		DistributionPath: "Frameworks/libexample.dylib",
	}

	if err := ValidateComponentLicenses(policy, ExceptionRegistry{SchemaVersion: SchemaVersion}, []Component{component}, now); err == nil {
		t.Fatal("expected missing exception to fail")
	}
	registry := ExceptionRegistry{
		SchemaVersion: SchemaVersion,
		Exceptions: []LicenseException{{
			Component:  component.ID,
			Path:       component.DistributionPath,
			License:    component.License,
			Reason:     "dynamically linked and replacement mechanism documented",
			ApprovedBy: "release-owner@example.test",
			ExpiresAt:  "2026-12-31",
		}},
	}
	if err := ValidateComponentLicenses(policy, registry, []Component{component}, now); err != nil {
		t.Fatalf("expected active complete exception to pass: %v", err)
	}

	registry.Exceptions[0].ExpiresAt = "2026-08-23"
	if err := ValidateComponentLicenses(policy, registry, []Component{component}, now); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected same-day expiry to fail closed，got %v", err)
	}
	registry.Exceptions[0].ExpiresAt = "2026-12-31"
	registry.Exceptions[0].ApprovedBy = ""
	if err := ValidateComponentLicenses(policy, registry, []Component{component}, now); err == nil || !strings.Contains(err.Error(), "approvedBy") {
		t.Fatalf("expected missing approver to fail closed，got %v", err)
	}
}

func TestValidateComponentLicensesAcceptsAllowedCompoundExpression(t *testing.T) {
	components := []Component{testComponent("npm:example", "(MPL-2.0 OR Apache-2.0)")}
	if err := ValidateComponentLicenses(testPolicy(), ExceptionRegistry{SchemaVersion: SchemaVersion}, components, time.Now()); err != nil {
		t.Fatalf("expected allowed SPDX expression to pass: %v", err)
	}
}

func TestValidateComponentLicensesRejectsMalformedExpressions(t *testing.T) {
	for _, expression := range []string{
		"MIT OR", "OR MIT", "MIT AND", "MIT Apache-2.0", "(MIT OR Apache-2.0", "MIT garbage",
		"MIT WITH InventedException", "MIT WITH GCC-exception-3.1", "GPL-3.0-or-later WITH",
	} {
		t.Run(expression, func(t *testing.T) {
			component := testComponent("npm:malformed", expression)
			err := ValidateComponentLicenses(testPolicy(), ExceptionRegistry{SchemaVersion: SchemaVersion}, []Component{component}, time.Now())
			if err == nil {
				t.Fatalf("expected %q to fail", expression)
			}
		})
	}
}

func TestValidateComponentLicensesPreservesKnownSPDXExceptionTerm(t *testing.T) {
	component := testComponent("native:gcc", "GPL-3.0-or-later WITH GCC-exception-3.1")
	err := ValidateComponentLicenses(testPolicy(), ExceptionRegistry{SchemaVersion: SchemaVersion}, []Component{component}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "uses denied license GPL-3.0-or-later WITH GCC-exception-3.1") {
		t.Fatalf("expected the complete WITH term to remain policy-visible，got %v", err)
	}
}

func TestValidateComponentLicensesRejectsUnusedException(t *testing.T) {
	component := testComponent("npm:mit", "MIT")
	component.DistributionPath = "assets/mit.js"
	registry := ExceptionRegistry{SchemaVersion: SchemaVersion, Exceptions: []LicenseException{{
		Component: component.ID, Path: component.DistributionPath, License: "LGPL-2.1-only",
		Reason: "stale", ApprovedBy: "owner@example.test", ExpiresAt: "2099-01-01",
	}}}
	err := ValidateComponentLicenses(testPolicy(), registry, []Component{component}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "stale or unused") {
		t.Fatalf("expected stale exception failure，got %v", err)
	}
}

func TestValidateComponentLicensesRejectsIncompleteInventorySchema(t *testing.T) {
	base := testComponent("asset:test", "MIT")
	base.Type = "asset"
	base.DistributionPath = "assets/test.bin"
	base.Hashes = map[string]string{"SHA-256": strings.Repeat("a", 64)}
	for _, test := range []struct {
		name   string
		mutate func(*Component)
		want   string
	}{
		{name: "type", mutate: func(component *Component) { component.Type = "mystery" }, want: "unsupported type"},
		{name: "name", mutate: func(component *Component) { component.Name = "" }, want: "missing name"},
		{name: "scope", mutate: func(component *Component) { component.Scope = "maybe" }, want: "unsupported scope"},
		{name: "source", mutate: func(component *Component) { component.Source = "http://example.test" }, want: "non-https"},
		{name: "distribution", mutate: func(component *Component) { component.DistributionPath = "../escape" }, want: "non-portable"},
		{name: "hash", mutate: func(component *Component) { component.Hashes = nil }, want: "distributed SHA-256"},
	} {
		t.Run(test.name, func(t *testing.T) {
			component := base
			test.mutate(&component)
			err := ValidateComponentLicenses(testPolicy(), ExceptionRegistry{SchemaVersion: SchemaVersion}, []Component{component}, time.Now())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q，got %v", test.want, err)
			}
		})
	}
}

func testComponent(id, license string) Component {
	return Component{
		ID: id, Type: "npm", Name: id, License: license, Scope: "runtime",
		Source: "https://example.test/" + id,
	}
}

func testPolicy() Policy {
	return Policy{
		SchemaVersion:                    SchemaVersion,
		AllowedLicenses:                  []string{"Apache-2.0", "MIT", "MPL-2.0"},
		DeniedLicenseFamilies:            []string{"AGPL", "GPL"},
		ExceptionRequiredLicenseFamilies: []string{"LGPL"},
	}
}
