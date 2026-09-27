package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"karte/internal/contextcore"
)

func TestMCPBoundedFramesAndRecovery(t *testing.T) {
	root := t.TempDir()
	buildDedicatedRoot(t, root)
	// The limit counts JSON bytes, excluding the newline delimiter.
	valid := strings.TrimSuffix(mcpLine(t, "before", "ping", nil), "\n")
	valid += strings.Repeat(" ", maxFrameBytes-len(valid))
	tooLarge := valid + " "
	input := valid + "\n" + tooLarge + "\n" + mcpLine(t, "after", "ping", nil)
	lines := serve(t, root, input)
	if len(lines) != 3 {
		t.Fatalf("expected ping, size error, ping on one stream; got %v", lines)
	}
	frameByID(t, lines, "before")
	frameByID(t, lines, "after")
	var rejected struct {
		Error *rpcError `json:"error"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &rejected); err != nil || rejected.Error == nil || rejected.Error.Message != "frame too large" {
		t.Fatalf("oversized frame was not rejected: %s, %v", lines[1], err)
	}

	// A stream with no newline must be rejected at EOF with bounded memory.
	server, err := NewServer(root)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := server.Run(context.Background(), strings.NewReader(strings.Repeat("x", 16*maxFrameBytes)), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "frame too large") {
		t.Fatalf("newline-free frame was not rejected: %s", out.String())
	}
}

func TestMCPFrameMemoryBound(t *testing.T) {
	root := t.TempDir()
	buildDedicatedRoot(t, root)
	server, err := NewServer(root)
	if err != nil {
		t.Fatal(err)
	}
	input := strings.NewReader(strings.Repeat("x", 16*maxFrameBytes))
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if err := server.Run(context.Background(), input, io.Discard); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 8*maxFrameBytes {
		t.Fatalf("oversized frame allocated %d bytes inside Run", allocated)
	}
}

func TestMCPRunCancellationWhileInputIdle(t *testing.T) {
	root := t.TempDir()
	buildDedicatedRoot(t, root)
	server, err := NewServer(root)
	if err != nil {
		t.Fatal(err)
	}
	reader, writer := io.Pipe()
	defer writer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	completed := make(chan error, 1)
	go func() { completed <- server.Run(ctx, reader, io.Discard) }()
	cancel()
	select {
	case err := <-completed:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run returned %v after cancellation", err)
		}
	case <-time.After(time.Second):
		reader.Close()
		t.Fatal("Run remained blocked on idle input")
	}
}

type rejectingWriter struct{}

var errWriterClosed = errors.New("writer closed")

func (rejectingWriter) Write([]byte) (int, error) { return 0, errWriterClosed }

func TestMCPRunPropagatesOutputFailure(t *testing.T) {
	root := t.TempDir()
	buildDedicatedRoot(t, root)
	server, err := NewServer(root)
	if err != nil {
		t.Fatal(err)
	}
	err = server.Run(context.Background(), strings.NewReader(mcpLine(t, "ping", "ping", nil)), rejectingWriter{})
	if !errors.Is(err, errWriterClosed) {
		t.Fatalf("output failure was lost: %v", err)
	}
}

func TestMCPNotificationsHaveNoResponse(t *testing.T) {
	root := t.TempDir()
	buildDedicatedRoot(t, root)
	server, err := NewServer(root)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	input := "{\"jsonrpc\":\"2.0\",\"method\":\"notifications/cancelled\"}\n" + mcpLine(t, "ping", "ping", nil)
	if err := server.Run(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], `"id":"ping"`) {
		t.Fatalf("notification received a response: %s", out.String())
	}
}

func TestMCPRejectsUnknownArguments(t *testing.T) {
	root := t.TempDir()
	buildDedicatedRoot(t, root)
	for _, tc := range []struct{ name, arguments string }{
		{"karte_search", `{"query":"planning","project":["local"]}`},
		{"karte_read", `{"doc_id":"doc:alpha","unused":true}`},
	} {
		input := mcpLine(t, "call", "tools/call", map[string]any{"name": tc.name, "arguments": json.RawMessage(tc.arguments)})
		lines := serve(t, root, input)
		frame := frameByID(t, lines, "call")
		result, _ := frame["result"].(map[string]any)
		if result["isError"] != true {
			t.Fatalf("%s accepted undeclared arguments: %v", tc.name, frame)
		}
	}
}

func TestMCPPreServiceValidationIsAudited(t *testing.T) {
	root := t.TempDir()
	buildDedicatedRoot(t, root)
	input := mcpLine(t, "empty-query", "tools/call", map[string]any{
		"name": "karte_search", "arguments": map[string]any{"query": ""},
	}) + mcpLine(t, "empty-id", "tools/call", map[string]any{
		"name": "karte_read", "arguments": map[string]any{"doc_id": ""},
	}) + mcpLine(t, "unknown-field", "tools/call", map[string]any{
		"name": "karte_search", "arguments": map[string]any{"query": "planning", "project": "codex"},
	})
	ids := []string{"empty-query", "empty-id", "unknown-field"}
	for _, tc := range []struct {
		id    string
		value any
	}{
		{"zero-top-k", 0}, {"negative-top-k", -1}, {"large-top-k", 21}, {"null-top-k", nil},
	} {
		input += mcpLine(t, tc.id, "tools/call", map[string]any{
			"name": "karte_search", "arguments": map[string]any{"query": "planning", "top_k": tc.value},
		})
		ids = append(ids, tc.id)
	}
	for _, tc := range []struct {
		id    string
		field string
		value any
	}{
		{"null-projects", "projects", nil},
		{"null-tags", "tags", nil},
		{"empty-projects", "projects", []string{}},
		{"empty-tags", "tags", []string{}},
		{"null-sensitivity", "sensitivity", nil},
		{"empty-sensitivity", "sensitivity", ""},
		{"unknown-sensitivity", "sensitivity", "secret"},
	} {
		arguments := map[string]any{"query": "planning", tc.field: tc.value}
		input += mcpLine(t, tc.id, "tools/call", map[string]any{
			"name": "karte_search", "arguments": arguments,
		})
		ids = append(ids, tc.id)
	}
	lines := serve(t, root, input)
	if len(lines) != len(ids) {
		t.Fatalf("expected %d responses, got %d", len(ids), len(lines))
	}
	for _, id := range ids {
		frame := frameByID(t, lines, id)
		result, _ := frame["result"].(map[string]any)
		if result["isError"] != true {
			t.Fatalf("%s was not rejected: %v", id, frame)
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, ".mdsys", "context", "v1", "audit"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(ids) {
		t.Fatalf("expected %d invalid audit events, got %d", len(ids), len(entries))
	}
	counts := map[string]int{}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(root, ".mdsys", "context", "v1", "audit", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var event contextcore.AuditEvent
		if err := json.Unmarshal(data, &event); err != nil {
			t.Fatal(err)
		}
		if event.Status != "invalid" || event.ResultCount != 0 || event.ErrorCode != "invalid_arguments" {
			t.Fatalf("wrong pre-service audit event: %+v", event)
		}
		counts[event.Operation]++
	}
	if counts["search"] != 13 || counts["read"] != 1 {
		t.Fatalf("wrong audited operations: %v", counts)
	}
}

func TestMCPReadSchemaAdvertisesDocIDLimit(t *testing.T) {
	root := t.TempDir()
	buildDedicatedRoot(t, root)
	frame := frameByID(t, serve(t, root, mcpLine(t, "list", "tools/list", nil)), "list")
	result := frame["result"].(map[string]any)
	for _, tool := range result["tools"].([]any) {
		item := tool.(map[string]any)
		if item["name"] != "karte_read" {
			continue
		}
		schema := item["inputSchema"].(map[string]any)
		properties := schema["properties"].(map[string]any)
		docID := properties["doc_id"].(map[string]any)
		if docID["minLength"] != float64(1) || docID["maxLength"] != float64(256) {
			t.Fatalf("wrong doc_id length schema: %v", docID)
		}
		return
	}
	t.Fatal("karte_read was not listed")
}

func TestMCPSearchSchemaAdvertisesFilterBounds(t *testing.T) {
	root := t.TempDir()
	buildDedicatedRoot(t, root)
	frame := frameByID(t, serve(t, root, mcpLine(t, "list", "tools/list", nil)), "list")
	result := frame["result"].(map[string]any)
	for _, tool := range result["tools"].([]any) {
		item := tool.(map[string]any)
		if item["name"] != "karte_search" {
			continue
		}
		schema := item["inputSchema"].(map[string]any)
		properties := schema["properties"].(map[string]any)
		projects := properties["projects"].(map[string]any)
		projectItem := projects["items"].(map[string]any)
		if projects["minItems"] != float64(1) || projects["maxItems"] != float64(64) || projectItem["maxLength"] != float64(64) || projectItem["pattern"] != `^(\*|[a-z0-9][a-z0-9._-]{0,63})$` {
			t.Fatalf("wrong project filter bounds: %v", projects)
		}
		tags := properties["tags"].(map[string]any)
		tagItem := tags["items"].(map[string]any)
		if tags["minItems"] != float64(1) || tags["maxItems"] != float64(64) || tagItem["maxLength"] != float64(128) || tagItem["minLength"] != float64(1) {
			t.Fatalf("wrong tag filter bounds: %v", tags)
		}
		return
	}
	t.Fatal("karte_search was not listed")
}

func TestMCPInvalidArgumentAuditUsesLiveActor(t *testing.T) {
	root := t.TempDir()
	buildDedicatedRoot(t, root)
	server, err := NewServer(root)
	if err != nil {
		t.Fatal(err)
	}
	rewritePolicy(t, root, func(policy *contextcore.Policy) {
		policy.Actors["alternate"] = contextcore.ActorPolicy{
			SensitivityCeiling: "internal",
			Projects:           []string{"codex"},
			Capabilities:       []contextcore.Capability{contextcore.CapabilitySearch, contextcore.CapabilityRead},
		}
	})
	if err := contextcore.WriteMCPScope(root, contextcore.MCPScope{
		ProtocolVersion: contextcore.ProtocolVersion,
		Actor:           "alternate",
		Capabilities:    []string{"search", "read"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.search(json.RawMessage(`{"query":""}`)); err == nil {
		t.Fatal("empty query was accepted")
	}
	entries, err := os.ReadDir(filepath.Join(root, ".mdsys", "context", "v1", "audit"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one audit event: entries=%v err=%v", entries, err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".mdsys", "context", "v1", "audit", entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var event contextcore.AuditEvent
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatal(err)
	}
	actorHash := sha256.Sum256([]byte("alternate"))
	if event.ActorIDSHA256 != hex.EncodeToString(actorHash[:]) || event.Status != "invalid" {
		t.Fatalf("validation audit was not attributed to live actor: %+v", event)
	}
}

func TestMCPRejectsUnknownScopeMarkerFields(t *testing.T) {
	root := t.TempDir()
	buildDedicatedRoot(t, root)
	server, err := NewServer(root)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, ".mdsys", "context", "v1", "mcp-scope.json")
	data := []byte(`{"protocol_version":"1.0","actor":"codex","capabilities":["search","read"],"projects":["codex"]}`)
	if err := os.WriteFile(marker, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewServer(root); err == nil {
		t.Fatal("unknown marker field was accepted at startup")
	}
	if _, err := server.search(json.RawMessage(`{"query":"planning"}`)); err == nil {
		t.Fatal("unknown marker field was accepted after startup")
	}
}

func TestMCPRejectsOversizedScopeActor(t *testing.T) {
	root := t.TempDir()
	buildDedicatedRoot(t, root)
	longActor := strings.Repeat("界", 129)
	rewritePolicy(t, root, func(policy *contextcore.Policy) {
		policy.Actors[longActor] = contextcore.ActorPolicy{
			SensitivityCeiling: "internal",
			Projects:           []string{"codex"},
			Capabilities:       []contextcore.Capability{contextcore.CapabilitySearch, contextcore.CapabilityRead},
		}
	})
	scope := contextcore.MCPScope{
		ProtocolVersion: contextcore.ProtocolVersion,
		Actor:           longActor,
		Capabilities:    []string{"search", "read"},
	}
	if err := contextcore.WriteMCPScope(root, scope); err == nil {
		t.Fatal("writer accepted an actor too long for Request.Validate")
	}
	data, err := json.Marshal(scope)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, ".mdsys", "context", "v1", "mcp-scope.json")
	if err := os.WriteFile(marker, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewServer(root); err == nil {
		t.Fatal("startup accepted an actor too long for Request.Validate")
	}
}

func TestMCPAuditFailureIsSanitized(t *testing.T) {
	root := t.TempDir()
	buildDedicatedRoot(t, root)
	// A regular file at the audit directory gives a deterministic write error.
	auditPath := filepath.Join(root, ".mdsys", "context", "v1", "audit")
	if err := os.WriteFile(auditPath, []byte("blocker"), 0o600); err != nil {
		t.Fatal(err)
	}
	lines := serve(t, root, mcpLine(t, "search", "tools/call", map[string]any{
		"name": "karte_search", "arguments": map[string]any{"query": "planning"},
	}))
	frame := frameByID(t, lines, "search")
	result, _ := frame["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("audit failure was reported as success: %v", frame)
	}
	if strings.Contains(lines[0], root) || strings.Contains(lines[0], "Alpha planning body") {
		t.Fatalf("tool response exposed local data: %s", lines[0])
	}
}

func TestMCPAuditRecordsDenialAndValidationCode(t *testing.T) {
	root := t.TempDir()
	buildDedicatedRoot(t, root)
	input := mcpLine(t, "denied", "tools/call", map[string]any{
		"name": "karte_read", "arguments": map[string]any{"doc_id": "doc:beta"},
	}) + mcpLine(t, "invalid", "tools/call", map[string]any{
		"name": "karte_search", "arguments": map[string]any{"query": "planning", "projects": []string{"UpperCase"}},
	})
	lines := serve(t, root, input)
	if len(lines) != 2 {
		t.Fatalf("expected two tool responses: %v", lines)
	}
	entries, err := os.ReadDir(filepath.Join(root, ".mdsys", "context", "v1", "audit"))
	if err != nil {
		t.Fatal(err)
	}
	seenDenial, seenInvalid := false, false
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(root, ".mdsys", "context", "v1", "audit", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var event contextcore.AuditEvent
		if err := json.Unmarshal(data, &event); err != nil {
			t.Fatal(err)
		}
		if event.Operation == "read" && event.Status == "denied" && event.ResultCount == 0 {
			seenDenial = true
		}
		if event.Operation == "search" && event.Status == "invalid" && event.ResultCount == 0 && event.ErrorCode != "" {
			seenInvalid = true
		}
	}
	if !seenDenial || !seenInvalid {
		t.Fatalf("missing denied/invalid audit events: denied=%v invalid=%v", seenDenial, seenInvalid)
	}
}
