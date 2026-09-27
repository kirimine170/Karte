package mcp

// Regression tests for post-startup policy and scope-marker re-evaluation.
//
// The MCP server used to keep the policy and scope marker loaded at
// NewServer time and authorize every later call against that snapshot. These
// tests mutate the synthetic on-disk grants after the server starts and
// require every subsequent search/read to observe the new state: narrowed or
// empty project lists and lowered ceilings deny access, while missing,
// corrupted, or grant-less policy/marker files fail closed instead of
// falling back to the startup snapshot or to the shared DefaultPolicy.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"karte/internal/contextcore"
)

// reloadBaseline starts a server on a dedicated root and proves that the
// codex-scoped fixture document is reachable before any mutation.
func reloadBaseline(t *testing.T) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	buildDedicatedRoot(t, root)
	server, err := NewServer(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := server.search(json.RawMessage(`{"query":"planning"}`))
	if err != nil {
		t.Fatalf("baseline search failed: %v", err)
	}
	out := got.(SearchOutput)
	if len(out.Results) != 1 || out.Results[0].DocID != "doc:alpha" {
		t.Fatalf("baseline search did not return doc:alpha: %+v", out)
	}
	doc, err := server.read(json.RawMessage(`{"doc_id":"doc:alpha"}`))
	if err != nil || doc.(ReadOutput).Document == nil {
		t.Fatalf("baseline read did not return doc:alpha: %v %v", doc, err)
	}
	return server, root
}

func rewritePolicy(t *testing.T, root string, mutate func(*contextcore.Policy)) {
	t.Helper()
	policy, err := contextcore.LoadPolicy(root)
	if err != nil {
		t.Fatal(err)
	}
	mutate(&policy)
	writePolicyFile(t, root, policy)
}

func reloadPolicyPath(root string) string {
	return filepath.Join(root, ".mdsys", "context", "v1", "policy.json")
}

func reloadScopePath(root string) string {
	return filepath.Join(root, ".mdsys", "context", "v1", "mcp-scope.json")
}

// TestMCPProjectRevocationReflected verifies that narrowing the actor's
// project list after startup removes the revoked project from both search
// results and read access on the same running server.
func TestMCPProjectRevocationReflected(t *testing.T) {
	server, root := reloadBaseline(t)

	rewritePolicy(t, root, func(p *contextcore.Policy) {
		actor := p.Actors["codex"]
		actor.Projects = []string{"local"}
		p.Actors["codex"] = actor
	})

	got, err := server.search(json.RawMessage(`{"query":"planning"}`))
	if err != nil {
		t.Fatalf("search after project revocation: %v", err)
	}
	out := got.(SearchOutput)
	if out.Status != "ok" || len(out.Results) != 0 {
		t.Fatalf("search after project revocation returned %d results: %+v", len(out.Results), out)
	}

	doc, err := server.read(json.RawMessage(`{"doc_id":"doc:alpha"}`))
	if err != nil {
		t.Fatalf("read after project revocation: %v", err)
	}
	read := doc.(ReadOutput)
	if read.Document != nil {
		t.Fatalf("read returned a revoked document: %+v", read)
	}

	// The policy model does not admit an empty project list, so "revoking
	// every project" is an invalid policy on disk. The next call must fail
	// closed on the invalid policy rather than keep serving from the
	// startup snapshot.
	rewritePolicy(t, root, func(p *contextcore.Policy) {
		actor := p.Actors["codex"]
		actor.Projects = []string{}
		p.Actors["codex"] = actor
	})

	if _, err := server.search(json.RawMessage(`{"query":"gamma"}`)); err == nil {
		t.Fatal("search succeeded against a policy with an empty project list")
	}
	if _, err := server.read(json.RawMessage(`{"doc_id":"doc:gamma"}`)); err == nil {
		t.Fatal("read succeeded against a policy with an empty project list")
	}
}

// TestMCPCapabilityRevocationReflected verifies that removing the search
// grant from the actor policy after startup fails closed for both
// operations, because the dedicated marker still requires both explicit
// grants to be backed by the current policy.
func TestMCPCapabilityRevocationReflected(t *testing.T) {
	server, root := reloadBaseline(t)

	rewritePolicy(t, root, func(p *contextcore.Policy) {
		actor := p.Actors["codex"]
		actor.Capabilities = []contextcore.Capability{contextcore.CapabilityRead}
		p.Actors["codex"] = actor
	})

	if _, err := server.search(json.RawMessage(`{"query":"planning"}`)); err == nil {
		t.Fatal("search succeeded after the search grant was revoked on disk")
	}
	if _, err := server.read(json.RawMessage(`{"doc_id":"doc:alpha"}`)); err == nil {
		t.Fatal("read succeeded after the marker lost its explicit search/read grant")
	}
}

// TestMCPActorRemovalReflected verifies that deleting the actor from
// policy.json after startup fails closed for subsequent calls.
func TestMCPActorRemovalReflected(t *testing.T) {
	server, root := reloadBaseline(t)

	rewritePolicy(t, root, func(p *contextcore.Policy) {
		delete(p.Actors, "codex")
	})

	if _, err := server.search(json.RawMessage(`{"query":"planning"}`)); err == nil {
		t.Fatal("search succeeded after its actor was removed from policy.json")
	}
	if _, err := server.read(json.RawMessage(`{"doc_id":"doc:alpha"}`)); err == nil {
		t.Fatal("read succeeded after its actor was removed from policy.json")
	}
}

