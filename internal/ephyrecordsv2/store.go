package ephyrecordsv2

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"karte/internal/canonical"
	"karte/internal/contextcore"
	"os"
	"path/filepath"
	"sort"
)

type DocState struct {
	Target      Target `json:"target"`
	Path        string `json:"path"`
	ScopeID     string `json:"scope_id"`
	ProducerID  string `json:"producer_instance_id"`
	LogicalKey  string `json:"logical_record_key"`
	HumanEdited bool   `json:"human_edited"`
}
type eventEffect struct {
	Hash    string  `json:"hash"`
	DocID   string  `json:"doc_id"`
	Receipt Receipt `json:"receipt"`
}
type ledger struct {
	Docs       map[string]DocState    `json:"docs"`
	Candidates map[string]Receipt     `json:"candidates"`
	Events     map[string]eventEffect `json:"events"`
}
type transaction struct {
	Human         bool     `json:"human,omitempty"`
	SchemaVersion string   `json:"schema_version"`
	Proposal      Proposal `json:"proposal"`
	Raw           []byte   `json:"raw"`
	Base          *string  `json:"base"`
	Result        []byte   `json:"result"`
	Doc           DocState `json:"doc"`
	Receipt       Receipt  `json:"receipt"`
	Stage         string   `json:"stage"`
}
type RecoveryResult struct {
	CandidateID string `json:"candidate_id"`
	Status      string `json:"status"`
}

func loadLedger(w *canonical.Writer) (ledger, error) {
	l := ledger{Docs: map[string]DocState{}, Candidates: map[string]Receipt{}, Events: map[string]eventEffect{}}
	b, e := w.Read(filepath.Join(stateDir, "ledger.json"))
	if errors.Is(e, os.ErrNotExist) {
		return l, nil
	}
	if e != nil {
		return l, e
	}
	e = json.Unmarshal(b, &l)
	if e == nil && (l.Docs == nil || l.Candidates == nil || l.Events == nil) {
		e = fmt.Errorf("invalid_ledger")
	}
	return l, e
}
func saveJSON(w *canonical.Writer, path string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return w.Write(path, b, 0600)
}
func saveLedger(w *canonical.Writer, l ledger) error {
	return saveJSON(w, filepath.Join(stateDir, "ledger.json"), l)
}
func txPath(id string) string      { return filepath.Join(outboxDir, "transactions", id+".json") }
func receiptPath(id string) string { return filepath.Join(outboxDir, "receipts", id+".json") }
func historyPath(t Target) string {
	return filepath.Join(stateDir, "revisions", t.DocID, fmt.Sprintf("%d-%s.md", t.Revision, t.SHA256))
}
func docID(p Proposal) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("karte-record-v2\x00"+p.ScopeID+"\x00"+p.ProducerID+"\x00"+p.LogicalKey)).String()
}
func eventKey(scope, id string) string { return scope + ":" + id }
func (s *Service) fault(stage string) error {
	if s.Fault != nil {
		return s.Fault(stage)
	}
	return nil
}

