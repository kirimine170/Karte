package contextcore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPolicySecurityMatrixAppliesCapabilityProjectTagSensitivityAndProvenance(t *testing.T) {
	policy := Policy{
		ProtocolVersion: ProtocolVersion,
		MasterProjects:  []string{"master"},
		Actors: map[string]ActorPolicy{
			"ephy": {
				SensitivityCeiling: "internal", Projects: []string{"*"}, ProvenanceTypes: []string{"*"},
				Capabilities: []Capability{CapabilitySearch, CapabilityRead, CapabilityPropose},
			},
			"human": {
				SensitivityCeiling: "restricted", Projects: []string{"*"}, ProvenanceTypes: []string{"*"},
				Capabilities: []Capability{CapabilitySearch, CapabilityRead, CapabilityReview, CapabilityExport},
			},
			"analyst": {
				SensitivityCeiling: "confidential", Projects: []string{"ephy", "master"},
				AllowedTags: []string{"person:alice"}, DeniedTags: []string{"do-not-share"},
				ProvenanceTypes: []string{"canonical"}, Capabilities: []Capability{CapabilitySearch, CapabilityRead},
			},
			"trainer": {
				SensitivityCeiling: "internal", Projects: []string{"ephy"}, ProvenanceTypes: []string{"canonical"},
				Capabilities: []Capability{CapabilityLearn},
			},
			"no-provenance": {
				SensitivityCeiling: "internal", Projects: []string{"ephy"}, ProvenanceTypes: []string{},
				Capabilities: []Capability{CapabilityRead},
			},
		},
	}
	if err := policy.Validate(); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name       string
		actor      Actor
		capability Capability
		resource   Resource
		allowed    bool
	}{
		{"ephy internal read", Actor{Type: "ephy", ID: "ephy"}, CapabilityRead, matrixResource("ephy", "internal", []string{"person:alice"}, "canonical"), true},
		{"ephy confidential read", Actor{Type: "ephy", ID: "ephy"}, CapabilityRead, matrixResource("ephy", "confidential", []string{"person:alice"}, "canonical"), false},
		{"ephy learning default deny", Actor{Type: "ephy", ID: "ephy"}, CapabilityLearn, matrixResource("ephy", "public", []string{"person:alice"}, "canonical"), false},
		{"human restricted export", Actor{Type: "human", ID: "local-human"}, CapabilityExport, matrixResource("private", "restricted", []string{"person:alice"}, "canonical"), true},
		{"human learning default deny", Actor{Type: "human", ID: "local-human"}, CapabilityLearn, matrixResource("ephy", "public", []string{"person:alice"}, "canonical"), false},
		{"analyst matching tag", Actor{Type: "tool", ID: "analyst"}, CapabilityRead, matrixResource("ephy", "confidential", []string{"person:alice"}, "canonical"), true},
		{"analyst other person", Actor{Type: "tool", ID: "analyst"}, CapabilityRead, matrixResource("ephy", "internal", []string{"person:bob"}, "canonical"), false},
		{"analyst denied tag wins", Actor{Type: "tool", ID: "analyst"}, CapabilityRead, matrixResource("ephy", "internal", []string{"person:alice", "do-not-share"}, "canonical"), false},
		{"analyst other project", Actor{Type: "tool", ID: "analyst"}, CapabilityRead, matrixResource("private", "internal", []string{"person:alice"}, "canonical"), false},
		{"analyst untrusted provenance", Actor{Type: "tool", ID: "analyst"}, CapabilityRead, matrixResource("ephy", "internal", []string{"person:alice"}, "web"), false},
		{"analyst master project", Actor{Type: "tool", ID: "analyst"}, CapabilityRead, matrixResource("master", "internal", []string{"person:alice"}, "canonical"), true},
		{"trainer internal learning", Actor{Type: "tool", ID: "trainer"}, CapabilityLearn, matrixResource("ephy", "internal", nil, "canonical"), true},
		{"trainer confidential learning", Actor{Type: "tool", ID: "trainer"}, CapabilityLearn, matrixResource("ephy", "confidential", nil, "canonical"), false},
		{"trainer export denied", Actor{Type: "tool", ID: "trainer"}, CapabilityExport, matrixResource("ephy", "internal", nil, "canonical"), false},
		{"explicit empty provenance denies all", Actor{Type: "tool", ID: "no-provenance"}, CapabilityRead, matrixResource("ephy", "internal", nil, "canonical"), false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			decision, err := policy.Authorize(test.actor, test.capability, test.resource)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Allowed != test.allowed {
				t.Fatalf("unexpected decision: %#v", decision)
			}
		})
	}
	if !policy.IsMasterProject("MASTER") || policy.IsMasterProject("ephy") {
		t.Fatal("master project classification was not normalized")
	}
}

