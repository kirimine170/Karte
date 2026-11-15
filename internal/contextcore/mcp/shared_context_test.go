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

	"karte/internal/canonical"
	"karte/internal/contextcore"
	"karte/internal/decisionlog"
)

func sharedFixture(t *testing.T) (string, map[string]string) {
	t.Helper()
	root := t.TempDir()
	policy := contextcore.DefaultPolicy()
	policy.Actors[decisionlog.AgentChatGPT] = contextcore.ActorPolicy{
		Projects: []string{"karte"}, SensitivityCeiling: "internal",
		ProvenanceTypes: []string{"canonical", "meeting-summary"},
		Capabilities: []contextcore.Capability{
			contextcore.CapabilitySearch, contextcore.CapabilityRead, contextcore.CapabilityPropose,
		},
	}
	policy.Actors[decisionlog.AgentGrok] = contextcore.ActorPolicy{
		Projects: []string{"hacksick"}, SensitivityCeiling: "public",
		ProvenanceTypes: []string{"canonical"},
		Capabilities: []contextcore.Capability{
			contextcore.CapabilitySearch, contextcore.CapabilityRead,
		},
	}
	raw, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	err = canonical.WithWriter(root, func(w *canonical.Writer) error {
		if err := w.Write(".mdsys/context/v1/policy.json", raw, 0600); err != nil {
			return err
		}
		docs := map[string]string{
			"content/projects/karte/note/2026-11/chatgpt.md": "---\ndoc_id: chatgpt-note\ntitle: ChatGPT permitted\nproject: karte\nkind: note\nsensitivity: internal\n---\n\nKARTE_VISIBLE_2717\n",
			"content/projects/hacksick/note/2026-11/grok.md": "---\ndoc_id: grok-note\ntitle: Grok permitted\nproject: hacksick\nkind: note\nsensitivity: public\n---\n\nGROK_VISIBLE_2717\n",
			"content/projects/karte/note/2026-11/secret.md": "---\ndoc_id: secret-note\ntitle: Private board\nproject: karte\nkind: note\nsensitivity: confidential\n---\n\nPRIVATE_2717\n",
			"content/projects/karte/note/2026-11/broken.md": "---\ntitle: malformed\nproject: karte\n---\n\nUNINDEXED_2717\n",
		}
		for path, value := range docs {
			if err := w.Write(path, []byte(value), 0600); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	tokens := map[string]string{}
	for _, actor := range []string{decisionlog.AgentChatGPT, decisionlog.AgentGrok} {
		token, err := decisionlog.IssueAgentToken(root, actor)
		if err != nil {
			t.Fatal(err)
		}
		tokens[actor] = token
	}
	return root, tokens
}

func sharedCall(t *testing.T, handler http.Handler, token, method string, params any) *httptest.ResponseRecorder {
	t.Helper()
	message := mcpLine(t, "request-2717", method, params)
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(message))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("MCP-Protocol-Version", protocolVersion)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	return resp
}

func TestSharedContextAgentsAreScopedAndLegacyModeIsDecisionOnly(t *testing.T) {
	root, tokens := sharedFixture(t)
	shared := agentHTTPHandlerWithContext(root, true)
	legacy := agentHTTPHandler(root)

	chat := tokens[decisionlog.AgentChatGPT]
	grok := tokens[decisionlog.AgentGrok]
	call := func(handler http.Handler, token, query string) *httptest.ResponseRecorder {
		return sharedCall(t, handler, token, "tools/call", map[string]any{
			"name": "karte_search", "arguments": map[string]any{"query": query},
		})
	}
	for _, tc := range []struct {
		token, allowed, denied string
	}{
		{chat, "KARTE_VISIBLE_2717", "GROK_VISIBLE_2717"},
		{grok, "GROK_VISIBLE_2717", "KARTE_VISIBLE_2717"},
	} {
		got := call(shared, tc.token, tc.allowed)
		if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), tc.allowed) {
			t.Fatalf("scoped search failed: %d %s", got.Code, got.Body.String())
		}
		for _, query := range []string{tc.denied, "PRIVATE_2717", "UNINDEXED_2717"} {
			denied := call(shared, tc.token, query)
			if denied.Code != http.StatusOK || strings.Contains(denied.Body.String(), "doc_id") ||
				strings.Contains(denied.Body.String(), query) || strings.Contains(denied.Body.String(), "missing_doc_id") {
				t.Fatalf("denied search revealed data or diagnostics: %d %s", denied.Code, denied.Body.String())
			}
		}
	}
	if result := call(legacy, chat, "KARTE_VISIBLE_2717"); result.Code != http.StatusOK ||
		strings.Contains(result.Body.String(), "KARTE_VISIBLE_2717") {
		t.Fatalf("decision-only mode unexpectedly exposed canonical Markdown: %s", result.Body.String())
	}

	read := func(token, id string) *httptest.ResponseRecorder {
		return sharedCall(t, shared, token, "tools/call", map[string]any{
			"name": "karte_read", "arguments": map[string]any{"doc_id": id},
		})
	}
	if visible := read(chat, "chatgpt-note"); visible.Code != 200 ||
		!strings.Contains(visible.Body.String(), "KARTE_VISIBLE_2717") {
		t.Fatalf("authorized read failed: %s", visible.Body.String())
	}
	for _, id := range []string{"grok-note", "secret-note", "missing-note"} {
		if denied := read(chat, id); denied.Code != 200 ||
			!strings.Contains(denied.Body.String(), "denied") ||
			strings.Contains(denied.Body.String(), "PRIVATE_2717") ||
			strings.Contains(denied.Body.String(), "missing_doc_id") {
			t.Fatalf("read authorization or diagnostics failed: %s", denied.Body.String())
		}
	}

	sharedTools := sharedCall(t, shared, chat, "tools/list", map[string]any{})
	legacyTools := sharedCall(t, legacy, chat, "tools/list", map[string]any{})
	if !strings.Contains(sharedTools.Body.String(), "Workspace Markdown") ||
		strings.Contains(legacyTools.Body.String(), "Workspace Markdown") ||
		!strings.Contains(sharedTools.Body.String(), "karte_propose_decision") {
		t.Fatal("shared and decision-only tool metadata were not separated")
	}
	if response := sharedCall(t, shared, "", "tools/list", map[string]any{}); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated discovery succeeded: %d", response.Code)
	}
	if response := sharedCall(t, shared, "forged", "tools/list", map[string]any{}); response.Code != http.StatusUnauthorized {
		t.Fatalf("forged token authenticated: %d", response.Code)
	}
	// STDIO through the private tunnel uses the identical actor policy.
	stdio, err := NewSharedContextAgentServer(root, chat)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	line := mcpLine(t, "stdio-2717", "tools/call", map[string]any{
		"name": "karte_read", "arguments": map[string]any{"doc_id": "chatgpt-note"},
	})
	if err := stdio.Run(context.Background(), strings.NewReader(line), &output); err != nil ||
		!strings.Contains(output.String(), "KARTE_VISIBLE_2717") {
		t.Fatalf("stdio shared context failed: %v %s", err, output.String())
	}
}

