package ephyoutbox

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	for _, name := range []string{"create-proposal.json", "update-proposal.json"} {
		proposal, err := DecodeProposal(fixtureBytes(t, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if proposal.SchemaVersion != SchemaVersion {
			t.Fatalf("%s: unexpected schema version", name)
		}
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
	if err := os.WriteFile(filepath.Join(store.pendingDir, "candidate-update-001.json"), fixtureBytes(t, "update-proposal.json"), 0o644); err != nil {
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
	if err := os.Symlink(outside, filepath.Join(store.receiptsDir, "candidate-update-001.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadReceipt("candidate-update-001"); err == nil {
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
	if err := os.WriteFile(filepath.Join(store.receiptsDir, "candidate-update-001.json"), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadReceipt("candidate-update-001"); err == nil {
		t.Fatal("expected trailing receipt JSON to be rejected")
	}
}
