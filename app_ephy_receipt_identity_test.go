package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"karte/internal/ephyoutbox"
)

func TestEphyReceiptMismatchDoesNotProcessPendingProposal(t *testing.T) {
	for _, result := range []string{"accepted", "rejected"} {
		for _, action := range []string{"accept", "reject"} {
			t.Run(result+"/"+action, func(t *testing.T) {
				app, dataRoot := newEphyTestApp(t)
				canonicalPath, canonical := writeAppendTarget(t, dataRoot)
				pending := appendProposalWithHash(t, canonical)
				proposal := writePendingPayload(t, dataRoot, pending)
				store, err := ephyoutbox.NewStore(dataRoot)
				if err != nil {
					t.Fatal(err)
				}
				if err := store.EnsureLayout(); err != nil {
					t.Fatal(err)
				}
				var receipt ephyoutbox.Receipt
				fixture, err := os.ReadFile(filepath.Join("schemas", "karte-ephy", "v1", "fixtures", "accepted-receipt.json"))
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(fixture, &receipt); err != nil {
					t.Fatal(err)
				}
				receipt.CandidateID, receipt.Result = "candidate-other-001", result
				if err := receipt.Validate(); err != nil {
					t.Fatal(err)
				}
				receiptBytes, err := json.Marshal(receipt)
				if err != nil {
					t.Fatal(err)
				}
				outboxRoot := filepath.Join(dataRoot, ".mdsys", "ephy", "outbox")
				canonicalSHA := ephyoutbox.SHA256Bytes(canonical)
				transaction := ephyoutbox.Transaction{
					SchemaVersion: ephyoutbox.SchemaVersion, CandidateID: proposal.CandidateID,
					RelativePath: *proposal.TargetRelativePath, DocID: *proposal.TargetDocID,
					BaseSHA256: proposal.BaseSHA256, PreparedContent: string(canonical),
					State: "saved", ResultingSHA256: &canonicalSHA, StartedAt: "2026-09-01T00:00:00Z",
				}
				if err := store.WriteTransaction(transaction); err != nil {
					t.Fatal(err)
				}
				transactionPath := filepath.Join(outboxRoot, "transactions", proposal.CandidateID+".json")
				transactionBytes, err := os.ReadFile(transactionPath)
				if err != nil {
					t.Fatal(err)
				}
				receiptPath := filepath.Join(outboxRoot, "receipts", proposal.CandidateID+".json")
				if err := os.WriteFile(receiptPath, receiptBytes, 0o600); err != nil {
					t.Fatal(err)
				}
				inbox, err := app.ListEphyProposals()
				if err != nil {
					t.Fatal(err)
				}
				if len(inbox.Proposals) != 0 || len(inbox.Errors) != 1 || inbox.Errors[0].Code != "receipt_read_failed" {
					t.Errorf("mismatched receipt did not fail closed in inbox: %#v", inbox)
				}
				app.ephySaveFile = func(path, content string) error {
					t.Error("mismatched receipt reached SaveFile")
					return nil
				}
				var got *ephyoutbox.Receipt
				if action == "accept" {
					got, err = app.AcceptEphyProposal(proposal.CandidateID, proposal.ProposedFrontmatter, proposal.ProposedBody)
				} else {
					got, err = app.RejectEphyProposal(proposal.CandidateID, "Synthetic rejection")
				}
				if err == nil || !strings.Contains(err.Error(), "candidate_id does not match filename") || got != nil {
					t.Errorf("%s accepted another candidate's receipt: receipt=%#v err=%v", action, got, err)
				}
				for path, want := range map[string][]byte{
					canonicalPath: canonical,
					filepath.Join(outboxRoot, "pending", proposal.CandidateID+".json"): pending,
					receiptPath:     receiptBytes,
					transactionPath: transactionBytes,
				} {
					current, readErr := os.ReadFile(path)
					if readErr != nil || !bytes.Equal(current, want) {
						t.Errorf("%s changed %s: err=%v", action, path, readErr)
					}
				}
				for _, state := range []string{"accepted", "rejected"} {
					if entries, err := os.ReadDir(filepath.Join(outboxRoot, state)); err != nil || len(entries) != 0 {
						t.Errorf("%s changed %s: entries=%v err=%v", action, state, entries, err)
					}
				}
			})
		}
	}
}

func TestMatchingEphyReceiptIsIdempotentAfterRestart(t *testing.T) {
	for _, action := range []string{"accept", "reject"} {
		t.Run(action, func(t *testing.T) {
			app, dataRoot := newEphyTestApp(t)
			canonicalPath, canonical := writeAppendTarget(t, dataRoot)
			pending := appendProposalWithHash(t, canonical)
			proposal := writePendingPayload(t, dataRoot, pending)
			process := func(app *App) (*ephyoutbox.Receipt, error) {
				if action == "accept" {
					return app.AcceptEphyProposal(proposal.CandidateID, proposal.ProposedFrontmatter, proposal.ProposedBody)
				}
				return app.RejectEphyProposal(proposal.CandidateID, "Synthetic rejection")
			}
			first, err := process(app)
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(canonicalPath)
			if err != nil {
				t.Fatal(err)
			}
			restarted := NewApp()
			restarted.root, restarted.dataDir = dataRoot, dataRoot
			restarted.ephySaveFile = func(path, content string) error {
				t.Error("matching receipt retry called SaveFile")
				return nil
			}
			second, err := process(restarted)
			if err != nil {
				t.Fatal(err)
			}
			firstJSON, _ := json.Marshal(first)
			secondJSON, _ := json.Marshal(second)
			if second.CandidateID != proposal.CandidateID || !bytes.Equal(firstJSON, secondJSON) {
				t.Fatalf("receipt changed after restart: first=%s second=%s", firstJSON, secondJSON)
			}
			after, err := os.ReadFile(canonicalPath)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("matching receipt retry changed canonical bytes: err=%v", err)
			}
			archive := filepath.Join(dataRoot, ".mdsys", "ephy", "outbox", first.Result, proposal.CandidateID+".json")
			archived, err := os.ReadFile(archive)
			if err != nil || !bytes.Equal(pending, archived) {
				t.Fatalf("matching receipt retry changed archived proposal: err=%v", err)
			}
		})
	}
}
