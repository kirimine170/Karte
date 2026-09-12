package ephyrecordsv2

import (
	"fmt"
	"github.com/google/uuid"
	"karte/internal/canonical"
	"karte/internal/contextcore"
	"path/filepath"
)

func reservedBytes(r Record) ([]byte, error) {
	r.Extra = nil
	r.Events = append([]Event(nil), r.Events...)
	for i := range r.Events {
		r.Events[i].Text = ""
	}
	if r.Derivation != nil {
		copy := *r.Derivation
		copy.Claims = append([]Claim(nil), copy.Claims...)
		for i := range copy.Claims {
			copy.Claims[i].Text = ""
		}
		r.Derivation = &copy
	}
	return Render(r)
}

// SaveHuman executes inside the same writer lease as App.SaveFile．Free text
// and custom frontmatter can change；identity and event metadata stay reserved．
func (s *Service) SaveHuman(w *canonical.Writer, path string, data []byte) (bool, error) {
	if _, e := s.recoverAll(w); e != nil {
		return true, e
	}
	l, e := loadLedger(w)
	if e != nil {
		return true, e
	}
	for _, d := range l.Docs {
		if filepath.Clean(path) != filepath.Clean(d.Path) {
			continue
		}
		before, e := w.Read(d.Path)
		if e != nil {
			return true, e
		}
		if canonical.Hash(before) != d.Target.SHA256 {
			return true, fmt.Errorf("conflict")
		}
		old, e := Parse(before)
		if e != nil {
			return true, e
		}
		if e = s.authorizeHuman(w, old, contextcore.CapabilityReview); e != nil {
			return true, e
		}
		r, e := Parse(data)
		if e != nil {
			return true, fmt.Errorf("reserved_metadata: %w", e)
		}
		a, e := reservedBytes(old)
		if e != nil {
			return true, e
		}
		b, e := reservedBytes(r)
		if e != nil {
			return true, e
		}
		if string(a) != string(b) {
			return true, fmt.Errorf("conflict: reserved_metadata_changed")
		}
		if string(before) == string(data) {
			return true, nil
		}
		r.Meta.Revision++
		r.Meta.HumanEdited = true
		result, e := Render(r)
		if e != nil {
			return true, e
		}
		target := r.Target(result)
		intent := uuid.NewString()
		receipt := Receipt{SchemaVersion: Version, CandidateID: intent, ProposalHash: canonical.Hash(data), Status: "human_edit", Applied: target, EventIDs: []string{}, Adoption: r.Meta.Adoption}
		tx := transaction{SchemaVersion: Version, Human: true, Proposal: Proposal{CandidateID: intent}, Base: &d.Target.SHA256, Result: result, Doc: DocState{Target: target, Path: d.Path, ScopeID: d.ScopeID, ProducerID: d.ProducerID, LogicalKey: d.LogicalKey, HumanEdited: true}, Receipt: receipt, Stage: "prepared"}
		if e = saveJSON(w, txPath(intent), tx); e != nil {
			return true, e
		}
		if e = s.fault("prepared"); e != nil {
			return true, e
		}
		return true, s.finish(w, &tx)
	}
	return false, nil
}
