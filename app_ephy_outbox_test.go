package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"karte/internal/contextcore"
	"karte/internal/ephyoutbox"
)

func newEphyTestApp(t *testing.T) (*App, string) {
	t.Helper()
	dataRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataRoot, "content"), 0o755); err != nil {
		t.Fatal(err)
	}
	app := NewApp()
	app.root = dataRoot
	app.dataDir = dataRoot
	return app, dataRoot
}

func readProposalFixture(t *testing.T, name string) ([]byte, ephyoutbox.Proposal) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("schemas", "karte-ephy", "v1", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := ephyoutbox.DecodeProposal(data)
	if err != nil {
		t.Fatal(err)
	}
	return data, proposal
}

func writePendingPayload(t *testing.T, dataRoot string, data []byte) ephyoutbox.Proposal {
	t.Helper()
	proposal, err := ephyoutbox.DecodeProposal(data)
	if err != nil {
		t.Fatal(err)
	}
	pending := filepath.Join(dataRoot, ".mdsys", "ephy", "outbox", "pending")
	if err := os.MkdirAll(pending, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pending, proposal.CandidateID+".json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return proposal
}

func writePendingFixture(t *testing.T, dataRoot, name string) ephyoutbox.Proposal {
	t.Helper()
	data, _ := readProposalFixture(t, name)
	return writePendingPayload(t, dataRoot, data)
}

func writeAppendTarget(t *testing.T, dataRoot string) (string, []byte) {
	t.Helper()
	relativePath := "content/projects/ephy/note/2026-09/existing.md"
	canonical := []byte("---\ntitle: Existing\ntags: fixture, reviewed\ndoc_id: doc:synthetic-001\nproject: ephy\nkind: note\n---\n# Existing\n\nCanonical body.\n")
	abs := filepath.Join(dataRoot, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, canonical, 0o644); err != nil {
		t.Fatal(err)
	}
	return abs, canonical
}

func appendProposalWithHash(t *testing.T, canonical []byte) []byte {
	t.Helper()
	fixture, _ := readProposalFixture(t, "append-proposal.json")
	var payload map[string]any
	if err := json.Unmarshal(fixture, &payload); err != nil {
		t.Fatal(err)
	}
	payload["base_sha256"] = ephyoutbox.SHA256Bytes(canonical)
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestListEphyProposalsIsReadOnlyAndShowsResolvedPlacement(t *testing.T) {
	app, dataRoot := newEphyTestApp(t)
	proposal := writePendingFixture(t, dataRoot, "create-proposal.json")
	inbox, err := app.ListEphyProposals()
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox.Proposals) != 1 || len(inbox.Errors) != 0 {
		t.Fatalf("unexpected inbox: %#v", inbox)
	}
	review := inbox.Proposals[0]
	wantPath := "content/projects/ephy/decision/2026-09/synthetic-placement-decision.md"
	if review.Proposal.CandidateID != proposal.CandidateID || review.ResolvedRelativePath != wantPath || review.ResolvedDocID == "" {
		t.Fatalf("review placement is incomplete: %#v", review)
	}
	if review.Diff == "" || !strings.Contains(review.ProposedContent, proposal.ProposedBody) || !strings.Contains(review.RoutingReason, "project=ephy") {
		t.Fatalf("review context is incomplete: %#v", review)
	}
	if _, err := os.Stat(filepath.Join(dataRoot, filepath.FromSlash(wantPath))); !os.IsNotExist(err) {
		t.Fatal("listing proposals changed canonical content")
	}
}

func TestAcceptEphyCreateUsesSaveFileAndRecoversReceiptRetry(t *testing.T) {
	app, dataRoot := newEphyTestApp(t)
	proposal := writePendingFixture(t, dataRoot, "create-proposal.json")
	inbox, err := app.ListEphyProposals()
	if err != nil {
		t.Fatal(err)
	}
	canonicalPath := filepath.Join(dataRoot, filepath.FromSlash(inbox.Proposals[0].ResolvedRelativePath))
	saveCalls := 0
	app.ephySaveFile = func(path, content string) error {
		saveCalls++
		return app.SaveFile(path, content)
	}
	receiptCalls := 0
	app.ephyWriteReceipt = func(store *ephyoutbox.Store, receipt ephyoutbox.Receipt) error {
		receiptCalls++
		if receiptCalls == 1 {
			return errors.New("synthetic receipt failure")
		}
		return store.WriteReceipt(receipt)
	}
	editedFrontmatter := map[string]any{"title": "Edited synthetic memory", "tags": "ephy, reviewed"}
	editedBody := "# Edited synthetic memory\n\nReviewed before acceptance."
	if _, err := app.AcceptEphyProposal(proposal.CandidateID, editedFrontmatter, editedBody); err == nil {
		t.Fatal("expected first receipt attempt to fail")
	}
	firstSaved, err := os.ReadFile(canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := app.AcceptEphyProposal(proposal.CandidateID, editedFrontmatter, editedBody)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Result != "accepted" || receipt.DocID == nil || receipt.RelativePath == nil || saveCalls != 1 {
		t.Fatalf("unexpected receipt or save count: %#v calls=%d", receipt, saveCalls)
	}
	secondSaved, err := os.ReadFile(canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstSaved) != string(secondSaved) || !strings.Contains(string(secondSaved), "Reviewed before acceptance.") {
		t.Fatal("receipt retry changed or lost canonical Markdown")
	}
}

func TestAcceptEphyCreatePreservesYAMLListTags(t *testing.T) {
	app, dataRoot := newEphyTestApp(t)
	fixture, _ := readProposalFixture(t, "create-proposal.json")
	var payload map[string]any
	if err := json.Unmarshal(fixture, &payload); err != nil {
		t.Fatal(err)
	}
	frontmatter, ok := payload["proposed_frontmatter"].(map[string]any)
	if !ok {
		t.Fatal("fixture proposed_frontmatter is not a mapping")
	}
	frontmatter["tags"] = []any{"e2e", "karte-integration", "e2e"}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	proposal := writePendingPayload(t, dataRoot, encoded)
	receipt, err := app.AcceptEphyProposal(proposal.CandidateID, proposal.ProposedFrontmatter, proposal.ProposedBody)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.RelativePath == nil {
		t.Fatal("accepted receipt has no relative path")
	}
	canonical, err := os.ReadFile(filepath.Join(dataRoot, filepath.FromSlash(*receipt.RelativePath)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(canonical), `tags: "e2e, karte-integration"`) {
		t.Fatalf("accepted canonical Markdown lost YAML list tags: %s", canonical)
	}
}

func TestProposalAuthorizationAuditsListAndAcceptSeparately(t *testing.T) {
	app, dataRoot := newEphyTestApp(t)
	proposal := writePendingFixture(t, dataRoot, "create-proposal.json")
	if _, err := app.ListEphyProposals(); err != nil {
		t.Fatal(err)
	}
	if _, err := app.AcceptEphyProposal(proposal.CandidateID, proposal.ProposedFrontmatter, proposal.ProposedBody); err != nil {
		t.Fatal(err)
	}
	auditFiles, err := filepath.Glob(filepath.Join(dataRoot, ".mdsys", "context", "v1", "audit", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(auditFiles) != 4 {
		t.Fatalf("list and accept authorization audits were deduplicated: files=%#v", auditFiles)
	}
	correlations := map[string]bool{}
	actorOperations := map[string]int{}
	for _, path := range auditFiles {
		encoded, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		var event contextcore.AuditEvent
		if err := json.Unmarshal(encoded, &event); err != nil {
			t.Fatal(err)
		}
		correlations[event.CorrelationSHA256] = true
		actorOperations[event.ActorType+":"+event.Operation]++
	}
	if len(correlations) != 4 || actorOperations["ephy:propose"] != 2 || actorOperations["human:review"] != 2 {
		t.Fatalf("authorization audit phases are incomplete: correlations=%d actorOperations=%#v", len(correlations), actorOperations)
	}
}

func TestCreateProposalDeduplicatesSourceProvenanceTypes(t *testing.T) {
	app, dataRoot := newEphyTestApp(t)
	fixture, _ := readProposalFixture(t, "create-proposal.json")
	var payload map[string]any
	if err := json.Unmarshal(fixture, &payload); err != nil {
		t.Fatal(err)
	}
	sourceRefs := payload["source_refs"].([]any)
	payload["source_refs"] = append(sourceRefs, map[string]any{
		"type": "synthetic-test", "reference": "fixture://conversation/duplicate-type",
	})
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	proposal := writePendingPayload(t, dataRoot, encoded)
	inbox, err := app.ListEphyProposals()
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox.Proposals) != 1 || len(inbox.Errors) != 0 {
		t.Fatalf("duplicate provenance type made proposal unreviewable: %#v", inbox)
	}
	if _, err := app.AcceptEphyProposal(proposal.CandidateID, proposal.ProposedFrontmatter, proposal.ProposedBody); err != nil {
		t.Fatalf("duplicate provenance type blocked acceptance: %v", err)
	}
}

func TestAcceptedEphyDocumentSurvivesRestartAndContextSearchRead(t *testing.T) {
	app, dataRoot := newEphyTestApp(t)
	proposal := writePendingFixture(t, dataRoot, "create-proposal.json")
	receipt, err := app.AcceptEphyProposal(proposal.CandidateID, proposal.ProposedFrontmatter, proposal.ProposedBody)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.DocID == nil || receipt.RelativePath == nil || receipt.ResultingSHA256 == nil {
		t.Fatalf("accepted receipt is incomplete: %#v", receipt)
	}
	canonical, err := os.ReadFile(filepath.Join(dataRoot, filepath.FromSlash(*receipt.RelativePath)))
	if err != nil {
		t.Fatal(err)
	}
	if ephyoutbox.SHA256Bytes(canonical) != *receipt.ResultingSHA256 {
		t.Fatal("receipt hash does not match persisted Markdown")
	}

	// A new App and Processor model a Karte restart: no in-memory state from the
	// accepting instance is reused.
	restarted := NewApp()
	restarted.root = dataRoot
	restarted.dataDir = dataRoot
	processor, err := contextcore.NewProcessor(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	restarted.contextProcessor = processor

	search := contextcore.Request{
		ProtocolVersion: contextcore.ProtocolVersion,
		RequestID:       "accepted-restart-search",
		Operation:       "search",
		Actor:           contextcore.Actor{Type: "ephy", ID: "ephy"},
		Scope:           contextcore.Scope{Projects: []string{"ephy"}, SensitivityCeiling: "internal"},
		Query:           &contextcore.SearchQuery{Text: "synthetic placement decision", TopK: 5},
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
	}
	writeContextRequest(t, dataRoot, search)
	summary, err := restarted.ProcessContextRequests()
	if err != nil {
		t.Fatal(err)
	}
	if summary.Processed != 1 || summary.Failed != 0 {
		t.Fatalf("search was not processed after restart: %#v", summary)
	}
	searchResponse := readContextResponse(t, dataRoot, search.RequestID)
	if searchResponse.Status != "ok" || len(searchResponse.Results) != 1 || searchResponse.Results[0].DocID != *receipt.DocID {
		t.Fatalf("accepted document was not searchable after restart: %#v", searchResponse)
	}

	read := contextcore.Request{
		ProtocolVersion: contextcore.ProtocolVersion,
		RequestID:       "accepted-restart-read",
		Operation:       "read",
		Actor:           contextcore.Actor{Type: "ephy", ID: "ephy"},
		Scope:           contextcore.Scope{Projects: []string{"ephy"}, SensitivityCeiling: "internal"},
		DocID:           receipt.DocID,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
	}
	writeContextRequest(t, dataRoot, read)
	summary, err = restarted.ProcessContextRequests()
	if err != nil {
		t.Fatal(err)
	}
	if summary.Processed != 1 || summary.Failed != 0 {
		t.Fatalf("read was not processed after restart: %#v", summary)
	}
	readResponse := readContextResponse(t, dataRoot, read.RequestID)
	if readResponse.Status != "ok" || readResponse.Document == nil {
		t.Fatalf("accepted document was not readable after restart: %#v", readResponse)
	}
	if readResponse.Document.DocID != *receipt.DocID || readResponse.Document.RelativePath != *receipt.RelativePath ||
		readResponse.Document.SHA256 != *receipt.ResultingSHA256 || !strings.Contains(readResponse.Document.Body, proposal.ProposedBody) {
		t.Fatalf("readback does not match accepted Markdown and receipt: %#v", readResponse.Document)
	}
}

func writeContextRequest(t *testing.T, dataRoot string, request contextcore.Request) {
	t.Helper()
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	requestsDir := filepath.Join(dataRoot, ".mdsys", "context", "v1", "requests")
	if err := os.MkdirAll(requestsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(requestsDir, request.RequestID+".json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readContextResponse(t *testing.T, dataRoot, requestID string) contextcore.Response {
	t.Helper()
	path := filepath.Join(dataRoot, ".mdsys", "context", "v1", "responses", requestID+".json")
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var response contextcore.Response
	if err := json.Unmarshal(encoded, &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestAcceptEphyProposalRecoversCrashAfterCanonicalSave(t *testing.T) {
	app, dataRoot := newEphyTestApp(t)
	proposal := writePendingFixture(t, dataRoot, "create-proposal.json")
	docID, err := ephyoutbox.DeriveCreateDocID(proposal.CandidateID)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := ephyoutbox.ResolvePlacement(dataRoot, proposal, docID)
	if err != nil {
		t.Fatal(err)
	}
	frontmatter := cloneStringMap(proposal.ProposedFrontmatter)
	frontmatter["doc_id"], frontmatter["project"], frontmatter["kind"] = docID, proposal.Placement.Project, proposal.Placement.Kind
	prepared, err := renderEphyProposalContent(frontmatter, proposal.ProposedBody)
	if err != nil {
		t.Fatal(err)
	}
	canonicalPath := filepath.Join(dataRoot, filepath.FromSlash(decision.RelativePath))
	if err := os.MkdirAll(filepath.Dir(canonicalPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(canonicalPath, []byte(prepared), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := ephyoutbox.NewStore(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	transaction := ephyoutbox.Transaction{
		SchemaVersion: ephyoutbox.SchemaVersion, CandidateID: proposal.CandidateID,
		RelativePath: decision.RelativePath, DocID: docID, PreparedContent: prepared,
		State: "prepared", StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := store.WriteTransaction(transaction); err != nil {
		t.Fatal(err)
	}
	app.ephySaveFile = func(path, content string) error {
		t.Fatal("crash recovery must not invoke SaveFile twice")
		return nil
	}
	receipt, err := app.AcceptEphyProposal(proposal.CandidateID, proposal.ProposedFrontmatter, proposal.ProposedBody)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Result != "accepted" || receipt.ResultingSHA256 == nil || *receipt.ResultingSHA256 != ephyoutbox.SHA256Bytes([]byte(prepared)) {
		t.Fatalf("unexpected recovered receipt: %#v", receipt)
	}
}

func TestAcceptEphyAppendUsesMatchingDocIDContentAndByteHash(t *testing.T) {
	app, dataRoot := newEphyTestApp(t)
	canonicalPath, canonical := writeAppendTarget(t, dataRoot)
	proposal := writePendingPayload(t, dataRoot, appendProposalWithHash(t, canonical))
	receipt, err := app.AcceptEphyProposal(proposal.CandidateID, proposal.ProposedFrontmatter, proposal.ProposedBody)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Result != "accepted" || receipt.DocID == nil || *receipt.DocID != "doc:synthetic-001" {
		t.Fatalf("unexpected append receipt: %#v", receipt)
	}
	updated, err := os.ReadFile(canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(updated)
	if !strings.Contains(text, "Canonical body.") || !strings.Contains(text, "Reviewed addition") || !strings.Contains(text, `doc_id: "doc:synthetic-001"`) {
		t.Fatalf("canonical append is incorrect: %s", text)
	}
}

func TestAppendProposalDeduplicatesCanonicalProvenanceType(t *testing.T) {
	app, dataRoot := newEphyTestApp(t)
	_, canonical := writeAppendTarget(t, dataRoot)
	payload := appendProposalWithHash(t, canonical)
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	sourceRefs := decoded["source_refs"].([]any)
	decoded["source_refs"] = append(sourceRefs, map[string]any{
		"type": "canonical", "reference": "fixture://canonical/reference",
	})
	encoded, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	proposal := writePendingPayload(t, dataRoot, encoded)
	if _, err := app.AcceptEphyProposal(proposal.CandidateID, proposal.ProposedFrontmatter, proposal.ProposedBody); err != nil {
		t.Fatalf("canonical provenance type blocked append acceptance: %v", err)
	}
}

func TestAcceptEphyAppendStopsOnStaleBaseHash(t *testing.T) {
	app, dataRoot := newEphyTestApp(t)
	canonicalPath, canonical := writeAppendTarget(t, dataRoot)
	proposal := writePendingFixture(t, dataRoot, "append-proposal.json")
	saveCalls := 0
	app.ephySaveFile = func(path, content string) error {
		saveCalls++
		return app.SaveFile(path, content)
	}
	receipt, err := app.AcceptEphyProposal(proposal.CandidateID, proposal.ProposedFrontmatter, proposal.ProposedBody)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Result != "conflict" || receipt.ErrorCode == nil || *receipt.ErrorCode != "stale_base_sha256" || saveCalls != 0 {
		t.Fatalf("unexpected stale result: %#v calls=%d", receipt, saveCalls)
	}
	current, err := os.ReadFile(canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != string(canonical) {
		t.Fatal("stale append changed canonical content")
	}
}

func TestListEphyAppendRejectsProjectOrKindMismatch(t *testing.T) {
	app, dataRoot := newEphyTestApp(t)
	_, canonical := writeAppendTarget(t, dataRoot)
	data := appendProposalWithHash(t, canonical)
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	placement := payload["placement"].(map[string]any)
	placement["project"] = "master"
	placement["candidates"] = []any{map[string]any{"project": "master", "kind": "note", "confidence": 0.97, "reason": "Synthetic mismatch."}}
	data, _ = json.Marshal(payload)
	writePendingPayload(t, dataRoot, data)
	inbox, err := app.ListEphyProposals()
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox.Proposals) != 0 || len(inbox.Errors) != 1 || inbox.Errors[0].Code != "proposal_policy_denied" || strings.Contains(inbox.Errors[0].Message, "master") {
		t.Fatalf("content mismatch was not rejected: %#v", inbox)
	}
}

func TestProposalPolicyDenialDoesNotExposeRestrictedPayload(t *testing.T) {
	app, dataRoot := newEphyTestApp(t)
	fixture, _ := readProposalFixture(t, "create-proposal.json")
	var payload map[string]any
	if err := json.Unmarshal(fixture, &payload); err != nil {
		t.Fatal(err)
	}
	payload["sensitivity"] = "restricted"
	payload["proposed_body"] = "Restricted proposal body must stay hidden."
	frontmatter := payload["proposed_frontmatter"].(map[string]any)
	frontmatter["title"] = "Restricted proposal title"
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	writePendingPayload(t, dataRoot, encoded)
	inbox, err := app.ListEphyProposals()
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox.Proposals) != 0 || len(inbox.Errors) != 1 || inbox.Errors[0].Code != "proposal_policy_denied" {
		t.Fatalf("restricted proposal was exposed: %#v", inbox)
	}
	serialized, err := json.Marshal(inbox)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"Restricted proposal body", "Restricted proposal title", "synthetic-placement-decision.md"} {
		if strings.Contains(string(serialized), secret) {
			t.Fatalf("denied proposal disclosed %q: %s", secret, serialized)
		}
	}
	proposal, err := ephyoutbox.DecodeProposal(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.RejectEphyProposal(proposal.CandidateID, "deny"); err == nil || !strings.Contains(err.Error(), "policy") {
		t.Fatalf("restricted proposal was directly rejected outside policy: %v", err)
	}
	if store, err := ephyoutbox.NewStore(dataRoot); err != nil {
		t.Fatal(err)
	} else if existing, readErr := store.ReadReceipt(proposal.CandidateID); readErr != nil || existing != nil {
		t.Fatalf("denied rejection wrote a receipt: receipt=%#v err=%v", existing, readErr)
	}
}

func TestAppendCannotDowngradeCanonicalSensitivity(t *testing.T) {
	app, dataRoot := newEphyTestApp(t)
	canonicalPath, canonical := writeAppendTarget(t, dataRoot)
	canonical = []byte(strings.Replace(string(canonical), "kind: note\n", "kind: note\nsensitivity: confidential\n", 1))
	if err := os.WriteFile(canonicalPath, canonical, 0o644); err != nil {
		t.Fatal(err)
	}
	payload := appendProposalWithHash(t, canonical)
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	decoded["sensitivity"] = "internal"
	patch := decoded["proposed_frontmatter"].(map[string]any)
	patch["sensitivity"] = "internal"
	encoded, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	proposal := writePendingPayload(t, dataRoot, encoded)
	if _, err := app.AcceptEphyProposal(proposal.CandidateID, proposal.ProposedFrontmatter, proposal.ProposedBody); err == nil || !strings.Contains(err.Error(), "policy") {
		t.Fatalf("sensitivity downgrade was not denied: %v", err)
	}
	current, err := os.ReadFile(canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != string(canonical) {
		t.Fatal("denied sensitivity downgrade changed canonical content")
	}
}

func TestAppendCannotReplaceDeniedCanonicalTagBeforeAuthorization(t *testing.T) {
	app, dataRoot := newEphyTestApp(t)
	canonicalPath, canonical := writeAppendTarget(t, dataRoot)
	canonical = []byte(strings.Replace(string(canonical), "tags: fixture, reviewed", "tags: do-not-share", 1))
	if err := os.WriteFile(canonicalPath, canonical, 0o600); err != nil {
		t.Fatal(err)
	}
	policyDir := filepath.Join(dataRoot, ".mdsys", "context", "v1")
	if err := os.MkdirAll(policyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	policy := `{"protocol_version":"1.0","actors":{"ephy":{"sensitivity_ceiling":"internal","projects":["*"],"denied_tags":["do-not-share"],"provenance_types":["*"],"capabilities":["propose"]},"human":{"sensitivity_ceiling":"restricted","projects":["*"],"provenance_types":["*"],"capabilities":["review"]}}}`
	if err := os.WriteFile(filepath.Join(policyDir, "policy.json"), []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	proposal := writePendingPayload(t, dataRoot, appendProposalWithHash(t, canonical))
	inbox, err := app.ListEphyProposals()
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox.Proposals) != 0 || len(inbox.Errors) != 1 || inbox.Errors[0].Code != "proposal_policy_denied" {
		t.Fatalf("denied canonical tag was bypassed by the append patch: %#v", inbox)
	}
	if _, err := app.AcceptEphyProposal(proposal.CandidateID, proposal.ProposedFrontmatter, proposal.ProposedBody); err == nil || !strings.Contains(err.Error(), "policy") {
		t.Fatalf("append with a denied canonical tag reached acceptance: %v", err)
	}
	current, err := os.ReadFile(canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != string(canonical) {
		t.Fatal("denied append changed canonical content")
	}
}

func TestAppendClassificationPatchMustMatchPathDerivedClassification(t *testing.T) {
	for key, value := range map[string]string{"project": "master", "kind": "decision"} {
		t.Run(key, func(t *testing.T) {
			app, dataRoot := newEphyTestApp(t)
			canonicalPath, canonical := writeAppendTarget(t, dataRoot)
			canonical = []byte(strings.Replace(strings.Replace(string(canonical), "project: ephy\n", "", 1), "kind: note\n", "", 1))
			if err := os.WriteFile(canonicalPath, canonical, 0o600); err != nil {
				t.Fatal(err)
			}
			payload := appendProposalWithHash(t, canonical)
			var decoded map[string]any
			if err := json.Unmarshal(payload, &decoded); err != nil {
				t.Fatal(err)
			}
			decoded["proposed_frontmatter"].(map[string]any)[key] = value
			encoded, err := json.Marshal(decoded)
			if err != nil {
				t.Fatal(err)
			}
			proposal := writePendingPayload(t, dataRoot, encoded)
			inbox, err := app.ListEphyProposals()
			if err != nil {
				t.Fatal(err)
			}
			if len(inbox.Proposals) != 0 || len(inbox.Errors) != 1 || inbox.Errors[0].Code != "proposal_policy_denied" {
				t.Fatalf("path-derived %s classification was bypassed: %#v", key, inbox)
			}
			if _, err := app.AcceptEphyProposal(proposal.CandidateID, proposal.ProposedFrontmatter, proposal.ProposedBody); err == nil || !strings.Contains(err.Error(), "policy") {
				t.Fatalf("mismatched path-derived %s reached acceptance: %v", key, err)
			}
			current, err := os.ReadFile(canonicalPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(current) != string(canonical) {
				t.Fatalf("denied %s patch changed canonical content", key)
			}
		})
	}
}

func TestDocumentExportUsesPolicyAndAuditDoesNotStorePath(t *testing.T) {
	app, dataRoot := newEphyTestApp(t)
	relativePath := "content/projects/private/note/2026-09/restricted-export-secret.md"
	canonical := `---
title: "Restricted export title"
tags: person:secret-export
doc_id: "doc:restricted-export"
project: private
kind: note
sensitivity: restricted
---
Restricted export body.
`
	absPath := filepath.Join(dataRoot, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absPath, []byte(canonical), 0o600); err != nil {
		t.Fatal(err)
	}
	policyDir := filepath.Join(dataRoot, ".mdsys", "context", "v1")
	if err := os.MkdirAll(policyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	policy := `{"protocol_version":"1.0","actors":{"human":{"sensitivity_ceiling":"internal","projects":["*"],"provenance_types":["canonical"],"capabilities":["review","export"]}}}`
	if err := os.WriteFile(filepath.Join(policyDir, "policy.json"), []byte(policy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := app.authorizeDocumentExport(relativePath); err == nil {
		t.Fatal("restricted export was allowed above the human ceiling")
	}
	auditFiles, err := filepath.Glob(filepath.Join(policyDir, "audit", "*.json"))
	if err != nil || len(auditFiles) != 1 {
		t.Fatalf("export audit is missing: files=%#v err=%v", auditFiles, err)
	}
	auditData, err := os.ReadFile(auditFiles[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{relativePath, "Restricted export title", "person:secret-export", "doc:restricted-export", "Restricted export body"} {
		if strings.Contains(string(auditData), secret) {
			t.Fatalf("export audit disclosed %q: %s", secret, auditData)
		}
	}
}

func TestContextGraphAndHTMLExportLogsDoNotPersistContentIdentifiers(t *testing.T) {
	app, dataRoot := newEphyTestApp(t)
	app.ctx = nil
	relativePath := "content/projects/ephy/note/2026-09/private-log-path.md"
	canonical := `---
title: "Private log title"
tags: person:private-log-person
doc_id: "doc:private-log-id"
project: ephy
kind: note
sensitivity: internal
---
Private log body payload.
`
	absPath := filepath.Join(dataRoot, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absPath, []byte(canonical), 0o600); err != nil {
		t.Fatal(err)
	}
	app.logFilePath = filepath.Join(dataRoot, "app.log")
	if _, err := app.GetGraphData(); err != nil {
		t.Fatal(err)
	}
	exportedURL, err := app.ExportPreviewHTML(relativePath)
	if err != nil {
		t.Fatal(err)
	}
	exportedPath := strings.TrimPrefix(exportedURL, "file://")
	exportedData, err := os.ReadFile(exportedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(exportedData), "Private log body payload") {
		t.Fatalf("export did not render the canonical document: %s", exportedData)
	}
	exportedInfo, err := os.Stat(exportedPath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && exportedInfo.Mode().Perm() != 0o600 {
		t.Fatalf("HTML export permissions are too broad: %o", exportedInfo.Mode().Perm())
	}
	logData, err := os.ReadFile(app.logFilePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{relativePath, "Private log title", "person:private-log-person", "doc:private-log-id", "Private log body payload"} {
		if strings.Contains(string(logData), secret) {
			t.Fatalf("durable app log disclosed %q: %s", secret, logData)
		}
	}
}

func TestRootDocumentCreatedByUIUsesLegacyClassificationForExport(t *testing.T) {
	app, _ := newEphyTestApp(t)
	app.ctx = nil
	if created, err := app.CreateNewFile("root-export"); err != nil || !created {
		t.Fatalf("root document was not created: created=%v err=%v", created, err)
	}
	const relativePath = "content/root-export.md"
	loaded, err := app.LoadFile(relativePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded, "doc_id:") {
		t.Fatalf("root document did not receive doc_id: %s", loaded)
	}
	exportedURL, err := app.ExportPreviewHTML(relativePath)
	if err != nil {
		t.Fatalf("legacy-classified root document could not be exported: %v", err)
	}
	exported, err := os.ReadFile(strings.TrimPrefix(exportedURL, "file://"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(exported), "Start writing your content here") {
		t.Fatalf("root document export lost canonical body: %s", exported)
	}
}

func TestConsultationProposalCannotReachReviewOrSave(t *testing.T) {
	app, dataRoot := newEphyTestApp(t)
	proposal := writePendingFixture(t, dataRoot, "consultation-proposal.json")
	inbox, err := app.ListEphyProposals()
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox.Proposals) != 0 || len(inbox.Errors) != 1 || !strings.Contains(inbox.Errors[0].Message, "consultation") {
		t.Fatalf("consultation proposal became reviewable: %#v", inbox)
	}
	if _, err := app.AcceptEphyProposal(proposal.CandidateID, proposal.ProposedFrontmatter, proposal.ProposedBody); err == nil {
		t.Fatal("consultation proposal reached SaveFile")
	}
}

func TestRejectEphyProposalIsIdempotentAndDoesNotSave(t *testing.T) {
	app, dataRoot := newEphyTestApp(t)
	proposal := writePendingFixture(t, dataRoot, "create-proposal.json")
	app.ephySaveFile = func(path, content string) error {
		t.Fatal("reject must not call SaveFile")
		return nil
	}
	first, err := app.RejectEphyProposal(proposal.CandidateID, "Not canonical.")
	if err != nil {
		t.Fatal(err)
	}
	second, err := app.RejectEphyProposal(proposal.CandidateID, "Not canonical.")
	if err != nil {
		t.Fatal(err)
	}
	if first.Result != "rejected" || second.Result != "rejected" {
		t.Fatalf("unexpected receipts: %#v %#v", first, second)
	}
}

func TestListEphyProposalsRejectsPlacementSymlinkEscape(t *testing.T) {
	app, dataRoot := newEphyTestApp(t)
	writePendingFixture(t, dataRoot, "create-proposal.json")
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dataRoot, "content", "projects")); err != nil {
		t.Fatal(err)
	}
	inbox, err := app.ListEphyProposals()
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox.Proposals) != 0 || len(inbox.Errors) == 0 || inbox.Errors[0].Code != "proposal_not_reviewable" {
		t.Fatalf("symlink escape was not rejected: %#v", inbox)
	}
}