func TestPolicyRejectsContradictoryTagsAndUnknownCapabilities(t *testing.T) {
	base := Policy{
		ProtocolVersion: ProtocolVersion,
		Actors: map[string]ActorPolicy{
			"tool": {SensitivityCeiling: "internal", Projects: []string{"ephy"}, AllowedTags: []string{"person:alice"}, DeniedTags: []string{"PERSON:ALICE"}},
		},
	}
	if err := base.Validate(); err == nil {
		t.Fatal("contradictory tag rules were accepted")
	}
	base.Actors["tool"] = ActorPolicy{SensitivityCeiling: "internal", Projects: []string{"ephy"}, Capabilities: []Capability{"upload"}}
	if err := base.Validate(); err == nil {
		t.Fatal("unknown capability was accepted")
	}
}

func TestPolicyRejectsInvalidResourceTagsAndProvenance(t *testing.T) {
	policy := DefaultPolicy()
	resource := matrixResource("ephy", "internal", []string{"private\nname"}, "canonical")
	if _, err := policy.Authorize(Actor{Type: "ephy", ID: "ephy"}, CapabilityRead, resource); err == nil {
		t.Fatal("invalid resource tag was accepted")
	}
	resource = matrixResource("ephy", "internal", nil, "private source name")
	if _, err := policy.Authorize(Actor{Type: "ephy", ID: "ephy"}, CapabilityRead, resource); err == nil {
		t.Fatal("invalid resource provenance was accepted")
	}
}

func TestAuditRejectsContentBearingMetadataFields(t *testing.T) {
	if _, err := NewAuditEvent("correlation", Actor{Type: "person:alice", ID: "actor"}, "read", "ok", 0, "", ""); err == nil {
		t.Fatal("content-bearing actor type was accepted")
	}
	if _, err := NewAuditEvent("correlation", Actor{Type: "ephy", ID: "actor"}, "read", "ok", 0, "contains private name", ""); err == nil {
		t.Fatal("content-bearing error code was accepted")
	}
}

func TestAuditEventIdentityIncludesResultCount(t *testing.T) {
	first, err := NewAuditEvent("request", Actor{Type: "ephy", ID: "ephy"}, "search", "ok", 1, "", "2026-09-02T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewAuditEvent("request", Actor{Type: "ephy", ID: "ephy"}, "search", "ok", 2, "", "2026-09-02T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if first.EventID == second.EventID {
		t.Fatal("different result counts produced the same audit identity")
	}

	dataRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataRoot, "content"), 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteAudit(first); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteAudit(first); err != nil {
		t.Fatalf("identical audit replay was not idempotent: %v", err)
	}
}

func TestAuditStoreRejectsContentBearingEventFields(t *testing.T) {
	dataRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataRoot, "content"), 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	event, err := NewAuditEvent("request", Actor{Type: "ephy", ID: "ephy"}, "read", "ok", 1, "", "2026-09-02T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	event.ErrorCode = "private person name"
	if err := store.WriteAudit(event); err == nil {
		t.Fatal("content-bearing audit event was written directly")
	}
}

func matrixResource(project, sensitivity string, tags []string, provenance string) Resource {
	return Resource{Project: project, Kind: "note", Sensitivity: sensitivity, Tags: tags, ProvenanceTypes: []string{provenance}}
}
