package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func writePendingFixture(t *testing.T, dataRoot, name string) ephyoutbox.Proposal {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("schemas", "karte-ephy", "v1", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
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

func TestListEphyProposalsIsReadOnlyAndShowsReviewContext(t *testing.T) {
	app, dataRoot := newEphyTestApp(t)
	proposal := writePendingFixture(t, dataRoot, "create-proposal.json")
	canonicalBefore, err := filepath.Glob(filepath.Join(dataRoot, "content", "**", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := app.ListEphyProposals()
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox.Proposals) != 1 || len(inbox.Errors) != 0 {
		t.Fatalf("unexpected inbox: %#v", inbox)
	}
	review := inbox.Proposals[0]
	if review.Proposal.CandidateID != proposal.CandidateID || review.Diff == "" || !strings.Contains(review.ProposedContent, proposal.ProposedBody) {
		t.Fatalf("review context is incomplete: %#v", review)
	}
	canonicalAfter, err := filepath.Glob(filepath.Join(dataRoot, "content", "**", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(canonicalBefore) != len(canonicalAfter) {
		t.Fatal("listing proposals changed canonical content")
	}
}

func TestAcceptEphyProposalUsesSaveFileAndRecoversReceiptRetry(t *testing.T) {
	app, dataRoot := newEphyTestApp(t)
	proposal := writePendingFixture(t, dataRoot, "create-proposal.json")
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
	canonicalPath := filepath.Join(dataRoot, filepath.FromSlash(proposal.TargetRelativePath))
	firstSaved, err := os.ReadFile(canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := app.AcceptEphyProposal(proposal.CandidateID, editedFrontmatter, editedBody)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Result != "accepted" || receipt.DocID == nil || receipt.ResultingSHA256 == nil {
		t.Fatalf("unexpected receipt: %#v", receipt)
	}
	if saveCalls != 1 {
		t.Fatalf("SaveFile must not be repeated after canonical save, got %d calls", saveCalls)
	}
	secondSaved, err := os.ReadFile(canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstSaved) != string(secondSaved) {
		t.Fatal("receipt retry changed canonical Markdown")
	}
	if !strings.Contains(string(secondSaved), "Edited synthetic memory") || !strings.Contains(string(secondSaved), "Reviewed before acceptance.") {
		t.Fatal("edit-and-accept content was not saved")
	}
	if _, err := os.Stat(filepath.Join(dataRoot, ".mdsys", "ephy", "outbox", "accepted", proposal.CandidateID+".json")); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptEphyProposalRecoversCrashAfterCanonicalSave(t *testing.T) {
	app, dataRoot := newEphyTestApp(t)
	proposal := writePendingFixture(t, dataRoot, "create-proposal.json")
	prepared := "---\ndoc_id: doc:recovered-001\ntitle: Edited synthetic memory\n---\n# Recovered memory\n"
	canonicalPath := filepath.Join(dataRoot, filepath.FromSlash(proposal.TargetRelativePath))
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
		SchemaVersion:   ephyoutbox.SchemaVersion,
		CandidateID:     proposal.CandidateID,
		RelativePath:    proposal.TargetRelativePath,
		DocID:           "doc:recovered-001",
		PreparedContent: prepared,
		State:           "prepared",
		StartedAt:       time.Now().UTC().Format(time.RFC3339Nano),
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
	if _, err := os.Stat(filepath.Join(dataRoot, ".mdsys", "ephy", "outbox", "accepted", proposal.CandidateID+".json")); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptEphyUpdateUsesMatchingCanonicalByteHash(t *testing.T) {
	app, dataRoot := newEphyTestApp(t)
	canonicalPath := filepath.Join(dataRoot, "content", "existing.md")
	canonical := []byte("---\ntitle: Existing\ndoc_id: doc:synthetic-001\n---\n# Existing\n\nCanonical body.\n")
	if err := os.WriteFile(canonicalPath, canonical, 0o644); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(filepath.Join("schemas", "karte-ephy", "v1", "fixtures", "update-proposal.json"))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(fixture, &payload); err != nil {
		t.Fatal(err)
	}
	payload["base_sha256"] = ephyoutbox.SHA256Bytes(canonical)
	proposalBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := ephyoutbox.DecodeProposal(proposalBytes)
	if err != nil {
		t.Fatal(err)
	}
	pendingDir := filepath.Join(dataRoot, ".mdsys", "ephy", "outbox", "pending")
	if err := os.MkdirAll(pendingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pendingDir, proposal.CandidateID+".json"), proposalBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	receipt, err := app.AcceptEphyProposal(proposal.CandidateID, proposal.ProposedFrontmatter, proposal.ProposedBody)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Result != "accepted" || receipt.DocID == nil || *receipt.DocID != "doc:synthetic-001" {
		t.Fatalf("unexpected update receipt: %#v", receipt)
	}
	updated, err := os.ReadFile(canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "Reviewed fixture update.") || !strings.Contains(string(updated), `doc_id: "doc:synthetic-001"`) {
		t.Fatalf("canonical update is incorrect: %s", updated)
	}
}

func TestAcceptEphyProposalStopsOnStaleBaseHash(t *testing.T) {
	app, dataRoot := newEphyTestApp(t)
	canonicalPath := filepath.Join(dataRoot, "content", "existing.md")
	canonical := "---\ntitle: Existing\ndoc_id: doc:synthetic-001\n---\n# Existing\n\nCanonical body.\n"
	if err := os.WriteFile(canonicalPath, []byte(canonical), 0o644); err != nil {
		t.Fatal(err)
	}
	proposal := writePendingFixture(t, dataRoot, "update-proposal.json")
	saveCalls := 0
	app.ephySaveFile = func(path, content string) error {
		saveCalls++
		return app.SaveFile(path, content)
	}
	receipt, err := app.AcceptEphyProposal(proposal.CandidateID, proposal.ProposedFrontmatter, proposal.ProposedBody)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Result != "conflict" || receipt.ErrorCode == nil || *receipt.ErrorCode != "stale_base_sha256" {
		t.Fatalf("unexpected conflict receipt: %#v", receipt)
	}
	if saveCalls != 0 {
		t.Fatal("stale proposal called SaveFile")
	}
	current, err := os.ReadFile(canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != canonical {
		t.Fatal("stale proposal changed canonical content")
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
	if _, err := os.Stat(filepath.Join(dataRoot, ".mdsys", "ephy", "outbox", "rejected", proposal.CandidateID+".json")); err != nil {
		t.Fatal(err)
	}
}

func TestListEphyProposalsRejectsTargetSymlinkEscape(t *testing.T) {
	app, dataRoot := newEphyTestApp(t)
	proposal := writePendingFixture(t, dataRoot, "create-proposal.json")
	pendingPath := filepath.Join(dataRoot, ".mdsys", "ephy", "outbox", "pending", proposal.CandidateID+".json")
	data, err := os.ReadFile(pendingPath)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	payload["target_relative_path"] = "content/escape/new.md"
	data, err = json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pendingPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dataRoot, "content", "escape")); err != nil {
		t.Fatal(err)
	}
	inbox, err := app.ListEphyProposals()
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox.Proposals) != 0 || len(inbox.Errors) == 0 || inbox.Errors[0].Code != "invalid_target_path" {
		t.Fatalf("symlink escape was not rejected: %#v", inbox)
	}
}
