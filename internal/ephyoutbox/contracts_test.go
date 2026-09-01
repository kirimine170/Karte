package ephyoutbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixtureBytes(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "karte-ephy", "v1", "fixtures", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestDecodeProposalFixtures(t *testing.T) {
	for _, name := range []string{"create-proposal.json", "append-proposal.json", "consultation-proposal.json"} {
		proposal, err := DecodeProposal(fixtureBytes(t, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if proposal.SchemaVersion != SchemaVersion {
			t.Fatalf("%s: unexpected schema version", name)
		}
	}
	consultation, err := DecodeProposal(fixtureBytes(t, "consultation-proposal.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := consultation.RequirePublishable(); err == nil {
		t.Fatal("consultation must be resolved before publish")
	}
	if _, err := DecodeProposal(fixtureBytes(t, "invalid-traversal-proposal.json")); err == nil {
		t.Fatal("expected traversal proposal to be rejected")
	}
	withTrailing := append(fixtureBytes(t, "create-proposal.json"), []byte("{}")...)
	if _, err := DecodeProposal(withTrailing); err == nil {
		t.Fatal("expected trailing JSON to be rejected")
	}
}

func TestStoreListsPendingAndRejectsDuplicateCandidate(t *testing.T) {
	dataRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataRoot, "content"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	payload := fixtureBytes(t, "create-proposal.json")
	if err := os.WriteFile(filepath.Join(store.pendingDir, "candidate-create-001.json"), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.pendingDir, ".partial.tmp"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	proposals, proposalErrors, err := store.ListPending()
	if err != nil {
		t.Fatal(err)
	}
	if len(proposals) != 1 || len(proposalErrors) != 0 {
		t.Fatalf("unexpected initial inbox: proposals=%d errors=%d", len(proposals), len(proposalErrors))
	}
	if err := os.WriteFile(filepath.Join(store.pendingDir, "duplicate.json"), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	proposals, proposalErrors, err = store.ListPending()
	if err != nil {
		t.Fatal(err)
	}
	if len(proposals) != 0 {
		t.Fatalf("duplicate candidate must not be reviewable: %#v", proposals)
	}
	if len(proposalErrors) < 2 {
		t.Fatalf("expected duplicate errors, got %#v", proposalErrors)
	}
}

func TestStoreWritesReceiptAtomicallyAndArchivesProposal(t *testing.T) {
	dataRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataRoot, "content"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.pendingDir, "candidate-append-001.json"), fixtureBytes(t, "append-proposal.json"), 0o644); err != nil {
		t.Fatal(err)
	}
	var receipt Receipt
	if err := json.Unmarshal(fixtureBytes(t, "accepted-receipt.json"), &receipt); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteReceipt(receipt); err != nil {
		t.Fatalf("idempotent receipt retry failed: %v", err)
	}
	if err := store.MoveProposal(receipt.CandidateID, receipt.Result); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(store.acceptedDir, receipt.CandidateID+".json")); err != nil {
		t.Fatal(err)
	}
	partials, err := filepath.Glob(filepath.Join(store.receiptsDir, "*.tmp"))
	if err != nil || len(partials) != 0 {
		t.Fatalf("partial receipts remain: %#v, %v", partials, err)
	}
}

func TestNewStoreRejectsSymlinkEscapeBeforeCreatingLayout(t *testing.T) {
	dataRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataRoot, "content"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dataRoot, ".mdsys")); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(dataRoot); err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("outside directory was modified: entries=%v err=%v", entries, err)
	}
}

func TestStoreRejectsReceiptSymlink(t *testing.T) {
	dataRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataRoot, "content"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "receipt.json")
	if err := os.WriteFile(outside, fixtureBytes(t, "accepted-receipt.json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(store.receiptsDir, "candidate-append-001.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadReceipt("candidate-append-001"); err == nil {
		t.Fatal("expected receipt symlink to be rejected")
	}
}

func TestStoreRejectsReceiptWithTrailingJSON(t *testing.T) {
	dataRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataRoot, "content"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	payload := append(fixtureBytes(t, "accepted-receipt.json"), []byte("{}")...)
	if err := os.WriteFile(filepath.Join(store.receiptsDir, "candidate-append-001.json"), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadReceipt("candidate-append-001"); err == nil {
		t.Fatal("expected trailing receipt JSON to be rejected")
	}
}

func TestResolvePlacementUsesProjectKindMonthAndStableCollisionSuffix(t *testing.T) {
	dataRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataRoot, "content"), 0o755); err != nil {
		t.Fatal(err)
	}
	proposal, err := DecodeProposal(fixtureBytes(t, "create-proposal.json"))
	if err != nil {
		t.Fatal(err)
	}
	docID, err := DeriveCreateDocID(proposal.CandidateID)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := ResolvePlacement(dataRoot, proposal, docID)
	if err != nil {
		t.Fatal(err)
	}
	want := "content/projects/ephy/decision/2026-09/synthetic-placement-decision.md"
	if decision.RelativePath != want || decision.DocID != docID {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	abs := filepath.Join(dataRoot, filepath.FromSlash(want))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("---\ndoc_id: other-document\n---\nexisting\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	collision, err := ResolvePlacement(dataRoot, proposal, docID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(collision.RelativePath, "synthetic-placement-decision--"+docID[:8]+".md") {
		t.Fatalf("collision suffix did not use doc_id: %#v", collision)
	}
}
