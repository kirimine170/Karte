package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"karte/internal/canonical"
	"karte/internal/contextcore"
	"karte/internal/decisionlog"
	"karte/internal/ephyoutbox"
	gitvcs "karte/internal/git"
)

type decisionAcceptanceCase struct {
	Settings     decisionlog.Settings `json:"settings"`
	DecidedQuery string               `json:"decided_query"`
	OpenBusiness string               `json:"open_business"`
	OpenMatch    string               `json:"open_match"`
}

func TestDecisionLogAcceptance(t *testing.T) {
	t.Run("fixture", func(t *testing.T) {
		runDecisionAcceptance(t, filepath.Join("testdata", "decision-log", "2026-11-12_fixture.txt"),
			"https://example.invalid/summary", decisionAcceptanceCase{
				Settings:     decisionlog.Settings{Businesses: map[string]string{"workshop": "工作室"}},
				DecidedQuery: "収益の柱", OpenBusiness: "workshop", OpenMatch: "利用時間",
			})
	})
	t.Run("meeting-room", func(t *testing.T) {
		path, url, config := os.Getenv("KARTE_DECISION_ACCEPTANCE_SUMMARY"), os.Getenv("KARTE_DECISION_ACCEPTANCE_SOURCE"), os.Getenv("KARTE_DECISION_ACCEPTANCE_CASE")
		if path == "" && url == "" && config == "" {
			t.Skip("set the three KARTE_DECISION_ACCEPTANCE_* variables")
		}
		if path == "" || url == "" || config == "" {
			t.Fatal("all three decision acceptance variables are required")
		}
		raw, err := os.ReadFile(config)
		if err != nil {
			t.Fatal(err)
		}
		var profile decisionAcceptanceCase
		if err := json.Unmarshal(raw, &profile); err != nil || profile.DecidedQuery == "" || profile.OpenBusiness == "" || profile.OpenMatch == "" {
			t.Fatal("invalid decision acceptance case")
		}
		runDecisionAcceptance(t, path, url, profile)
	})
}