func TestSharedContextPolicyAndTokenChangesTakeEffectImmediately(t *testing.T) {
	root, tokens := sharedFixture(t)
	handler := agentHTTPHandlerWithContext(root, true)
	args := map[string]any{"name": "karte_read", "arguments": map[string]any{"doc_id": "chatgpt-note"}}
	token := tokens[decisionlog.AgentChatGPT]
	if got := sharedCall(t, handler, token, "tools/call", args); !strings.Contains(got.Body.String(), "KARTE_VISIBLE_2717") {
		t.Fatal("fixture did not allow initial read")
	}
	// Tightening the live policy must take effect without server restart.
	policy := contextcore.DefaultPolicy()
	policy.Actors[decisionlog.AgentChatGPT] = contextcore.ActorPolicy{
		Projects: []string{"karte"}, SensitivityCeiling: "public",
		Capabilities: []contextcore.Capability{contextcore.CapabilitySearch, contextcore.CapabilityRead},
	}
	raw, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := canonical.WithWriter(root, func(w *canonical.Writer) error {
		return w.Write(".mdsys/context/v1/policy.json", raw, 0600)
	}); err != nil {
		t.Fatal(err)
	}
	denied := sharedCall(t, handler, token, "tools/call", args)
	if denied.Code != http.StatusOK || !strings.Contains(denied.Body.String(), "denied") ||
		strings.Contains(denied.Body.String(), "KARTE_VISIBLE_2717") {
		t.Fatalf("live policy narrowing failed: %s", denied.Body.String())
	}
	// Token rotation revokes the old credential.
	next, err := decisionlog.IssueAgentToken(root, decisionlog.AgentChatGPT)
	if err != nil {
		t.Fatal(err)
	}
	if response := sharedCall(t, handler, token, "tools/list", map[string]any{}); response.Code != 401 {
		t.Fatalf("old rotated token still valid: %d", response.Code)
	}
	if response := sharedCall(t, handler, next, "tools/list", map[string]any{}); response.Code != 200 {
		t.Fatalf("new rotated token invalid: %d", response.Code)
	}
	// Removal of the authorization file fails closed, not to the DefaultPolicy.
	if err := os.Remove(filepath.Join(root, ".mdsys/context/v1/policy.json")); err != nil {
		t.Fatal(err)
	}
	if response := sharedCall(t, handler, next, "tools/list", map[string]any{}); response.Code != 401 {
		t.Fatalf("missing policy did not fail closed: %d", response.Code)
	}
}
