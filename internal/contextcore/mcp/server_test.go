package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"karte/internal/contextcore"
)

// ---------- fixtures ----------

func buildDedicatedRoot(t *testing.T, root string) {
	t.Helper()
	writeDoc(t, root, "content/projects/codex/notes/alpha.md", `---
title: "Alpha plan"
tags: codex, planning
doc_id: "doc:alpha"
project: codex
kind: note
sensitivity: internal
---
Alpha planning body with the word codex.
`)
	writeDoc(t, root, "content/projects/codex/notes/beta.md", `---
title: "Beta report"
tags: codex
doc_id: "doc:beta"
project: codex
kind: note
sensitivity: confidential
---
Beta confidential body.
`)
	writeDoc(t, root, "content/projects/local/notes/gamma.md", `---
title: "Gamma local"
tags: local
doc_id: "doc:gamma"
project: local
kind: note
sensitivity: internal
---
Gamma body should not appear in the codex scope.
`)
	writePolicyFile(t, root, contextcore.Policy{
		ProtocolVersion: contextcore.ProtocolVersion,
		Actors: map[string]contextcore.ActorPolicy{
			"codex": {
				SensitivityCeiling: "internal",
				Projects:           []string{"codex"},
				Capabilities:       []contextcore.Capability{contextcore.CapabilitySearch, contextcore.CapabilityRead},
			},
		},
	})
	if err := contextcore.WriteMCPScope(root, contextcore.MCPScope{
		ProtocolVersion: contextcore.ProtocolVersion,
		Actor:           "codex",
		Capabilities:    []string{"search", "read"},
	}); err != nil {
		t.Fatal(err)
	}
}

func writePolicyFile(t *testing.T, root string, policy contextcore.Policy) {
	t.Helper()
	data, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	policyDir := filepath.Join(root, ".mdsys", "context", "v1")
	if err := os.MkdirAll(policyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(policyDir, "policy.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeDoc(t *testing.T, root, relative, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// ---------- wire helpers ----------

func mcpLine(t *testing.T, id string, method string, params any) string {
	t.Helper()
	var paramsRaw json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		paramsRaw = data
	}
	frame := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if paramsRaw != nil {
		frame["params"] = json.RawMessage(paramsRaw)
	}
	data, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	return string(data) + "\n"
}

// serve sends the input frames to a fresh server bound to root and returns
// all response frames as raw JSON lines.
func serve(t *testing.T, root, input string) []string {
	t.Helper()
	server, err := NewServer(root)
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- server.Run(ctx, strings.NewReader(input), &stdout) }()
	err = <-errCh
	if err != nil && err != context.Canceled {
		t.Fatalf("server.Run: %v", err)
	}
	// Parse the JSON-encoded lines (each frame is a JSON object on its own line).
	var lines []string
	decoder := json.NewDecoder(strings.NewReader(stdout.String()))
	var value any
	for decoder.Decode(&value) == nil {
		encoded, _ := json.Marshal(value)
		lines = append(lines, string(encoded))
	}
	return lines
}

func frameByID(t *testing.T, lines []string, id string) map[string]any {
	t.Helper()
	for _, line := range lines {
		var frame map[string]any
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			continue
		}
		if frameID, _ := frame["id"].(string); frameID == id {
			return frame
		}
	}
	t.Fatalf("no response frame for id %q in %d frames", id, len(lines))
	return nil
}

func toolText(t *testing.T, line map[string]any) (SearchOutput, bool) {
	t.Helper()
	result, _ := line["result"].(map[string]any)
	if result == nil {
		t.Fatalf("frame has no result: %v", line)
	}
	content, _ := result["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(content))
	}
	item, _ := content[0].(map[string]any)
	text, _ := item["text"].(string)
	var out SearchOutput
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("tool result is not a SearchOutput: %v (text=%s)", err, text)
	}
	isError, _ := result["isError"].(bool)
	return out, isError
}

func toolReadText(t *testing.T, line map[string]any) (ReadOutput, bool) {
	t.Helper()
	result, _ := line["result"].(map[string]any)
	if result == nil {
		t.Fatalf("frame has no result: %v", line)
	}
	content, _ := result["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(content))
	}
	item, _ := content[0].(map[string]any)
	text, _ := item["text"].(string)
	var out ReadOutput
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("tool result is not a ReadOutput: %v (text=%s)", err, text)
	}
	isError, _ := result["isError"].(bool)
	return out, isError
}

