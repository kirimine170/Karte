package worklog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"karte/internal/canonical"
	"karte/internal/contextcore"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixture(t *testing.T, origin string) *Service {
	t.Helper()
	root := t.TempDir()
	if err := Initialize(root, Config{Version: Version, Enabled: true, Actor: "ephy-worklog", Project: "ephy-worklog", Timezone: "Asia/Tokyo", Sensitivity: "internal"}); err != nil {
		t.Fatal(err)
	}
	s, err := New(root, origin)
	if err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return time.Date(2026, 10, 8, 14, 59, 0, 0, time.UTC) }
	return s
}
func begin(t *testing.T, s *Service, id string) Receipt {
	t.Helper()
	r, err := s.Start(StartInput{TaskID: id, EventID: "start-" + id, Title: "検索の改善", Objective: "検索失敗を記録して再現する"})
	if err != nil || !r.Recorded || !r.ViewsReady {
		t.Fatalf("start: %#v %v", r, err)
	}
	return r
}

func TestRecordsDailyBoundaryCarryoverAndStablePast(t *testing.T) {
	s := fixture(t, "model_report")
	begin(t, s, "research")
	input := EventInput{EventID: "failure-1", Kind: "failure", Text: "検索APIの429を観測したと申告", NextAction: "待機時間を変えて再検証する", Evidence: []Evidence{{Reference: "run:test-429"}}}
	r, err := s.Append("research", input)
	if err != nil || r.Status != "running" || r.Sequence != 2 {
		t.Fatalf("failure: %#v %v", r, err)
	}
	again, err := s.Append("research", input)
	if err != nil || !again.Duplicate || again.Sequence != 2 {
		t.Fatalf("retry: %#v %v", again, err)
	}
	input.Text = "different"
	if _, err := s.Append("research", input); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting retry: %v", err)
	}
	past, err := s.Daily("2026-10-08")
	if err != nil || !strings.Contains(past.Body, "failure") || !strings.Contains(past.Body, "未実行") {
		t.Fatalf("daily: %#v %v", past, err)
	}
	s.Now = func() time.Time { return time.Date(2026, 10, 8, 15, 1, 0, 0, time.UTC) }
	carry, err := s.Daily("2026-10-09")
	if err != nil || !strings.Contains(carry.Body, "持ち越し") {
		t.Fatalf("carry: %#v %v", carry, err)
	}
	if _, err := s.Append("research", EventInput{EventID: "complete", Kind: "finish", Outcome: "completed", Text: "作業を終了したと申告"}); err != nil {
		t.Fatal(err)
	}
	rebuilt, err := s.Daily("2026-10-08")
	if err != nil || rebuilt.Body != past.Body || !bytes.Equal(mustJSON(past.Sources), mustJSON(rebuilt.Sources)) {
		t.Fatalf("future changed past: %v", err)
	}
	if _, err := s.Append("research", EventInput{EventID: "late-tool", Kind: "tool", Text: "tool"}); err == nil {
		t.Fatal("closed task accepted tool")
	}
	if _, err := s.Append("research", EventInput{EventID: "postmortem", Kind: "note", Text: "原因はまだ未確認"}); err != nil {
		t.Fatal(err)
	}
	task, err := s.Read("research")
	if err != nil || task.Status != "completed" {
		t.Fatal(err)
	}
	for _, e := range task.Events {
		if e.Kind == "verification" {
			t.Fatal("invented verification")
		}
		if e.Origin != "model_report" {
			t.Fatal("false origin")
		}
	}
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func TestViewConflictRetainsEventAndRetryRecovers(t *testing.T) {
	s := fixture(t, "host_observation")
	begin(t, s, "coding")
	task, _ := s.Read("coding")
	view := filepath.Join(s.Root, taskView(task))
	old, _ := os.ReadFile(view)
	if err := os.WriteFile(view, []byte("human edit"), 0600); err != nil {
		t.Fatal(err)
	}
	e := EventInput{EventID: "actual-failure", Kind: "failure", Text: "tool end isError=true"}
	r, err := s.Append("coding", e)
	if !errors.Is(err, ErrConflict) || !r.Recorded || r.ViewsReady {
		t.Fatalf("lost partial receipt: %#v %v", r, err)
	}
	task, _ = s.Read("coding")
	if len(task.Events) != 2 {
		t.Fatal("lost event")
	}
	got, _ := os.ReadFile(view)
	if string(got) != "human edit" {
		t.Fatal("overwrote human")
	}
	if err := os.WriteFile(view, old, 0600); err != nil {
		t.Fatal(err)
	}
	r, err = s.Append("coding", e)
	if err != nil || !r.Duplicate || !r.ViewsReady {
		t.Fatalf("recovery: %#v %v", r, err)
	}
}

