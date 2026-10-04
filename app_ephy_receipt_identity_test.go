package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"karte/internal/ephyoutbox"
)

// A receipt can have the exact requested identity while the remaining artifacts
// belong to a different candidate. Exercise real case aliases only where the
// filesystem resolves them; payload mismatches also run on case-sensitive hosts.
func TestEphyReceiptDoesNotProcessOtherCandidateArtifacts(t *testing.T) {
	for _, artifact := range []string{"pending", "archive", "transaction"} {
		for _, identity := range []string{"lower-name-and-payload", "lower-name", "lower-payload"} {
			for _, action := range []string{"accept", "reject"} {
				if artifact == "transaction" && action == "reject" {
					continue // Reject does not remove transactions.
				}
				t.Run(artifact+"/"+identity+"/"+action, func(t *testing.T) {
					app, dataRoot := newEphyTestApp(t)
					_, canonical := writeAppendTarget(t, dataRoot)
					var proposal ephyoutbox.Proposal
					if err := json.Unmarshal(appendProposalWithHash(t, canonical), &proposal); err != nil {
						t.Fatal(err)
					}
					lowerID, requestedID := proposal.CandidateID, strings.ToUpper(proposal.CandidateID)
					proposal.CandidateID = requestedID
					store, err := ephyoutbox.NewStore(dataRoot)
					if err != nil {
						t.Fatal(err)
					}
					if err := store.EnsureLayout(); err != nil {
						t.Fatal(err)
					}
					outboxRoot := filepath.Join(dataRoot, ".mdsys", "ephy", "outbox")
					result := "accepted"
					if action == "reject" {
						result = "rejected"
					}
					var receipt ephyoutbox.Receipt
					fixture, err := os.ReadFile(filepath.Join("schemas", "karte-ephy", "v1", "fixtures", "accepted-receipt.json"))
					if err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(fixture, &receipt); err != nil {
						t.Fatal(err)
					}
					receipt.CandidateID, receipt.Result = requestedID, result
					if err := store.WriteReceipt(receipt); err != nil {
						t.Fatal(err)
					}
					sha := ephyoutbox.SHA256Bytes(canonical)
					transaction := ephyoutbox.Transaction{
						SchemaVersion: ephyoutbox.SchemaVersion, CandidateID: requestedID,
						RelativePath: *proposal.TargetRelativePath, DocID: *proposal.TargetDocID,
						BaseSHA256: proposal.BaseSHA256, PreparedContent: string(canonical),
						State: "saved", ResultingSHA256: &sha, StartedAt: "2026-09-01T00:00:00Z",
					}
					nameID, payloadID := requestedID, requestedID
					if identity != "lower-payload" {
						nameID = lowerID
					}
					if identity != "lower-name" {
						payloadID = lowerID
					}
					proposalDir := "pending"
					if artifact == "archive" {
						proposalDir = result
					}
					proposalName, transactionName := requestedID, requestedID
					if artifact == "transaction" {
						transactionName, transaction.CandidateID = nameID, payloadID
					} else {
						proposalName, proposal.CandidateID = nameID, payloadID
					}
					for path, value := range map[string]any{
						filepath.Join(outboxRoot, proposalDir, proposalName+".json"):       proposal,
						filepath.Join(outboxRoot, "transactions", transactionName+".json"): transaction,
					} {
						payload, err := json.Marshal(value)
						if err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(path, payload, 0o600); err != nil {
							t.Fatal(err)
						}
					}
					if nameID != requestedID {
						dir := proposalDir
						if artifact == "transaction" {
							dir = "transactions"
						}
						if _, err := os.Stat(filepath.Join(outboxRoot, dir, requestedID+".json")); os.IsNotExist(err) {
							t.Skip("case-sensitive filesystem: no case alias to exercise")
						} else if err != nil {
							t.Fatal(err)
						}
						t.Log("case-insensitive filesystem alias confirmed")
					}
					if got, err := store.ReadReceipt(requestedID); err != nil || got == nil {
						t.Fatalf("receipt must have an exact, valid identity: receipt=%#v err=%v", got, err)
					}
					before := snapshotEphyIdentityFiles(t, dataRoot)
					for attempt := 0; attempt < 2; attempt++ {
						app.ephySaveFile = func(path, content string) error {
							t.Error("other candidate's artifact reached SaveFile")
							return nil
						}
						var got *ephyoutbox.Receipt
						if action == "accept" {
							got, err = app.AcceptEphyProposal(requestedID, nil, "")
						} else {
							got, err = app.RejectEphyProposal(requestedID, "Synthetic rejection")
						}
						if err == nil || !strings.Contains(err.Error(), "candidate_id does not match filename") || got != nil {
							t.Errorf("attempt %d trusted %s: receipt=%#v err=%v", attempt, artifact, got, err)
						}
						if !reflect.DeepEqual(before, snapshotEphyIdentityFiles(t, dataRoot)) {
							t.Errorf("attempt %d changed files or bytes before rejecting identity", attempt)
						}
						app = NewApp()
						app.root, app.dataDir = dataRoot, dataRoot
					}
				})
			}
		}
	}
}