func (s *Service) Apply(raw []byte) (Receipt, error) {
	var p Proposal
	var result Receipt
	if e := checkVersion(raw, "schema_version"); e != nil {
		return result, e
	}
	if e := strict(raw, &p); e != nil {
		return result, e
	}
	if e := requireWireFields(raw, []string{"schema_version", "candidate_id", "operation", "logical_record_key", "scope_id", "producer_instance_id", "actor", "policy_id", "policy_revision", "consent_epoch", "scope_generation", "record", "target", "created_at", "auth"}, "/target"); e != nil {
		return result, e
	}
	if e := p.Validate(); e != nil {
		return result, e
	}
	signed, e := signingBytes(raw)
	if e != nil {
		return result, e
	}
	hash := canonical.Hash(signed)
	// Deliberately separate reception from the final commit boundary．
	if e = canonical.WithWriter(s.DataRoot, func(w *canonical.Writer) error {
		_, e := s.authorize(w, p, raw, contextcore.CapabilityPropose)
		return e
	}); e != nil {
		return result, e
	}
	if s.AfterReceive != nil {
		if e = s.AfterReceive(); e != nil {
			return result, e
		}
	}
	e = canonical.WithWriter(s.DataRoot, func(w *canonical.Writer) error {
		if _, e := s.recoverAll(w); e != nil {
			return e
		}
		g, e := s.authorize(w, p, raw, contextcore.CapabilityPropose)
		if e != nil {
			return e
		}
		l, e := loadLedger(w)
		if e != nil {
			return e
		}
		if old, ok := l.Candidates[p.CandidateID]; ok {
			if old.ProposalHash != hash {
				return fmt.Errorf("id_reuse")
			}
			result = old
			return saveJSON(w, receiptPath(p.CandidateID), old)
		}
		if b, e := w.Read(txPath(p.CandidateID)); e == nil {
			var t transaction
			if e = json.Unmarshal(b, &t); e != nil {
				return e
			}
			if t.Receipt.ProposalHash != hash {
				return fmt.Errorf("id_reuse")
			}
			return fmt.Errorf("conflict")
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		}
		if len(p.Events) == 1 {
			v := p.Events[0]
			b, e := encodeCanonical(v)
			if e != nil {
				return e
			}
			if old, ok := l.Events[eventKey(p.ScopeID, v.EventID)]; ok {
				if old.Hash != canonical.Hash(b) || old.DocID != docID(p) {
					return fmt.Errorf("id_reuse")
				}
				result = old.Receipt
				result.CandidateID = p.CandidateID
				result.ProposalHash = hash
				result.Status = "already_applied"
				l.Candidates[p.CandidateID] = result
				if e = saveLedger(w, l); e != nil {
					return e
				}
				return saveJSON(w, receiptPath(p.CandidateID), result)
			}
		}
		tx, e := s.prepare(w, l, g, p, raw, hash)
		if e != nil {
			return e
		}
		if e = saveJSON(w, txPath(p.CandidateID), tx); e != nil {
			return e
		}
		if e = s.fault("prepared"); e != nil {
			return e
		}
		if e = s.finish(w, &tx); e != nil {
			return e
		}
		result = tx.Receipt
		return nil
	})
	return result, e
}
func (s *Service) prepare(w *canonical.Writer, l ledger, g Grant, p Proposal, raw []byte, hash string) (transaction, error) {
	var tx transaction
	id := docID(p)
	current, exists := l.Docs[id]
	var r Record
	var base *string
	// Unresolved transactions retain exclusive ownership of their target．
	entries, e := w.Entries(filepath.Join(outboxDir, "transactions"))
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return tx, e
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		b, e := w.Read(filepath.Join(outboxDir, "transactions", entry.Name()))
		if e != nil {
			return tx, e
		}
		var t transaction
		if e = json.Unmarshal(b, &t); e != nil {
			return tx, e
		}
		if t.Doc.Target.DocID == id {
			return tx, fmt.Errorf("conflict")
		}
	}
	adoption := Adoption{Mode: "policy", ActorID: g.ActorID, PolicyID: g.PolicyID, PolicyRevision: g.Revision, Decision: "scope_allowed"}
	if p.Operation == "create_record" {
		if exists {
			return tx, fmt.Errorf("conflict")
		}
		c := g.Records[p.Record.Type]
		r = Record{DocID: id, Project: g.Project, Kind: c.Kind, Sensitivity: c.Sensitivity, Tags: c.Tags, ProvenanceTypes: c.ProvenanceTypes, Authorship: "ephy", Meta: RecordMeta{SchemaVersion: Version, ScopeID: p.ScopeID, ProducerID: p.ProducerID, LogicalKey: p.LogicalKey, Revision: 1, Spec: p.Record, Adoption: adoption}}
		baseDir := filepath.Join("content", "projects", g.Project, c.Kind, p.Record.LocalDate[:7])
		name := "ephy-v2-" + id
		for n := 0; n < 1000; n++ {
			suffix := ""
			if n > 0 {
				suffix = fmt.Sprintf("-%d", n)
			}
			candidate := filepath.Join(baseDir, name+suffix+".md")
			h, e := w.CurrentHash(candidate)
			if e != nil {
				return tx, e
			}
			if h == nil {
				current.Path = candidate
				break
			}
		}
		if current.Path == "" {
			return tx, fmt.Errorf("collision_limit")
		}
	} else {
		if !exists || p.Target == nil || *p.Target != current.Target || current.ScopeID != p.ScopeID || current.ProducerID != p.ProducerID || current.HumanEdited {
			return tx, fmt.Errorf("conflict")
		}
		data, e := w.Read(current.Path)
		if e != nil {
			return tx, e
		}
		if canonical.Hash(data) != current.Target.SHA256 {
			return tx, fmt.Errorf("conflict")
		}
		r, e = Parse(data)
		if e != nil {
			return tx, e
		}
		if r.Meta.HumanEdited {
			return tx, fmt.Errorf("conflict")
		}
		a, _ := encodeCanonical(r.Meta.Spec)
		b, _ := encodeCanonical(p.Record)
		if string(a) != string(b) {
			return tx, fmt.Errorf("record_identity_changed")
		}
		// Existing classification is evaluated too，so a narrower new grant cannot
		// relabel a previously restricted document while appending．
		policy, e := loadPrivacy(w)
		if e != nil {
			return tx, e
		}
		decision, e := policy.Authorize(p.Actor, contextcore.CapabilityPropose, resource(g, r.Resource()))
		if e != nil || !decision.Allowed {
			return tx, fmt.Errorf("permission_denied")
		}
		c, _ := encodeCanonical(g.Records[p.Record.Type])
		old, _ := encodeCanonical(r.Resource())
		if string(c) != string(old) {
			return tx, fmt.Errorf("classification_changed")
		}
		h := current.Target.SHA256
		base = &h
		r.Meta.Revision++
		r.Meta.Adoption = adoption
	}
	if len(p.Events) == 1 {
		v := p.Events[0]
		if len(r.Events) >= 256 {
			return tx, fmt.Errorf("record_capacity")
		}
		maxSeq := int64(0)
		// Sequence order spans segments of one conversation．A gap is a pending
		// predecessor，not a completed conversation．
		for _, d := range l.Docs {
			if d.ScopeID != p.ScopeID || d.ProducerID != p.ProducerID {
				continue
			}
			data, e := w.Read(d.Path)
			if e != nil {
				return tx, e
			}
			if canonical.Hash(data) != d.Target.SHA256 {
				return tx, fmt.Errorf("conflict")
			}
			other, e := Parse(data)
			if e != nil {
				return tx, e
			}
			if other.Meta.Spec.Type != "conversation" || other.Meta.Spec.ConversationID != v.ConversationID {
				continue
			}
			for _, prior := range other.Events {
				if prior.Seq > maxSeq {
					maxSeq = prior.Seq
				}
			}
		}
		if v.Seq != maxSeq+1 {
			return tx, fmt.Errorf("event_sequence_gap")
		}
		if v.Corrects != nil {
			effective := map[string]int64{}
			for _, prior := range r.Events {
				effective[prior.EventID] = prior.Revision
				if prior.Corrects != nil {
					effective[prior.Corrects.EventID]++
				}
			}
			if effective[v.Corrects.EventID] != v.Corrects.EventRevision {
				return tx, fmt.Errorf("stale_correction")
			}
		}
		r.Events = append(r.Events, v)
	} else {
		if e := s.checkSources(w, l, g, p.Actor, p.Derivation.InputRefs); e != nil {
			return tx, e
		}
		r.Derivation = p.Derivation
	}
	result, e := Render(r)
	if e != nil {
		return tx, e
	}
	target := r.Target(result)
	receipt := Receipt{SchemaVersion: Version, CandidateID: p.CandidateID, ProposalHash: hash, Status: "accepted", Applied: target, EventIDs: []string{}, Adoption: adoption}
	for _, v := range p.Events {
		receipt.EventIDs = append(receipt.EventIDs, v.EventID)
	}
	state := DocState{Target: target, Path: filepath.ToSlash(current.Path), ScopeID: p.ScopeID, ProducerID: p.ProducerID, LogicalKey: p.LogicalKey}
	return transaction{SchemaVersion: Version, Proposal: p, Raw: raw, Base: base, Result: result, Doc: state, Receipt: receipt, Stage: "prepared"}, nil
}
func (s *Service) finish(w *canonical.Writer, tx *transaction) error {
	l, e := loadLedger(w)
	if e != nil {
		return e
	}
	_, committed := l.Candidates[tx.Proposal.CandidateID]
	if canonical.Hash(tx.Result) != tx.Doc.Target.SHA256 || tx.Receipt.Applied != tx.Doc.Target {
		return fmt.Errorf("invalid_transaction")
	}
	current, e := w.CurrentHash(tx.Doc.Path)
	if e != nil {
		return e
	}
	if !committed && (current == nil || *current != tx.Doc.Target.SHA256) {
		if !(current == nil && tx.Base == nil || current != nil && tx.Base != nil && *current == *tx.Base) {
			return fmt.Errorf("conflict")
		}
		if tx.Human {
			old, err := w.Read(tx.Doc.Path)
			if err != nil {
				return err
			}
			record, err := Parse(old)
			if err != nil {
				return err
			}
			if err = s.authorizeHuman(w, record, contextcore.CapabilityReview); err != nil {
				return err
			}
		} else {
			g, err := s.authorize(w, tx.Proposal, tx.Raw, contextcore.CapabilityPropose)
			if err != nil {
				return err
			}
			if tx.Proposal.Derivation != nil {
				if err = s.checkSources(w, l, g, tx.Proposal.Actor, tx.Proposal.Derivation.InputRefs); err != nil {
					return err
				}
			}
		}
		if e = purgeResponses(w); e != nil {
			return e
		}
		if e = w.WriteCAS(tx.Doc.Path, tx.Base, tx.Result, 0600); e != nil {
			return e
		}
		if e = s.fault("canonical"); e != nil {
			return e
		}
	}
	// When canonical bytes already match，this is completion of an earlier
	// commit．A later revocation cannot cause a second canonical write．
	if !committed {
		if e = w.WriteCAS(historyPath(tx.Doc.Target), nil, tx.Result, 0600); e != nil {
			b, re := w.Read(historyPath(tx.Doc.Target))
			if re != nil || canonical.Hash(b) != tx.Doc.Target.SHA256 {
				return e
			}
		}
		l.Docs[tx.Doc.Target.DocID] = tx.Doc
		l.Candidates[tx.Proposal.CandidateID] = tx.Receipt
		for _, v := range tx.Proposal.Events {
			b, e := encodeCanonical(v)
			if e != nil {
				return e
			}
			l.Events[eventKey(tx.Proposal.ScopeID, v.EventID)] = eventEffect{Hash: canonical.Hash(b), DocID: tx.Doc.Target.DocID, Receipt: tx.Receipt}
		}
		if e = saveLedger(w, l); e != nil {
			return e
		}
		if e = s.fault("ledger"); e != nil {
			return e
		}
	}
	tx.Stage = "saved"
	if e = saveJSON(w, txPath(tx.Proposal.CandidateID), tx); e != nil {
		return e
	}
	if e = s.fault("saved"); e != nil {
		return e
	}
	if e = saveJSON(w, receiptPath(tx.Proposal.CandidateID), tx.Receipt); e != nil {
		return e
	}
	if e = s.fault("receipt"); e != nil {
		return e
	}
	// Archive only a matching pending payload．Never erase an ID-reuse attempt．
	pending := filepath.Join(outboxDir, "pending", tx.Proposal.CandidateID+".json")
	if b, e := w.Read(pending); e == nil {
		signed, e := signingBytes(b)
		if e != nil || canonical.Hash(signed) != tx.Receipt.ProposalHash {
			return fmt.Errorf("id_reuse")
		}
		if e = w.Write(filepath.Join(outboxDir, "accepted", tx.Proposal.CandidateID+".json"), b, 0600); e != nil {
			return e
		}
		if e = w.Remove(pending); e != nil {
			return e
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	if e = s.fault("archive"); e != nil {
		return e
	}
	return w.Remove(txPath(tx.Proposal.CandidateID))
}
func (s *Service) recoverAll(w *canonical.Writer) ([]RecoveryResult, error) {
	results := []RecoveryResult{}
	entries, e := w.Entries(filepath.Join(outboxDir, "transactions"))
	if errors.Is(e, os.ErrNotExist) {
		return results, nil
	}
	if e != nil {
		return nil, e
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, f := range entries {
		if f.IsDir() {
			continue
		}
		var tx transaction
		b, e := w.Read(filepath.Join(outboxDir, "transactions", f.Name()))
		if e != nil {
			return results, e
		}
		if e = json.Unmarshal(b, &tx); e != nil {
			return results, e
		}
		if !validID(tx.Proposal.CandidateID) || f.Name() != tx.Proposal.CandidateID+".json" || tx.SchemaVersion != Version {
			return results, fmt.Errorf("invalid_transaction")
		}
		status := "recovered"
		if e = s.finish(w, &tx); e != nil {
			status = e.Error()
			if status != "conflict" && status != "permission_denied" && status != "configuration_required" && status != "stale_source" {
				return results, e
			}
		}
		results = append(results, RecoveryResult{CandidateID: tx.Proposal.CandidateID, Status: status})
	}
	return results, nil
}
func (s *Service) Recover() ([]RecoveryResult, error) {
	var r []RecoveryResult
	e := canonical.WithWriter(s.DataRoot, func(w *canonical.Writer) error { var e error; r, e = s.recoverAll(w); return e })
	return r, e
}
