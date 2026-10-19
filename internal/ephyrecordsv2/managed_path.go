package ephyrecordsv2

import (
	"encoding/json"
	"errors"
	"fmt"
	"karte/internal/canonical"
	"os"
	"path/filepath"
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
	var state ledger
	if strict(raw, &state) != nil || state.Docs == nil || state.Candidates == nil || state.Events == nil {
		return false, fmt.Errorf("invalid_v2_ledger")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 3 {
		return false, fmt.Errorf("invalid_v2_ledger")
	}
	seen := map[string]bool{}
	match := false
	for id, doc := range state.Docs {
		p, e := managedContentPath(doc.Path)
		if e != nil || !validID(id) || doc.Target.DocID != id || doc.Target.Revision < 1 || !digest.MatchString(doc.Target.SHA256) || doc.ScopeID == "" || doc.ProducerID == "" || doc.LogicalKey == "" {
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
	return match, nil
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
