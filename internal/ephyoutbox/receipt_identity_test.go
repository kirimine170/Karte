package ephyoutbox

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreRejectsReceiptCandidateIDMismatch(t *testing.T) {
	for _, result := range []string{"accepted", "rejected", "conflict", "invalid"} {
		t.Run(result, func(t *testing.T) {
			dataRoot := t.TempDir()
			store, err := NewStore(dataRoot)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.EnsureLayout(); err != nil {
				t.Fatal(err)
			}
			var receipt Receipt
			if err := json.Unmarshal(fixtureBytes(t, "accepted-receipt.json"), &receipt); err != nil {
				t.Fatal(err)
			}
			receipt.Result = result
			if err := receipt.Validate(); err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(receipt)
			if err != nil {
				t.Fatal(err)
			}
			candidateID := "candidate-other-001"
			path := filepath.Join(store.receiptsDir, candidateID+".json")
			if err := os.WriteFile(path, payload, 0o600); err != nil {
				t.Fatal(err)
			}
			for attempt := 0; attempt < 2; attempt++ {
				got, err := store.ReadReceipt(candidateID)
				if err == nil || !strings.Contains(err.Error(), "candidate_id does not match filename") || got != nil {
					t.Errorf("mismatched receipt accepted: receipt=%#v err=%v", got, err)
				}
				store, err = NewStore(dataRoot)
				if err != nil {
					t.Fatal(err)
				}
			}
			current, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(current, payload) {
				t.Fatalf("reading mismatch changed receipt bytes: err=%v", err)
			}
		})
	}
}

func TestStoreMatchingReceiptIsIdempotentAfterRestart(t *testing.T) {
	dataRoot := t.TempDir()
	store, err := NewStore(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	var receipt Receipt
	if err := json.Unmarshal(fixtureBytes(t, "accepted-receipt.json"), &receipt); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.receiptsDir, receipt.CandidateID+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	store, err = NewStore(dataRoot)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.ReadReceipt(receipt.CandidateID)
	if err != nil || got == nil {
		t.Fatalf("matching receipt did not survive restart: receipt=%#v err=%v", got, err)
	}
	wantJSON, _ := json.Marshal(receipt)
	gotJSON, _ := json.Marshal(got)
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("matching receipt changed after restart: %s", gotJSON)
	}
	if err := store.WriteReceipt(receipt); err != nil {
		t.Fatalf("idempotent receipt retry failed after restart: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("idempotent retry changed receipt bytes: err=%v", err)
	}
}

func TestStoreReceiptIdentityUsesActualFilenameCase(t *testing.T) {
	for _, result := range []string{"accepted", "rejected", "conflict", "invalid"} {
		t.Run(result, func(t *testing.T) {
			dataRoot := t.TempDir()
			store, err := NewStore(dataRoot)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.EnsureLayout(); err != nil {
				t.Fatal(err)
			}
			var receipt Receipt
			if err := json.Unmarshal(fixtureBytes(t, "accepted-receipt.json"), &receipt); err != nil {
				t.Fatal(err)
			}
			actualID := receipt.CandidateID
			receipt.CandidateID, receipt.Result = strings.ToUpper(actualID), result
			if err := receipt.Validate(); err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(receipt)
			if err != nil {
				t.Fatal(err)
			}
			filePath := filepath.Join(store.receiptsDir, actualID+".json")
			if err := os.WriteFile(filePath, payload, 0o600); err != nil {
				t.Fatal(err)
			}
			_, aliasErr := os.Stat(filepath.Join(store.receiptsDir, receipt.CandidateID+".json"))
			if aliasErr != nil && !os.IsNotExist(aliasErr) {
				t.Fatal(aliasErr)
			}
			got, readErr := store.ReadReceipt(receipt.CandidateID)
			if os.IsNotExist(aliasErr) {
				if got != nil || readErr != nil {
					t.Fatalf("missing exact filename was not absent: receipt=%#v err=%v", got, readErr)
				}
			} else if got != nil || readErr == nil || !strings.Contains(readErr.Error(), "candidate_id does not match filename") {
				t.Errorf("case-insensitive alias trusted the caller's filename: receipt=%#v err=%v", got, readErr)
			}
			current, err := os.ReadFile(filePath)
			if err != nil || !bytes.Equal(current, payload) {
				t.Fatalf("case mismatch changed receipt bytes: err=%v", err)
			}
		})
	}
}