func runDecisionAcceptance(t *testing.T, path, sourceURL string, profile decisionAcceptanceCase) {
	t.Helper()
	originalQuery := profile.DecidedQuery
	app, root := newEphyTestApp(t)
	settings, err := json.Marshal(profile.Settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := canonical.WithWriter(root, func(w *canonical.Writer) error {
		return w.Write(decisionlog.SettingsPath, settings, 0600)
	}); err != nil {
		t.Fatal(err)
	}
	store, err := contextcore.NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	processor, err := contextcore.NewProcessor(root)
	if err != nil {
		t.Fatal(err)
	}
	var requests int
	contextCall := func(actor contextcore.Actor, operation, query string, docID *string) *contextcore.Response {
		t.Helper()
		requests++
		request := contextcore.Request{
			ProtocolVersion: contextcore.ProtocolVersion, RequestID: fmt.Sprintf("decision-test-%d", requests), Operation: operation,
			Actor: actor, Scope: contextcore.Scope{Projects: []string{"*"}, SensitivityCeiling: "internal"},
			CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), DocID: docID,
		}
		if operation == "search" {
			request.Query = &contextcore.SearchQuery{Text: query, TopK: 20}
		}
		data, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ".mdsys", "context", "v1", "requests", request.RequestID+".json"), data, 0600); err != nil {
			t.Fatal(err)
		}
		result, err := processor.ProcessPending(1)
		if err != nil || result.Processed != 1 || result.Failed != 0 {
			t.Fatalf("Context processor: %v (%+v)", err, result)
		}
		response, err := store.ReadResponse(request.RequestID)
		if err != nil || response == nil {
			t.Fatalf("Context response unavailable: %v", err)
		}
		return response
	}

	first, err := app.importDecisionSummary(path, sourceURL)
	if err != nil || first.Added < 3 {
		t.Fatalf("initial import: %v (%+v)", err, first)
	}
	repeat, err := app.importDecisionSummary(path, sourceURL)
	if err != nil || repeat.Unchanged != first.Added || repeat.Added != 0 {
		t.Fatalf("repeated import: %v (%+v)", err, repeat)
	}
	mirrorURL := "https://example.invalid/second-summary"
	duplicate, err := app.importDecisionSummary(path, mirrorURL)
	if err != nil || duplicate.Merged != first.Added || duplicate.Added != 0 {
		t.Fatalf("duplicate across sources: %v (%+v)", err, duplicate)
	}
	if result := contextCall(localHumanActor, "search", originalQuery, nil); len(result.Results) != 0 {
		t.Fatal("pending decision was returned by search")
	}
	inbox, err := app.ListEphyProposals()
	if err != nil || len(inbox.Proposals) != first.Added {
		t.Fatalf("decision inbox unavailable: %v", err)
	}
	var original, undecided, rejected ephyoutbox.Proposal
	for _, review := range inbox.Proposals {
		proposal := review.Proposal
		record, err := decisionlog.DecodeProposal(proposal)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(record.Content, originalQuery) && record.Status == decisionlog.Decided {
			original = proposal
		} else if strings.Contains(record.Content, profile.OpenMatch) && record.Business == profile.OpenBusiness && record.Status == decisionlog.Open {
			undecided = proposal
		} else if rejected.CandidateID == "" {
			rejected = proposal
		}
	}
	if original.CandidateID == "" || undecided.CandidateID == "" || rejected.CandidateID == "" {
		t.Fatal("required decided and open candidates were not extracted")
	}

	// Force an interrupted receipt write and retry via the existing review route.
	app.ephyWriteReceipt = func(*ephyoutbox.Store, ephyoutbox.Receipt) error { return errors.New("receipt interrupted") }
	if _, err := app.AcceptEphyProposal(original.CandidateID, nil, original.ProposedBody); err == nil {
		t.Fatal("receipt failure did not surface")
	}
	app.ephyWriteReceipt = nil
	receipt, err := app.AcceptEphyProposal(original.CandidateID, nil, original.ProposedBody)
	if err != nil || receipt.Result != "accepted" || receipt.DocID == nil || *receipt.DocID != original.CandidateID {
		t.Fatalf("recovered acceptance: %v", err)
	}
	if receipt, err := app.AcceptEphyProposal(undecided.CandidateID, nil, undecided.ProposedBody); err != nil || receipt.Result != "accepted" {
		t.Fatalf("open acceptance: %v", err)
	}
	if receipt, err := app.RejectEphyProposal(rejected.CandidateID, "acceptance fixture"); err != nil || receipt.Result != "rejected" {
		t.Fatalf("reject: %v", err)
	}
	thirdURL := "https://example.invalid/third-summary"
	third, err := app.importDecisionSummary(path, thirdURL)
	if err != nil || third.Added != 0 || third.Merged < 2 {
		t.Fatalf("merging sources after adoption and rejection: %v (%+v)", err, third)
	}

	id := original.CandidateID
	result := contextCall(localHumanActor, "search", originalQuery, nil)
	if len(result.Results) != 1 || result.Results[0].DocID != id ||
		!slices.Contains(result.Results[0].Tags, "decision:decided") ||
		len(result.Results[0].Provenance) != 3 || result.Results[0].Provenance[0].Reference != sourceURL {
		t.Fatal("active decision was not returned with state and all sources")
	}
	open := contextCall(localHumanActor, "search", "未決 "+profile.Settings.Businesses[profile.OpenBusiness], nil)
	if len(open.Results) != 1 || open.Results[0].DocID != undecided.CandidateID || !strings.Contains(open.Results[0].Snippet, "未決") {
		t.Fatal("open decision missing")
	}
	if result := contextCall(ephyContextActor, "search", originalQuery, nil); len(result.Results) != 0 {
		t.Fatal("private decision surfaced in external search")
	}
	if result := contextCall(ephyContextActor, "read", "", &id); result.Status != "denied" || result.Document != nil || len(result.Diagnostics) != 0 {
		t.Fatal("private decision surfaced in external read")
	}
	if len(app.GetFileList()) != 0 {
		t.Fatal("decision files leaked into generic document list")
	}
	relative := decisionlog.Path(id)
	vcs, err := gitvcs.NewVCS(nil, root, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if err := vcs.CheckAutomaticCommit(relative); err == nil {
		t.Fatal("decision storage was eligible for an automatic Git commit")
	}
	if _, err := app.ExportPreviewHTML(relative); err == nil {
		t.Fatal("decision files were available to generic export")
	}
	for _, path := range []string{relative, decisionlog.SettingsPath, ".mdsys/ephy/outbox/accepted/" + id + ".json", ".mdsys/context/v1/responses/" + result.RequestID + ".json"} {
		asset := httptest.NewRecorder()
		app.createAssetHandler().ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "/pdf/"+path, nil))
		if asset.Code != http.StatusNotFound {
			t.Fatal("decision storage was available via asset handler")
		}
	}

	base, err := decisionlog.DecodeProposal(original)
	if err != nil {
		t.Fatal(err)
	}
	replacementPath := filepath.Join(t.TempDir(), "2026-11-13_まとめ.txt")
	content := "【" + base.DisplayBusiness() + "】【決定】" + originalQuery + "の扱いを更新する受け入れテスト用の候補．\n"
	if err := os.WriteFile(replacementPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	created, err := app.importDecisionSummary(replacementPath, "https://example.invalid/replacement")
	if err != nil || created.Added != 1 {
		t.Fatalf("replacement import: %v", err)
	}
	inbox, err = app.ListEphyProposals()
	if err != nil {
		t.Fatal(err)
	}
	var newID string
	for _, review := range inbox.Proposals {
		proposal := review.Proposal
		replacement, err := decisionlog.DecodeProposal(proposal)
		if err != nil {
			t.Fatal(err)
		}
		if replacement.Date != "2026-11-13" {
			continue
		}
		replacement.Supersedes = id
		body, err := json.Marshal(replacement)
		if err != nil {
			t.Fatal(err)
		}
		receipt, err := app.AcceptEphyProposal(proposal.CandidateID, nil, string(body))
		if err != nil || receipt.Result != "accepted" {
			t.Fatalf("replacement acceptance: %v", err)
		}
		newID = replacement.ID
	}
	if newID == "" {
		t.Fatal("no replacement candidate was accepted")
	}

	// Recreate the application and processor against the same persisted workspace.
	app = NewApp()
	app.dataDir = root
	processor, err = contextcore.NewProcessor(root)
	if err != nil {
		t.Fatal(err)
	}
	if result := contextCall(localHumanActor, "search", originalQuery, nil); len(result.Results) != 1 || result.Results[0].DocID != newID {
		t.Fatal("search after restart included an obsolete decision or missed its replacement")
	}
	if history := contextCall(localHumanActor, "search", "置換 "+originalQuery, nil); len(history.Results) != 1 || history.Results[0].DocID != id {
		t.Fatal("superseded decision missing from history")
	}
	if read := contextCall(localHumanActor, "read", "", &id); read.Document == nil ||
		!slices.Contains(read.Document.Tags, "decision:superseded") || !strings.Contains(read.Document.Body, newID) ||
		read.Document.SHA256 == result.Results[0].SHA256 {
		t.Fatal("old ID did not resolve as history with a replacement link")
	}
	if err := app.WithdrawDecision(newID); err != nil {
		t.Fatal(err)
	}
	if result := contextCall(localHumanActor, "search", originalQuery, nil); len(result.Results) != 0 {
		t.Fatal("withdrawing a replacement resurrected the old decision")
	}
	if history := contextCall(localHumanActor, "search", "撤回 "+originalQuery, nil); len(history.Results) != 1 || history.Results[0].DocID != newID {
		t.Fatal("withdrawn decision missing from history")
	}
}

