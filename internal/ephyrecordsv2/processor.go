package ephyrecordsv2

import (
	"encoding/json"
	"errors"
	"fmt"
	"karte/internal/canonical"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Capabilities struct {
	ProtocolVersion string          `json:"protocol_version"`
	RecordSchema    string          `json:"record_schema"`
	Operations      []string        `json:"operations"`
	Enabled         bool            `json:"enabled"`
	Policies        []PolicyVersion `json:"policies"`
}
type PolicyVersion struct {
	ScopeID         string `json:"scope_id"`
	PolicyID        string `json:"policy_id"`
	Revision        int64  `json:"policy_revision"`
	ConsentEpoch    int64  `json:"consent_epoch"`
	ScopeGeneration int64  `json:"scope_generation"`
}
type ProcessSummary struct {
	Processed int `json:"processed"`
	Failed    int `json:"failed"`
	Recovered int `json:"recovered"`
}
type Rejection struct {
	SchemaVersion string `json:"schema_version"`
	CandidateID   string `json:"candidate_id"`
	Status        string `json:"status"`
	Code          string `json:"code"`
}

func (s *Service) Capabilities() (Capabilities, error) {
	c := Capabilities{ProtocolVersion: Version, RecordSchema: Version, Operations: []string{"create_record", "append_events", "revise_derivation", "search", "read"}, Policies: []PolicyVersion{}}
	all, e := s.loadRegistrations()
	if e != nil {
		return c, e
	}
	for _, r := range all.Scopes {
		g := r.Grant
		if g.Enabled {
			c.Enabled = true
			c.Policies = append(c.Policies, PolicyVersion{ScopeID: g.ScopeID, PolicyID: g.PolicyID, Revision: g.Revision, ConsentEpoch: g.ConsentEpoch, ScopeGeneration: g.ScopeGeneration})
		}
	}
	sort.Slice(c.Policies, func(i, j int) bool { return c.Policies[i].ScopeID < c.Policies[j].ScopeID })
	return c, nil
}
func purgeResponses(w *canonical.Writer) error {
	entries, e := w.Entries(filepath.Join(contextDir, "responses"))
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	for _, f := range entries {
		if !f.IsDir() {
			if e = w.Remove(filepath.Join(contextDir, "responses", f.Name())); e != nil {
				return e
			}
		}
	}
	return nil
}
func reasonCode(e error) string {
	if e == nil {
		return ""
	}
	code := e.Error()
	if token.MatchString(code) {
		return code
	}
	return "invalid_request"
}
func (s *Service) ProcessPending(limit int) (ProcessSummary, error) {
	summary := ProcessSummary{}
	if limit < 1 || limit > 100 {
		return summary, fmt.Errorf("invalid_limit")
	}
	recovered, e := s.Recover()
	if e != nil {
		return summary, e
	}
	for _, r := range recovered {
		if r.Status == "recovered" {
			summary.Recovered++
		}
	}
	type pending struct {
		path    string
		data    []byte
		context bool
	}
	items := []pending{}
	e = canonical.WithWriter(s.DataRoot, func(w *canonical.Writer) error {
		cap, e := s.Capabilities()
		if e != nil {
			return e
		}
		if e = saveJSON(w, filepath.Join(contextDir, "capabilities.json"), cap); e != nil {
			return e
		}
		for _, dir := range []string{filepath.Join(outboxDir, "pending"), filepath.Join(contextDir, "requests")} {
			if e = w.MkdirAll(dir, 0700); e != nil {
				return e
			}
			entries, e := w.Entries(dir)
			if e != nil {
				return e
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
			count := 0
			for _, f := range entries {
				if count >= limit {
					break
				}
				id := strings.TrimSuffix(f.Name(), ".json")
				if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") || !validID(id) {
					continue
				}
				path := filepath.Join(dir, f.Name())
				data, e := w.ReadLimit(path, 2<<20)
				if e != nil {
					summary.Failed++
					continue
				}
				items = append(items, pending{path: path, data: data, context: dir == filepath.Join(contextDir, "requests")})
				count++
			}
		}
		return nil
	})
	if e != nil {
		return summary, e
	}
	for _, item := range items {
		id := strings.TrimSuffix(filepath.Base(item.path), ".json")
		if item.context {
			e = s.processQuery(item.path, id, item.data)
		} else {
			var p Proposal
			e = checkVersion(item.data, "schema_version")
			if e == nil {
				e = strict(item.data, &p)
			}
			if e == nil && p.CandidateID != id {
				e = fmt.Errorf("id_mismatch")
			}
			if e == nil {
				_, e = s.Apply(item.data)
			}
			if e != nil {
				code := reasonCode(e)
				if rejectable(e) {
					cause := e
					writeErr := canonical.WithWriter(s.DataRoot, func(w *canonical.Writer) error {
						rejection := Rejection{SchemaVersion: Version, CandidateID: id, Status: "rejected", Code: code}
						// Accepted receipts are immutable facts．Store failed retries separately．
						if e = saveJSON(w, filepath.Join(outboxDir, "rejected", id+".result.json"), rejection); e != nil {
							return e
						}
						return archiveMatching(w, item.path, filepath.Join(outboxDir, "rejected", id+".json"), item.data)
					})
					if writeErr != nil {
						return summary, writeErr
					}
					e = cause
				}
			}
			if e == nil {
				e = canonical.WithWriter(s.DataRoot, func(w *canonical.Writer) error {
					return archiveMatching(w, item.path, filepath.Join(outboxDir, "accepted", id+".json"), item.data)
				})
			}

		}
		if e != nil {
			summary.Failed++
		} else {
			summary.Processed++
		}
	}
	return summary, nil
}
func archiveMatching(w *canonical.Writer, source, dest string, expected []byte) error {
	b, e := w.Read(source)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	if canonical.Hash(b) != canonical.Hash(expected) {
		return fmt.Errorf("id_reuse")
	}
	if e = w.Write(dest, b, 0600); e != nil {
		return e
	}
	return w.Remove(source)
}
func (s *Service) processQuery(path, id string, raw []byte) error {
	return canonical.WithWriter(s.DataRoot, func(w *canonical.Writer) error {
		response := Response{ProtocolVersion: Version, RequestID: id, Status: "invalid_request", Results: []ReadResult{}}
		var q Request
		validation := checkVersion(raw, "protocol_version")
		if validation == nil {
			validation = strict(raw, &q)
		}
		if validation == nil {
			validation = q.validateWire(raw)
		}
		if validation == nil && q.RequestID != id {
			validation = fmt.Errorf("id_mismatch")
		}
		if validation != nil {
			response.Status = reasonCode(validation)
		} else {
			// Re-evaluate every request under current policy，including exact retries．
			b, e := signingBytes(raw)
			if e != nil {
				return e
			}
			hash := canonical.Hash(b)
			seen := map[string]string{}
			ledgerPath := filepath.Join(contextDir, "request-ledger.json")
			if data, e := w.Read(ledgerPath); e == nil {
				if e = json.Unmarshal(data, &seen); e != nil {
					return e
				}
			} else if !errors.Is(e, os.ErrNotExist) {
				return e
			}
			if prior, ok := seen[id]; ok && prior != hash {
				response.Status = "id_reuse"
			} else {
				var e error
				response, e = s.queryLocked(w, q, raw)
				if e != nil {
					response.Status = "not_available"
					response.Results = []ReadResult{}
				}
				seen[id] = hash
				if e = saveJSON(w, ledgerPath, seen); e != nil {
					return e
				}
			}
		}
		if e := saveJSON(w, filepath.Join(contextDir, "responses", id+".json"), response); e != nil {
			return e
		}
		return archiveMatching(w, path, filepath.Join(contextDir, "processed", id+".json"), raw)
	})
}

func rejectable(err error) bool {
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		return false
	}
	code := err.Error()
	if strings.HasPrefix(code, "json:") || strings.HasPrefix(code, "invalid character") || strings.HasPrefix(code, "unexpected EOF") {
		return true
	}
	return oneOf(code, "missing_required_field", "invalid_null", "invalid_json", "trailing_json", "duplicate_key", "integer_required", "json_depth", "invalid_request", "invalid_envelope", "invalid_record", "invalid_record_type", "invalid_logical_key", "invalid_target", "invalid_event_count", "invalid_event", "invalid_event_date", "invalid_input_kind", "invalid_asr", "invalid_assistant", "invalid_playback", "invalid_correction", "invalid_derivation", "invalid_claim", "invalid_source", "claim_source_not_input", "unsupported_protocol", "unsupported_operation", "unsupported_event_type", "configuration_required", "permission_denied", "id_reuse", "id_mismatch", "conflict", "record_identity_changed", "classification_changed", "record_capacity", "event_sequence_gap", "stale_correction", "stale_source")
}
