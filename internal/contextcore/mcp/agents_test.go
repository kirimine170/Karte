package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"karte/internal/canonical"
	"karte/internal/contextcore"
	"karte/internal/decisionlog"
	"karte/internal/ephyoutbox"
)

func TestAgentMCPAuthenticationAndProposalOnly(t *testing.T) {
	root := t.TempDir()
	actors := []string{decisionlog.AgentGrok, decisionlog.AgentMuse, decisionlog.AgentChatGPT}
	policy := contextcore.DefaultPolicy()
	tokens := map[string]string{}
	for _, actor := range actors {
		policy.Actors[actor] = contextcore.ActorPolicy{
			Projects: []string{"karte"}, SensitivityCeiling: "internal",
			Capabilities: []contextcore.Capability{contextcore.CapabilitySearch, contextcore.CapabilityRead, contextcore.CapabilityPropose},
		}
		var err error
		tokens[actor], err = decisionlog.IssueAgentToken(root, actor)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := canonical.WithWriter(root, func(w *canonical.Writer) error {
		raw, err := json.Marshal(policy)
		if err != nil {
			return err
		}
		if err := w.Write(".mdsys/context/v1/policy.json", raw, 0600); err != nil {
			return err
		}
		if err := decisionlog.InitializeSettings(w); err != nil {
			return err
		}
		records, err := (decisionlog.Settings{}).ExtractSummary("2026-11-12_fixture.txt",
			[]byte("【Karte】【決定】全員向けテスト。\n【Karte】【決定】Grok限定テスト。"),
			"https://example.invalid/source", time.Now())
		if err != nil || len(records) != 2 {
			t.Fatalf("extract fixtures: %v", err)
		}
		records[0].VisibilityTags = actors
		records[1].VisibilityTags = []string{decisionlog.AgentGrok}
		for _, record := range records {
			if _, err := decisionlog.Accept(w, record); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	handler := agentHTTPHandler(root)
	call := func(token, tool string, arguments any) *httptest.ResponseRecorder {
		t.Helper()
		raw := mcpLine(t, "test", "tools/call", map[string]any{"name": tool, "arguments": arguments})
		request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(raw))
		request.Header.Set("Content-Type", "application/json")
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	for _, actor := range actors {
		response := call(tokens[actor], "karte_search", map[string]any{"query": "テスト"})
		body := response.Body.String()
		if response.Code != 200 || !strings.Contains(body, "全員向けテスト") ||
			(strings.Contains(body, "Grok限定テスト") != (actor == decisionlog.AgentGrok)) {
			t.Fatalf("recipient filter failed for %s (HTTP %d)", actor, response.Code)
		}
	}
	forged := strings.Replace(tokens[decisionlog.AgentGrok], decisionlog.AgentGrok, decisionlog.AgentChatGPT, 1)
	proposal := map[string]any{
		"business": "karte", "date": "2026-11-12", "content": "候補を投稿する。",
		"source": map[string]string{"url": "https://example.invalid/agent", "sha256": strings.Repeat("1", 64)},
	}
	for _, token := range []string{"", forged} {
		if response := call(token, "karte_search", map[string]any{"query": "テスト"}); response.Code != 401 {
			t.Fatal("unauthenticated search succeeded")
		}
		if response := call(token, "karte_propose_decision", proposal); response.Code != 401 {
			t.Fatal("unauthenticated submission succeeded")
		}
	}
	if response := call(tokens[decisionlog.AgentChatGPT], "karte_accept_decision", proposal); !strings.Contains(response.Body.String(), "unknown tool") {
		t.Fatal("agent could call a review tool")
	}

	// The same authentication boundary applies over stdio.
	server, err := NewAgentServer(root, tokens[decisionlog.AgentChatGPT])
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	line := mcpLine(t, "agent-proposal", "tools/call", map[string]any{"name": "karte_propose_decision", "arguments": proposal})
	if err := server.Run(context.Background(), strings.NewReader(line), &output); err != nil ||
		!strings.Contains(output.String(), "queued") {
		t.Fatalf("agent could not queue a proposal: %v", err)
	}
	store, err := ephyoutbox.NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	items, invalid, err := store.ListPending()
	if err != nil || len(invalid) != 0 || len(items) != 1 ||
		items[0].ProposedFrontmatter["submitted_by"] != decisionlog.AgentChatGPT {
		t.Fatalf("expected one audited pending candidate: %v", err)
	}
	record, err := decisionlog.DecodeProposal(items[0])
	if err != nil || record.Status != decisionlog.Open || record.Decider != decisionlog.Reviewer ||
		record.VisibleTo([]string{decisionlog.Public, decisionlog.AgentChatGPT}) {
		t.Fatalf("candidate was not private and reviewable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, decisionlog.Path(items[0].CandidateID))); !os.IsNotExist(err) {
		t.Fatal("agent submission wrote a canonical decision")
	}
	if _, err := decisionlog.IssueAgentToken(root, decisionlog.AgentChatGPT); err != nil {
		t.Fatal(err)
	}
	if response := call(tokens[decisionlog.AgentChatGPT], "karte_search", map[string]any{"query": "テスト"}); response.Code != 401 {
		t.Fatal("rotated token still authenticated")
	}
	output.Reset()
	if err := server.Run(context.Background(), strings.NewReader(line), &output); err != nil ||
		!strings.Contains(output.String(), "isError") {
		t.Fatal("stdio session retained access after token rotation")
	}
}
