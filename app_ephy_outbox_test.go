package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
	if len(inbox.Proposals) != 0 || len(inbox.Errors) != 1 || !strings.Contains(inbox.Errors[0].Message, "project") {
		t.Fatalf("content mismatch was not rejected: %#v", inbox)
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
