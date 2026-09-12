package canonical

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ManagedPaths is deliberately a small read model to keep legacy context
// independent of the v2 service．Malformed state fails closed at the caller．
func ManagedPaths(w *Writer) (map[string]bool, error) {
	paths := map[string]bool{}
	raw, e := w.Read(filepath.Join(".mdsys", "ephy", "records", "v2", "ledger.json"))
	if errors.Is(e, os.ErrNotExist) {
		return paths, nil
	}
	if e != nil {
		return nil, e
	}
	var state struct {
		Docs map[string]struct {
			Path string `json:"path"`
		} `json:"docs"`
	}
	if e = json.Unmarshal(raw, &state); e != nil {
		return nil, e
	}
	for _, d := range state.Docs {
		paths[filepath.ToSlash(d.Path)] = true
	}
	return paths, nil
}
func IsRecordPath(path string, raw []byte, paths map[string]bool) bool {
	return paths[filepath.ToSlash(path)] || strings.HasPrefix(filepath.Base(path), "ephy-v2-") || bytes.Contains(raw, []byte("runtime_record:")) || bytes.Contains(raw, []byte("karte-v2:item"))
}
