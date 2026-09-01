package contextcore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestServiceSearchAndReadApplyScope(t *testing.T) {
	dataDir := newContextFixture(t)
	writeContextDocument(t, dataDir, "content/projects/ephy/decision/2026-09/runtime.md", `---
title: "Runtime boundary"
tags:
  - architecture
  - ephy
doc_id: "doc:runtime-boundary"
project: ephy
kind: decision
sensitivity: internal
---
Karte owns durable personal context. Ephy owns conversations and execution.
`)
	writeContextDocument(t, dataDir, "content/projects/ephy/note/2026-09/private.md", `---
title: "Restricted note"
tags: private
doc_id: "doc:restricted-note"
project: ephy
kind: note
sensitivity: restricted
---
This body must not appear in the default Ephy scope.
`)

	service, err := NewService(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	request := Request{
		ProtocolVersion: ProtocolVersion, RequestID: "search-001", Operation: "search",
		Actor: Actor{Type: "ephy", ID: "ephy"},
		Scope: Scope{Projects: []string{"ephy"}, Tags: []string{"architecture"}, SensitivityCeiling: "restricted"},
		Query: &SearchQuery{Text: "personal context", TopK: 5}, CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	results, diagnostics, status, err := service.Search(request, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if status != "ok" || len(diagnostics) != 0 || len(results) != 1 {
		t.Fatalf("unexpected search response: status=%s results=%#v diagnostics=%#v", status, results, diagnostics)
	}
	if results[0].DocID != "doc:runtime-boundary" || results[0].Sensitivity != "internal" || results[0].SHA256 == "" {
		t.Fatalf("search result lost canonical metadata: %#v", results[0])
	}

	docID := "doc:runtime-boundary"
	read := request
	read.RequestID, read.Operation, read.Query, read.DocID = "read-001", "read", nil, &docID
	document, _, status, err := service.Read(read, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if status != "ok" || document == nil || !strings.Contains(document.Body, "Karte owns") {
		t.Fatalf("unexpected read response: status=%s document=%#v", status, document)
	}

	restrictedID := "doc:restricted-note"
	read.RequestID, read.DocID, read.Scope.Tags = "read-002", &restrictedID, []string{}
	document, _, status, err = service.Read(read, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if status != "denied" || document != nil {
		t.Fatalf("default Ephy policy exposed restricted content: status=%s document=%#v", status, document)
	}
}

func TestServiceExcludesDuplicateDocIDAndSymlink(t *testing.T) {
	dataDir := newContextFixture(t)
	for _, name := range []string{"one.md", "two.md"} {
		writeContextDocument(t, dataDir, filepath.Join("content/projects/ephy/note/2026-09", name), `---
title: "Duplicate"
tags: duplicate
doc_id: "doc:duplicate"
project: ephy
kind: note
---
duplicate body
`)
	}
	if runtime.GOOS != "windows" {
		outside := filepath.Join(t.TempDir(), "outside.md")
		if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(dataDir, "content", "linked.md")); err != nil {
			t.Fatal(err)
		}
	}
	service, err := NewService(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	request := Request{
		ProtocolVersion: ProtocolVersion, RequestID: "search-duplicate", Operation: "search",
		Actor: Actor{Type: "ephy", ID: "ephy"}, Scope: Scope{Projects: []string{"*"}, SensitivityCeiling: "internal"},
		Query: &SearchQuery{Text: "duplicate", TopK: 5}, CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	results, diagnostics, _, err := service.Search(request, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("duplicate doc_id reached results: %#v", results)
	}
	if !hasDiagnostic(diagnostics, "duplicate_doc_id") {
		t.Fatalf("duplicate diagnostic missing: %#v", diagnostics)
	}
	if runtime.GOOS != "windows" && !hasDiagnostic(diagnostics, "symlink_ignored") {
		t.Fatalf("symlink diagnostic missing: %#v", diagnostics)
	}
}

func TestProcessorWritesAtomicResponseAndRecoversIdempotently(t *testing.T) {
	dataDir := newContextFixture(t)
	writeContextDocument(t, dataDir, "content/projects/ephy/note/2026-09/context.md", `---
title: "Context"
tags: ephy
doc_id: "doc:context"
project: ephy
kind: note
---
personal context fixture
`)
	processor, err := NewProcessor(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	request := Request{
		ProtocolVersion: ProtocolVersion, RequestID: "processor-001", Operation: "search",
		Actor: Actor{Type: "ephy", ID: "ephy"}, Scope: Scope{Projects: []string{"ephy"}, SensitivityCeiling: "internal"},
		Query: &SearchQuery{Text: "personal context", TopK: 3}, CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(dataDir, ".mdsys", "context", "v1", "requests", request.RequestID+".json")
	if err := os.WriteFile(requestPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	summary, err := processor.ProcessPending(10)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Processed != 1 || summary.Failed != 0 {
		t.Fatalf("unexpected process summary: %#v", summary)
	}
	response, err := processor.store.ReadResponse(request.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	if response == nil || response.Status != "ok" || len(response.Results) != 1 || response.RequestSHA256 == "" {
		t.Fatalf("unexpected response: %#v", response)
	}
	processedPath := filepath.Join(dataDir, ".mdsys", "context", "v1", "processed", request.RequestID+".json")
	processedData, err := os.ReadFile(processedPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(requestPath, processedData, 0o600); err != nil {
		t.Fatal(err)
	}
	summary, err = processor.ProcessPending(10)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Processed != 1 || summary.Failed != 0 {
		t.Fatalf("idempotent recovery failed: %#v", summary)
	}
}

func TestPolicyRejectsTrailingJSONAndIntersectsProjectScope(t *testing.T) {
	dataDir := newContextFixture(t)
	policyDir := filepath.Join(dataDir, ".mdsys", "context", "v1")
	if err := os.MkdirAll(policyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	policyPath := filepath.Join(policyDir, "policy.json")
	if err := os.WriteFile(policyPath, []byte(`{"protocol_version":"1.0","actors":{"ephy":{"sensitivity_ceiling":"internal","projects":["ephy"]}}} {}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPolicy(dataDir); err == nil || !strings.Contains(err.Error(), "trailing JSON") {
		t.Fatalf("trailing policy JSON was accepted: %v", err)
	}

	policy := Policy{
		ProtocolVersion: ProtocolVersion,
		Actors: map[string]ActorPolicy{
			"ephy": {SensitivityCeiling: "internal", Projects: []string{"ephy"}},
		},
	}
	effective, err := policy.Resolve(
		Actor{Type: "ephy", ID: "ephy"},
		Scope{Projects: []string{"ephy", "private-project"}, SensitivityCeiling: "restricted"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if effective.SensitivityCeiling != "internal" || !effective.Projects["ephy"] || effective.Projects["private-project"] {
		t.Fatalf("policy did not intersect project and sensitivity scopes: %#v", effective)
	}
}

func newContextFixture(t *testing.T) string {
	t.Helper()
	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "content"), 0o700); err != nil {
		t.Fatal(err)
	}
	return dataDir
}

func writeContextDocument(t *testing.T, dataDir, relativePath, content string) {
	t.Helper()
	path := filepath.Join(dataDir, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func hasDiagnostic(diagnostics []Diagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code && diagnostic.Count > 0 {
			return true
		}
	}
	return false
}
