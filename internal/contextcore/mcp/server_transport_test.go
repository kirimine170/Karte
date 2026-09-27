package mcp

import (
	"bytes"
	"context"
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
