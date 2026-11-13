package contextcore

import (
	"testing"
	"time"

	"karte/internal/canonical"
	"karte/internal/decisionlog"
)

func TestDecisionRecipientSearchReadAndExport(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	records, err := (decisionlog.Settings{}).ExtractSummary("2026-11-12_fixture.txt", []byte(
		"【Karte】【決定】検証記録Grok。\n【Karte】【決定】検証記録ChatGPT。\n【Karte】【決定】検証記録非公開。\n"+
			"【Karte】【決定】検証記録公開。\n【HSAP】【決定】検証記録甲。\n【ESSEN】【決定】検証記録乙。"),
		"https://example.invalid/summary", now)
	if err != nil || len(records) != 6 {
		t.Fatalf("extract decisions: %v", err)
	}
	tags := [][]string{{decisionlog.AgentGrok}, {decisionlog.AgentChatGPT}, nil,
		{decisionlog.Public}, {decisionlog.Public}, {decisionlog.AgentChatGPT}}
	err = canonical.WithWriter(root, func(w *canonical.Writer) error {
		if err := decisionlog.InitializeSettings(w); err != nil {
			return err
		}
		for i, record := range records {
			record.VisibilityTags = tags[i]
			if _, err := decisionlog.Accept(w, record); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(root)
	if err != nil {
		t.Fatal(err)
	}
	policy := DefaultPolicy()
	grant := ActorPolicy{SensitivityCeiling: "internal", Projects: []string{"*"}, Capabilities: []Capability{CapabilitySearch, CapabilityRead}}
	policy.Actors[decisionlog.AgentChatGPT] = grant
	policy.Actors["unknown"] = grant
	request := Request{
		ProtocolVersion: ProtocolVersion, RequestID: "decision-visibility", Operation: "search",
		Actor: Actor{Type: "tool", ID: decisionlog.AgentChatGPT}, Scope: Scope{SensitivityCeiling: "internal"},
		CreatedAt: now.Format(time.RFC3339), Query: &SearchQuery{Text: "検証記録", TopK: 20},
	}
	agent := service.WithDecisionRecipient(decisionlog.AgentChatGPT)
	results, _, status, err := agent.Search(request, policy)
	if err != nil || status != "ok" || len(results) != 2 {
		t.Fatalf("recipient search status %s, results %d: %v", status, len(results), err)
	}
	for _, result := range results {
		if result.DocID != records[1].ID && result.DocID != records[3].ID {
			t.Fatal("recipient search included a hidden decision")
		}
	}
	if results, _, _, err := service.Search(request, policy); err != nil || len(results) != 0 {
		t.Fatal("self-asserted recipient gained access")
	}
	request.Actor.ID = "unknown"
	if results, _, _, err := agent.Search(request, policy); err != nil || len(results) != 0 {
		t.Fatal("unknown recipient gained access")
	}
	request.Actor.ID = decisionlog.AgentChatGPT
	request.Operation, request.Query = "read", nil
	for i, record := range records {
		request.DocID = &record.ID
		result, _, status, err := agent.Read(request, policy)
		if err != nil || (status == "ok" && result != nil) != (i == 1 || i == 3) {
			t.Fatalf("recipient read for fixture %d returned %s: %v", i, status, err)
		}
	}
	human := Actor{Type: "human", ID: "local-human"}
	exported, err := service.ExportDecisions(human, policy, decisionlog.AgentChatGPT)
	if err != nil || len(exported) != 2 {
		t.Fatalf("recipient export returned %d: %v", len(exported), err)
	}
	for _, result := range exported {
		if result.DocID != records[1].ID && result.DocID != records[3].ID {
			t.Fatal("recipient export included a hidden decision")
		}
	}
	public, err := service.ExportDecisions(human, policy, decisionlog.Public)
	if err != nil || len(public) != 1 || public[0].DocID != records[3].ID {
		t.Fatal("public export disclosed a restricted record or lost a public one")
	}
}