// TestMCPSensitivityCeilingLoweringReflected verifies that lowering the
// ceiling after startup stops internal documents from being returned.
func TestMCPSensitivityCeilingLoweringReflected(t *testing.T) {
	server, root := reloadBaseline(t)

	rewritePolicy(t, root, func(p *contextcore.Policy) {
		actor := p.Actors["codex"]
		actor.SensitivityCeiling = "public"
		p.Actors["codex"] = actor
	})

	got, err := server.search(json.RawMessage(`{"query":"planning"}`))
	if err != nil {
		t.Fatalf("search after ceiling lowering: %v", err)
	}
	if out := got.(SearchOutput); out.Status != "ok" || len(out.Results) != 0 {
		t.Fatalf("search returned documents above the lowered ceiling: %+v", out)
	}

	doc, err := server.read(json.RawMessage(`{"doc_id":"doc:alpha"}`))
	if err != nil {
		t.Fatalf("read after ceiling lowering: %v", err)
	}
	if read := doc.(ReadOutput); read.Document != nil {
		t.Fatalf("read returned a document above the lowered ceiling: %+v", read)
	}
}

// TestMCPMissingPolicyFailsClosed verifies that deleting policy.json after
// startup stops the server from serving with the stale startup policy or
// with the shared DefaultPolicy.
func TestMCPMissingPolicyFailsClosed(t *testing.T) {
	server, root := reloadBaseline(t)

	if err := os.Remove(reloadPolicyPath(root)); err != nil {
		t.Fatal(err)
	}

	if _, err := server.search(json.RawMessage(`{"query":"planning"}`)); err == nil {
		t.Fatal("search succeeded after policy.json was removed from disk")
	}
	if _, err := server.read(json.RawMessage(`{"doc_id":"doc:alpha"}`)); err == nil {
		t.Fatal("read succeeded after policy.json was removed from disk")
	}
}

// TestMCPInvalidPolicyFailsClosed verifies that corrupting policy.json after
// startup fails closed instead of continuing with the startup snapshot.
func TestMCPInvalidPolicyFailsClosed(t *testing.T) {
	server, root := reloadBaseline(t)

	if err := os.WriteFile(reloadPolicyPath(root), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := server.search(json.RawMessage(`{"query":"planning"}`)); err == nil {
		t.Fatal("search succeeded after policy.json was corrupted on disk")
	}
	if _, err := server.read(json.RawMessage(`{"doc_id":"doc:alpha"}`)); err == nil {
		t.Fatal("read succeeded after policy.json was corrupted on disk")
	}
}

// TestMCPRemovedScopeMarkerFailsClosed verifies that deleting the dedicated
// scope marker after startup fails closed for subsequent calls.
func TestMCPRemovedScopeMarkerFailsClosed(t *testing.T) {
	server, root := reloadBaseline(t)

	if err := os.Remove(reloadScopePath(root)); err != nil {
		t.Fatal(err)
	}

	if _, err := server.search(json.RawMessage(`{"query":"planning"}`)); err == nil {
		t.Fatal("search succeeded after mcp-scope.json was removed from disk")
	}
	if _, err := server.read(json.RawMessage(`{"doc_id":"doc:alpha"}`)); err == nil {
		t.Fatal("read succeeded after mcp-scope.json was removed from disk")
	}
}

// TestMCPCorruptedScopeMarkerFailsClosed verifies that a marker that no
// longer carries the explicit [search read] grant fails closed.
func TestMCPCorruptedScopeMarkerFailsClosed(t *testing.T) {
	server, root := reloadBaseline(t)

	if err := os.WriteFile(reloadScopePath(root), []byte(`{"protocol_version":"1.0","actor":"codex"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := server.search(json.RawMessage(`{"query":"planning"}`)); err == nil {
		t.Fatal("search succeeded after mcp-scope.json lost its explicit grant")
	}
	if _, err := server.read(json.RawMessage(`{"doc_id":"doc:alpha"}`)); err == nil {
		t.Fatal("read succeeded after mcp-scope.json lost its explicit grant")
	}
}

// TestMCPStalePolicyErrorIsSanitized verifies that a fail-closed reload
// error reaches the client as the stable sanitized tool error: no absolute
// host path and no document title or body may appear in the frame.
func TestMCPStalePolicyErrorIsSanitized(t *testing.T) {
	root := t.TempDir()
	buildDedicatedRoot(t, root)
	server, err := NewServer(root)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(reloadPolicyPath(root)); err != nil {
		t.Fatal(err)
	}

	input := mcpLine(t, "1", "initialize", map[string]any{
		"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "audit", "version": "0"},
	}) +
		mcpLine(t, "", "notifications/initialized", nil) +
		mcpLine(t, "2", "tools/call", map[string]any{
			"name": "karte_search", "arguments": map[string]any{"query": "planning"},
		})

	var out bytes.Buffer
	if err := server.Run(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "processing failed") {
		t.Fatalf("expected sanitized tool error, got frames: %s", text)
	}
	if strings.Contains(text, root) {
		t.Fatalf("absolute data root path leaked into the wire: %s", text)
	}
	for _, leaked := range []string{"Alpha plan", "Alpha planning body", "doc:alpha"} {
		if strings.Contains(text, leaked) {
			t.Fatalf("denied document content leaked into the wire: %q in %s", leaked, text)
		}
	}
	var frame struct {
		ID     any             `json:"id"`
		Result json.RawMessage `json:"result"`
	}
	for _, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, `"id":"2"`) {
			continue
		}
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Fatal(err)
		}
		var result struct {
			IsError bool `json:"isError"`
		}
		if err := json.Unmarshal(frame.Result, &result); err != nil {
			t.Fatalf("id 2 frame is not a tool result: %s", line)
		}
		if !result.IsError {
			t.Fatalf("fail-closed reload must produce isError tool content: %s", line)
		}
	}
}
