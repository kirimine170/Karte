package ephyrecordsv2

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"karte/internal/canonical"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

// IsManagedPath checks ownership without loading a v2 document or granting read
// access. Callers must hold the canonical lease and reject every returned error.
// This intentionally does not change the existing v2 reader or writer behavior.
func IsManagedPath(w *canonical.Writer, path string) (bool, error) {
	requested, err := managedContentPath(path)
	if err != nil {
		return false, err
	}
	raw, err := w.ReadLimit(filepath.Join(stateDir, "ledger.json"), 2<<20)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("invalid_v2_ledger")
	}
	normalized, err := CanonicalJSON(raw)
	if err != nil {
		return false, fmt.Errorf("invalid_v2_ledger")
	}
	var state ledger
	if json.Unmarshal(normalized, &state) != nil || state.Docs == nil || state.Candidates == nil || state.Events == nil {
		return false, fmt.Errorf("invalid_v2_ledger")
	}
	// Every ledger field is required and none uses omitempty. A lossless
	// canonical round trip therefore rejects aliases, unknown/missing fields,
	// and null objects/scalars at every depth. No input is repaired or saved.
	decoded, err := encodeCanonical(state)
	if err != nil || !bytes.Equal(normalized, decoded) {
		return false, fmt.Errorf("invalid_v2_ledger")
	}
	seen := map[string]bool{}
	match := false
	for id, doc := range state.Docs {
		p, e := managedContentPath(doc.Path)
		if e != nil || !validID(id) || doc.Target.DocID != id || doc.Target.Revision < 1 || !digest.MatchString(doc.Target.SHA256) || !validID(doc.ScopeID) || !validID(doc.ProducerID) || doc.LogicalKey == "" {
			return false, fmt.Errorf("invalid_v2_ledger")
		}
		// Treat Windows aliases as one ownership key on every platform.
		key := strings.ToLower(p)
		if seen[key] {
			return false, fmt.Errorf("invalid_v2_ledger")
		}
		seen[key] = true
		match = match || strings.EqualFold(p, requested)
	}
	for id, receipt := range state.Candidates {
		if !validID(id) || receipt.CandidateID != id || !managedReceiptValid(receipt, state.Docs) {
			return false, fmt.Errorf("invalid_v2_ledger")
		}
		for _, eventID := range receipt.EventIDs {
			doc := state.Docs[receipt.Applied.DocID]
			effect, ok := state.Events[eventKey(doc.ScopeID, eventID)]
			if !ok || effect.DocID != receipt.Applied.DocID || effect.Receipt.Applied != receipt.Applied || effect.Receipt.Adoption != receipt.Adoption || receipt.Status != "already_applied" && !reflect.DeepEqual(receipt, effect.Receipt) {
				return false, fmt.Errorf("invalid_v2_ledger")
			}
		}
	}
	for key, effect := range state.Events {
		scope, eventID, ok := strings.Cut(key, ":")
		doc, exists := state.Docs[effect.DocID]
		receipt, recorded := state.Candidates[effect.Receipt.CandidateID]
		if !ok || !validID(scope) || !validID(eventID) || !digest.MatchString(effect.Hash) || !exists || doc.ScopeID != scope || effect.Receipt.Applied.DocID != effect.DocID || !managedReceiptValid(effect.Receipt, state.Docs) || effect.Receipt.Status != "accepted" || len(effect.Receipt.EventIDs) != 1 || effect.Receipt.EventIDs[0] != eventID || !recorded || !reflect.DeepEqual(receipt, effect.Receipt) {
			return false, fmt.Errorf("invalid_v2_ledger")
		}
	}
	return match, nil
}

func managedReceiptValid(receipt Receipt, docs map[string]DocState) bool {
	doc, exists := docs[receipt.Applied.DocID]
	adoption := receipt.Adoption
	if receipt.SchemaVersion != Version || !validID(receipt.CandidateID) || !digest.MatchString(receipt.ProposalHash) || !oneOf(receipt.Status, "accepted", "already_applied", "human_edit") || !exists || receipt.Applied.Revision < 1 || receipt.Applied.Revision > doc.Target.Revision || !digest.MatchString(receipt.Applied.SHA256) || receipt.EventIDs == nil || len(receipt.EventIDs) > 1 || adoption.Mode != "policy" || !token.MatchString(adoption.ActorID) || !validID(adoption.PolicyID) || adoption.PolicyRevision < 1 || adoption.Decision != "scope_allowed" {
		return false
	}
	// Historical receipts retain their original revision and hash after later
	// appends or human edits; only the current revision has the current hash.
	if receipt.Applied.Revision == doc.Target.Revision && receipt.Applied.SHA256 != doc.Target.SHA256 {
		return false
	}
	if receipt.Status == "human_edit" && len(receipt.EventIDs) != 0 || receipt.Status == "already_applied" && len(receipt.EventIDs) != 1 {
		return false
	}
	for _, id := range receipt.EventIDs {
		if !validID(id) {
			return false
		}
	}
	return true
}

func managedContentPath(path string) (string, error) {
	if strings.ContainsAny(path, ":\x00") {
		return "", fmt.Errorf("invalid_v2_path")
	}
	native := filepath.FromSlash(strings.ReplaceAll(path, "\\", "/"))
	if !filepath.IsLocal(native) || filepath.Clean(native) != native {
		return "", fmt.Errorf("invalid_v2_path")
	}
	slash := filepath.ToSlash(native)
	if !strings.HasPrefix(strings.ToLower(slash), "content/") {
		return "", fmt.Errorf("invalid_v2_path")
	}
	for _, part := range strings.Split(slash, "/") {
		if part == "" || strings.TrimRight(part, ". ") != part {
			return "", fmt.Errorf("invalid_v2_path")
		}
	}
	return slash, nil
}