// ---------- startup rejection ----------

func TestNewServerRejectsMissingAndInvalidRoots(t *testing.T) {
	if _, err := NewServer(""); err == nil {
		t.Fatal("expected error for empty KARTE_DATA_DIR")
	}
	if _, err := NewServer(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Fatal("expected error for nonexistent directory")
	}
	// A directory that lacks policy.json.
	if _, err := NewServer(t.TempDir()); err == nil {
		t.Fatal("expected error for directory without policy.json")
	}
	// A root whose policy has the actor but no mcp-scope.json.
	bare := t.TempDir()
	writePolicyFile(t, bare, contextcore.Policy{
		ProtocolVersion: contextcore.ProtocolVersion,
		Actors: map[string]contextcore.ActorPolicy{
			"codex": {SensitivityCeiling: "internal", Projects: []string{"codex"}},
		},
	})
	if _, err := NewServer(bare); err == nil || !strings.Contains(err.Error(), "mcp-scope.json") {
		t.Fatalf("expected mcp-scope.json refusal, got %v", err)
	}
	// A scope marker where the policy grants search but not read.
	restricted := t.TempDir()
	writePolicyFile(t, restricted, contextcore.Policy{
		ProtocolVersion: contextcore.ProtocolVersion,
		Actors: map[string]contextcore.ActorPolicy{
			"codex": {
				SensitivityCeiling: "internal",
				Projects:           []string{"codex"},
				Capabilities:       []contextcore.Capability{contextcore.CapabilitySearch},
			},
		},
	})
	if err := contextcore.WriteMCPScope(restricted, contextcore.MCPScope{
		ProtocolVersion: contextcore.ProtocolVersion, Actor: "codex", Capabilities: []string{"search", "read"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewServer(restricted); err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("expected read-capability refusal, got %v", err)
	}
	// A scope marker naming an actor absent from policy.json.
	unknown := t.TempDir()
	writePolicyFile(t, unknown, contextcore.Policy{
		ProtocolVersion: contextcore.ProtocolVersion,
		Actors: map[string]contextcore.ActorPolicy{
			"codex": {SensitivityCeiling: "internal", Projects: []string{"codex"}, Capabilities: []contextcore.Capability{contextcore.CapabilitySearch, contextcore.CapabilityRead}},
		},
	})
	policyDir := filepath.Join(unknown, ".mdsys", "context", "v1")
	if err := os.WriteFile(filepath.Join(policyDir, "mcp-scope.json"),
		[]byte(`{"protocol_version":"1.0","actor":"ghost","capabilities":["search","read"]}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewServer(unknown); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("expected unknown-actor refusal, got %v", err)
	}
}

// ---------- protocol round-trips ----------

func TestMCPInitializeListAndSearchRead(t *testing.T) {
	root := t.TempDir()
	buildDedicatedRoot(t, root)
	input := strings.Join([]string{
		mcpLine(t, "init", "initialize", map[string]any{}),
		mcpLine(t, "list", "tools/list", map[string]any{}),
		mcpLine(t, "search", "tools/call", map[string]any{
			"name":      "karte_search",
			"arguments": map[string]any{"query": "planning", "top_k": 5, "projects": []string{"codex"}, "tags": []string{"planning"}},
		}),
		mcpLine(t, "read", "tools/call", map[string]any{
			"name":      "karte_read",
			"arguments": map[string]any{"doc_id": "doc:alpha"},
		}),
	}, "")
	lines := serve(t, root, input)

	// initialize
	initFrame := frameByID(t, lines, "init")
	initResult, _ := initFrame["result"].(map[string]any)
	if initResult == nil {
		t.Fatalf("initialize has no result: %v", initFrame)
	}
	if pv, _ := initResult["protocolVersion"].(string); pv == "" {
		t.Fatalf("initialize missing protocolVersion: %v", initResult)
	}
	serverInfo, _ := initResult["serverInfo"].(map[string]any)
	if serverInfo == nil || serverInfo["name"] != "karte-context" {
		t.Fatalf("initialize missing serverInfo: %v", initResult)
	}

	// tools/list
	listFrame := frameByID(t, lines, "list")
	listResult, _ := listFrame["result"].(map[string]any)
	tools, _ := listResult["tools"].([]any)
	names := map[string]bool{}
	for _, tool := range tools {
		m, _ := tool.(map[string]any)
		if name, _ := m["name"].(string); name != "" {
			names[name] = true
		}
	}
	if !names["karte_search"] || !names["karte_read"] {
		t.Fatalf("tools/list missing expected tools: %v", tools)
	}

	// karte_search
	searchFrame := frameByID(t, lines, "search")
	searchOut, isError := toolText(t, searchFrame)
	if isError {
		t.Fatalf("search returned isError: %v", searchFrame)
	}
	if searchOut.Status != "ok" || len(searchOut.Results) != 1 {
		t.Fatalf("search should return exactly alpha, got status=%s results=%#v", searchOut.Status, searchOut.Results)
	}
	result := searchOut.Results[0]
	if result.DocID != "doc:alpha" || result.RelativePath == "" || result.SHA256 == "" {
		t.Fatalf("search result lost metadata: %#v", result)
	}
	if !strings.Contains(strings.Join(result.Tags, ","), "planning") {
		t.Fatalf("search result missing planning tag: %#v", result.Tags)
	}

	// karte_read
	readFrame := frameByID(t, lines, "read")
	readOut, isError := toolReadText(t, readFrame)
	if isError {
		t.Fatalf("read returned isError: %v", readFrame)
	}
	if readOut.Status != "ok" || readOut.Document == nil || readOut.Document.DocID != "doc:alpha" {
		t.Fatalf("bad read output: status=%s doc=%#v", readOut.Status, readOut.Document)
	}
	if !strings.Contains(readOut.Document.Body, "Alpha planning body") {
		t.Fatalf("read document body mismatch: %q", readOut.Document.Body)
	}
	if readOut.Document.RelativePath == "" || readOut.Document.SHA256 == "" {
		t.Fatalf("read document lost canonical fields: %#v", readOut.Document)
	}
}

// ---------- privacy: tag filter, denial, existence hiding ----------

func TestMCPRespectsPolicyAndHidesExistence(t *testing.T) {
	root := t.TempDir()
	buildDedicatedRoot(t, root)
	input := strings.Join([]string{
		// Tag-filtered search: "confidential" with tags=[planning] must exclude beta.
		mcpLine(t, "s1", "tools/call", map[string]any{
			"name":      "karte_search",
			"arguments": map[string]any{"query": "confidential", "tags": []string{"planning"}},
		}),
		// Reading a confidential doc (ceiling=internal) must be denied.
		mcpLine(t, "r1", "tools/call", map[string]any{
			"name":      "karte_read",
			"arguments": map[string]any{"doc_id": "doc:beta"},
		}),
		// Reading an unknown doc_id must also be denied (existence hidden).
		mcpLine(t, "r2", "tools/call", map[string]any{
			"name":      "karte_read",
			"arguments": map[string]any{"doc_id": "doc:does-not-exist"},
		}),
		// Reading a doc that exists only in the local project.
		mcpLine(t, "r3", "tools/call", map[string]any{
			"name":      "karte_read",
			"arguments": map[string]any{"doc_id": "doc:gamma"},
		}),
	}, "")
	lines := serve(t, root, input)

	// Tag-filtered search returns zero results.
	s1Frame := frameByID(t, lines, "s1")
	s1Out, isError := toolText(t, s1Frame)
	if isError {
		t.Fatalf("s1 returned isError: %v", s1Frame)
	}
	if s1Out.Status != "ok" || len(s1Out.Results) != 0 {
		t.Fatalf("tag-filtered search leaked a result: %#v", s1Out)
	}

	// All three reads must be denied with no document body.
	for _, id := range []string{"r1", "r2", "r3"} {
		f := frameByID(t, lines, id)
		out, isError := toolReadText(t, f)
		if isError {
			t.Fatalf("%s returned protocol-level error: %v", id, f)
		}
		if out.Status != "denied" || out.Document != nil {
			t.Fatalf("%s: existence or content disclosed: status=%s doc=%#v", id, out.Status, out.Document)
		}
	}
}

// ---------- cross-root isolation ----------

func TestMCPCrossRootIsolation(t *testing.T) {
	codexRoot := t.TempDir()
	localRoot := t.TempDir()
	buildDedicatedRoot(t, codexRoot)

	// Local root has its own policy and a unique document.
	writeDoc(t, localRoot, "content/projects/local/notes/local-only.md", `---
title: "Local only"
tags: local
doc_id: "doc:local-only"
project: local
kind: note
sensitivity: internal
---
Unique phrase that must never cross roots.
`)
	writePolicyFile(t, localRoot, contextcore.Policy{
		ProtocolVersion: contextcore.ProtocolVersion,
		Actors: map[string]contextcore.ActorPolicy{
			"local": {SensitivityCeiling: "internal", Projects: []string{"local"}, Capabilities: []contextcore.Capability{contextcore.CapabilitySearch, contextcore.CapabilityRead}},
		},
	})
	if err := contextcore.WriteMCPScope(localRoot, contextcore.MCPScope{
		ProtocolVersion: contextcore.ProtocolVersion, Actor: "local", Capabilities: []string{"search", "read"},
	}); err != nil {
		t.Fatal(err)
	}

	// The codex server must not see the local root's unique phrase.
	input := mcpLine(t, "cross", "tools/call", map[string]any{
		"name":      "karte_search",
		"arguments": map[string]any{"query": "Unique phrase that must never cross roots"},
	})
	lines := serve(t, codexRoot, input)
	crossFrame := frameByID(t, lines, "cross")
	out, isError := toolText(t, crossFrame)
	if isError {
		t.Fatalf("cross returned isError: %v", crossFrame)
	}
	if out.Status != "ok" || len(out.Results) != 0 {
		t.Fatalf("cross-root leak detected: %#v", out)
	}

	// Verify DataRoot is the resolved codex root, not the local root.
	server, err := NewServer(codexRoot)
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(codexRoot)
	if server.DataRoot() != resolved {
		t.Fatalf("DataRoot mismatch: got %s want %s", server.DataRoot(), resolved)
	}
	_ = localRoot
}

// ---------- protocol edge cases ----------

func TestMCPRejectsUnknownToolAndMethod(t *testing.T) {
	root := t.TempDir()
	buildDedicatedRoot(t, root)
	input := strings.Join([]string{
		mcpLine(t, "unknown", "tools/call", map[string]any{"name": "nope", "arguments": map[string]any{}}),
		mcpLine(t, "badmethod", "bogus/method", map[string]any{}),
	}, "")
	lines := serve(t, root, input)

	unknownFrame := frameByID(t, lines, "unknown")
	if rpcErr, ok := unknownFrame["error"].(map[string]any); !ok {
		t.Fatalf("expected JSON-RPC error for unknown tool, got %v", unknownFrame)
	} else if !strings.Contains(fmt.Sprint(rpcErr["message"]), "unknown tool") {
		t.Fatalf("unexpected error message: %v", rpcErr)
	}

	badMethodFrame := frameByID(t, lines, "badmethod")
	if rpcErr, ok := badMethodFrame["error"].(map[string]any); !ok {
		t.Fatalf("expected JSON-RPC error for unknown method, got %v", badMethodFrame)
	} else if !strings.Contains(fmt.Sprint(rpcErr["message"]), "method not found") {
		t.Fatalf("unexpected error message: %v", rpcErr)
	}
}

func TestMCPHandleMalformedJSON(t *testing.T) {
	root := t.TempDir()
	buildDedicatedRoot(t, root)
	// Send a malformed line followed by a valid ping.
	input := "{not-json\n" + mcpLine(t, "ping", "ping", map[string]any{})
	lines := serve(t, root, input)
	// The malformed line produces a parse-error frame with id=null.
	// The ping should still be answered.
	found := false
	for _, line := range lines {
		var frame map[string]any
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			continue
		}
		if id, _ := frame["id"].(string); id == "ping" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ping response missing after malformed line: %v", lines)
	}
}

// ---------- summary ----------

func TestMCPSummaryExposesRootIdentity(t *testing.T) {
	root := t.TempDir()
	buildDedicatedRoot(t, root)
	server, err := NewServer(root)
	if err != nil {
		t.Fatal(err)
	}
	summary := server.Summary()
	for _, want := range []string{"karte-context", "actor=codex", "policy_ceiling=internal"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q: %s", want, summary)
		}
	}
	for _, secret := range []string{"Alpha", "doc:alpha", "planning body"} {
		if strings.Contains(summary, secret) {
			t.Fatalf("summary leaked document content %q: %s", secret, summary)
		}
	}
}