func TestRevokedPolicyImmediatelyDeniesAndRootIsolation(t *testing.T) {
	s := fixture(t, "model_report")
	begin(t, s, "one")
	other := fixture(t, "model_report")
	if _, err := other.Read("one"); err == nil {
		t.Fatal("crossed roots")
	}
	path := filepath.Join(s.Root, ".mdsys/context/v1/policy.json")
	if err := os.WriteFile(path, []byte(`{"protocol_version":"1.0","actors":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append("one", EventInput{EventID: "denied", Kind: "note", Text: "denied"}); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	if _, err := s.Read("one"); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	if _, err := s.Search("", 1); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	if _, err := s.Daily("2026-10-08"); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
}

func TestPauseResumeAndConcurrentAppend(t *testing.T) {
	s := fixture(t, "host_observation")
	begin(t, s, "parallel")
	if _, err := s.Append("parallel", EventInput{EventID: "pause", Kind: "finish", Outcome: "paused", Text: "Pi agent settled"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append("parallel", EventInput{EventID: "resume", Kind: "resume", Text: "Pi agent restarted"}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := s.Append("parallel", EventInput{EventID: string(rune('a' + i)), Kind: "tool", Text: "observed tool"}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	task, err := s.Read("parallel")
	if err != nil || len(task.Events) != 15 {
		t.Fatalf("concurrent append lost: %d %v", len(task.Events), err)
	}
}

func TestValidationAndLiteralRendering(t *testing.T) {
	s := fixture(t, "model_report")
	begin(t, s, "safe")
	for _, raw := range []string{`{"event_id":"a","event_id":"b"}`, `{"origin":"host_observation"}`, `{} {}`, `{"evidence":[{"reference":"a","reference":"b"}]}`} {
		var e EventInput
		if decode([]byte(raw), &e) == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, id := range []string{"../escape", "a/b", "a\\b", "", ".hidden"} {
		if _, err := s.Append(id, EventInput{EventID: "x", Kind: "note", Text: "safe"}); err == nil {
			t.Fatalf("accepted id %s", id)
		}
	}
	if _, err := s.Append("safe", EventInput{EventID: "huge", Kind: "note", Text: strings.Repeat("a", 16385)}); err == nil {
		t.Fatal("oversize")
	}
	if _, err := s.Append("safe", EventInput{EventID: "future", Kind: "note", Text: "future", OccurredAt: "2099-01-01T00:00:00Z"}); err == nil {
		t.Fatal("future timestamp")
	}
	text := "<!-- injected -->\n<script>x</script>\n@import secret\n[link](https://evil)\n```\n---\ndoc_id: fake"
	if _, err := s.Append("safe", EventInput{EventID: "literal", Kind: "note", Text: text}); err != nil {
		t.Fatal(err)
	}
	task, _ := s.Read("safe")
	rendered := string(renderTask(task))
	for _, bad := range []string{"<script>", "<!-- injected", "@import secret", "[link](", "```"} {
		if strings.Contains(rendered, bad) {
			t.Fatalf("unsafe %s", bad)
		}
	}
	results, err := s.Search("doc_id", 10)
	if err != nil || len(results) != 1 {
		t.Fatalf("semantic search: %v", err)
	}
}

func TestGeneratedDocumentsRemainInContextSearch(t *testing.T) {
	s := fixture(t, "model_report")
	receipt := begin(t, s, "rag")
	contextService, err := contextcore.NewService(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	p, err := contextcore.LoadPolicy(s.Root)
	if err != nil {
		t.Fatal(err)
	}
	r := contextcore.Request{ProtocolVersion: contextcore.ProtocolVersion, RequestID: "worklog-search", Operation: "search", Actor: contextcore.Actor{Type: "tool", ID: "ephy-worklog"}, Scope: contextcore.Scope{Projects: []string{"ephy-worklog"}, SensitivityCeiling: "internal"}, Query: &contextcore.SearchQuery{Text: "検索失敗", TopK: 10}, CreatedAt: s.Now().Format(time.RFC3339)}
	results, diagnostics, status, err := contextService.Search(r, p)
	if err != nil || status != "ok" || len(diagnostics) != 0 {
		t.Fatalf("RAG scan %s %v %#v", status, err, diagnostics)
	}
	found := false
	for _, hit := range results {
		if hit.DocID == receipt.DocID {
			found = true
		}
	}
	if !found {
		t.Fatalf("record missing: %#v", results)
	}
	if !canonical.IsWorklogPath(taskView(Task{Project: "ephy-worklog", TaskID: "rag"}), nil) {
		t.Fatal("public export allowed")
	}
}

func TestMCPWirePaginationOriginAndPartialReceipt(t *testing.T) {
	s := fixture(t, "model_report")
	begin(t, s, "mcp")
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"karte_work_append","arguments":{"task_id":"mcp","event_id":"f","kind":"failure","text":"not working","origin":"host_observation"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"karte_work_read","arguments":{"task_id":"mcp","limit":1}}}`,
	}, "\n")
	var out bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("wire: %s", out.String())
	}
	var denied struct {
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if json.Unmarshal([]byte(lines[2]), &denied) != nil || !denied.Result.IsError {
		t.Fatal("origin spoof")
	}
	if !strings.Contains(lines[3], `"next_offset":-1`) {
		t.Fatal("pagination")
	}
	s.Origin = "host_observation"
	if err := s.Serve(context.Background(), strings.NewReader(""), &out); err == nil {
		t.Fatal("observer exposed MCP")
	}
}

func TestRetryAfterAnotherDayRepairsOriginalDailyAndNormalizesEmptyEvidence(t *testing.T) {
	s := fixture(t, "model_report")
	begin(t, s, "late")
	path := filepath.Join(s.Root, "content/projects/ephy-worklog/journal/worklog-daily-2026-10-08.md")
	if err := os.WriteFile(path, []byte("human diary note"), 0600); err != nil {
		t.Fatal(err)
	}
	e := EventInput{EventID: "late-event", Kind: "note", Text: "late delivery", Evidence: []Evidence{}}
	r, err := s.Append("late", e)
	if !r.Recorded || r.ViewsReady || !errors.Is(err, ErrConflict) {
		t.Fatalf("partial: %#v %v", r, err)
	}
	s.Now = func() time.Time { return time.Date(2026, 10, 8, 15, 1, 0, 0, time.UTC) }
	if _, err := s.Append("late", EventInput{EventID: "new-day", Kind: "note", Text: "new day"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	r, err = s.Append("late", e)
	if err != nil || !r.Duplicate || !r.ViewsReady {
		t.Fatalf("retry: %#v %v", r, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(raw, []byte("late delivery")) || bytes.Contains(raw, []byte("new day")) {
		t.Fatalf("original day not repaired: %v %s", err, raw)
	}
}