func snapshotEphyIdentityFiles(t *testing.T, root string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[relative], err = os.ReadFile(path)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func TestEphyReceiptMismatchDoesNotProcessPendingProposal(t *testing.T) {
	for _, result := range []string{"accepted", "rejected"} {
		for _, scenario := range []struct {
			action    string
			caseAlias bool
		}{{"accept", false}, {"reject", false}, {"accept", true}, {"reject", true}} {
			action := scenario.action
			name := result + "/" + action
			if scenario.caseAlias {
				name += "/case-alias"
			}
			t.Run(name, func(t *testing.T) {
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
				requestedID := proposal.CandidateID
				if scenario.caseAlias {
					receipt.CandidateID = strings.ToUpper(proposal.CandidateID)
					requestedID = receipt.CandidateID
				}
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
				if _, err := os.Stat(filepath.Join(outboxRoot, "receipts", requestedID+".json")); os.IsNotExist(err) {
					// Case-sensitive filesystems exercise the mismatched actual entry.
					requestedID = proposal.CandidateID
				} else if err != nil {
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
					got, err = app.AcceptEphyProposal(requestedID, proposal.ProposedFrontmatter, proposal.ProposedBody)
				} else {
					got, err = app.RejectEphyProposal(requestedID, "Synthetic rejection")
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

func TestMatchingEphyReceiptCompletesArchiveAfterRestart(t *testing.T) {
	for _, result := range []string{"accepted", "rejected"} {
		t.Run(result, func(t *testing.T) {
			_, dataRoot := newEphyTestApp(t)
			canonicalPath, canonical := writeAppendTarget(t, dataRoot)
			pending := appendProposalWithHash(t, canonical)
			proposal := writePendingPayload(t, dataRoot, pending)
			store, err := ephyoutbox.NewStore(dataRoot)
			if err != nil {
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
			receipt.Result = result
			if err := store.WriteReceipt(receipt); err != nil {
				t.Fatal(err)
			}
			sha := ephyoutbox.SHA256Bytes(canonical)
			if err := store.WriteTransaction(ephyoutbox.Transaction{
				SchemaVersion: ephyoutbox.SchemaVersion, CandidateID: proposal.CandidateID,
				RelativePath: *proposal.TargetRelativePath, DocID: *proposal.TargetDocID,
				BaseSHA256: proposal.BaseSHA256, PreparedContent: string(canonical),
				State: "saved", ResultingSHA256: &sha, StartedAt: "2026-09-01T00:00:00Z",
			}); err != nil {
				t.Fatal(err)
			}
			for attempt := 0; attempt < 2; attempt++ {
				app := NewApp()
				app.root, app.dataDir = dataRoot, dataRoot
				app.ephySaveFile = func(path, content string) error {
					t.Error("receipt recovery called SaveFile")
					return nil
				}
				var got *ephyoutbox.Receipt
				if result == "accepted" {
					got, err = app.AcceptEphyProposal(proposal.CandidateID, nil, "")
				} else {
					got, err = app.RejectEphyProposal(proposal.CandidateID, "Synthetic rejection")
				}
				if err != nil || !reflect.DeepEqual(got, &receipt) {
					t.Fatalf("matching recovery failed: receipt=%#v err=%v", got, err)
				}
				outboxRoot := filepath.Join(dataRoot, ".mdsys", "ephy", "outbox")
				for path, want := range map[string][]byte{
					canonicalPath: canonical,
					filepath.Join(outboxRoot, result, proposal.CandidateID+".json"): pending,
				} {
					if current, err := os.ReadFile(path); err != nil || !bytes.Equal(current, want) {
						t.Fatalf("matching recovery changed %s: err=%v", path, err)
					}
				}
				if _, err := os.Stat(filepath.Join(outboxRoot, "pending", proposal.CandidateID+".json")); !os.IsNotExist(err) {
					t.Fatalf("matching recovery left pending proposal: err=%v", err)
				}
				transaction, err := store.ReadTransaction(proposal.CandidateID)
				if err != nil || (transaction != nil) != (result == "rejected") {
					t.Fatalf("unexpected transaction cleanup: transaction=%#v err=%v", transaction, err)
				}
			}
		})
	}
}