func TestPublicDecisionContext(t *testing.T) {
	_, root := newEphyTestApp(t)
	records, err := (decisionlog.Settings{}).ExtractSummary("2026-11-12_fixture", []byte("【Karte】【決定】公開可能なチームAとチームBのテスト．"), "https://example.invalid/public", time.Now())
	if err != nil || len(records) != 1 {
		t.Fatalf("public fixture: %v", err)
	}
	record := records[0]
	record.Visibility = decisionlog.Public
	if err := canonical.WithWriter(root, func(w *canonical.Writer) error {
		_, err := decisionlog.Accept(w, record)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	service, err := contextcore.NewService(root)
	if err != nil {
		t.Fatal(err)
	}
	request := contextcore.Request{
		ProtocolVersion: contextcore.ProtocolVersion, RequestID: "public-decision", Operation: "search",
		Actor: ephyContextActor, Scope: contextcore.Scope{Projects: []string{"karte"}, SensitivityCeiling: "public"},
		Query: &contextcore.SearchQuery{Text: "公開可能", TopK: 10}, CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	results, _, status, err := service.Search(request, contextcore.DefaultPolicy())
	if err != nil || status != "ok" || len(results) != 1 || results[0].DocID != record.ID {
		t.Fatalf("public decision missing from external Context: %s %v", status, err)
	}
	if err := canonical.WithWriter(root, func(w *canonical.Writer) error {
		return w.Write(decisionlog.SettingsPath, []byte(`{"private_together":[["チームA","チームB"]]}`), 0600)
	}); err != nil {
		t.Fatal(err)
	}
	results, _, status, err = service.Search(request, contextcore.DefaultPolicy())
	if err != nil || status != "ok" || len(results) != 0 {
		t.Fatalf("new workspace restriction failed to suppress the decision: %s %v", status, err)
	}
}
