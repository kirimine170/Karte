package ephyrecordsv2

import (
	"fmt"
	"karte/internal/canonical"
	"karte/internal/contextcore"
	"path/filepath"
	"sort"
	"strings"
)

func (s *Service) checkSources(w *canonical.Writer, l ledger, g Grant, actor contextcore.Actor, refs []SourceRef) error {
	policy, e := loadPrivacy(w)
	if e != nil {
		return e
	}
	for _, ref := range refs {
		d, ok := l.Docs[ref.DocID]
		if !ok || d.ScopeID != g.ScopeID || d.ProducerID != g.ProducerID || d.Target.Revision != ref.Revision || d.Target.SHA256 != ref.SHA256 {
			return fmt.Errorf("stale_source")
		}
		raw, e := w.Read(d.Path)
		if e != nil || canonical.Hash(raw) != ref.SHA256 {
			return fmt.Errorf("stale_source")
		}
		r, e := Parse(raw)
		if e != nil || r.Meta.Spec.Type != "conversation" || r.Meta.Spec.ConversationID != ref.ConversationID {
			return fmt.Errorf("stale_source")
		}
		decision, e := policy.Authorize(actor, contextcore.CapabilityRead, resource(g, r.Resource()))
		if e != nil || !decision.Allowed {
			return fmt.Errorf("permission_denied")
		}
		if _, ok := g.Records[r.Meta.Spec.Type]; !ok {
			return fmt.Errorf("permission_denied")
		}
		effective := map[string]int64{}
		turns := map[string]string{}
		for _, v := range r.Events {
			effective[v.EventID] = v.Revision
			turns[v.EventID] = v.TurnID
			if v.Corrects != nil {
				effective[v.Corrects.EventID]++
			}
		}
		usedTurns := map[string]bool{}
		seen := map[string]bool{}
		for _, refEvent := range ref.Events {
			if seen[refEvent.EventID] || effective[refEvent.EventID] != refEvent.EventRevision || !oneOf(turns[refEvent.EventID], ref.TurnIDs...) {
				return fmt.Errorf("stale_source")
			}
			seen[refEvent.EventID] = true
			usedTurns[turns[refEvent.EventID]] = true
		}
		for _, id := range ref.TurnIDs {
			if !usedTurns[id] {
				return fmt.Errorf("stale_source")
			}
		}
	}
	return nil
}

type Query struct {
	Text        string   `json:"text"`
	RecordTypes []string `json:"record_types"`
	Timezone    string   `json:"timezone,omitempty"`
	DateFrom    string   `json:"date_from,omitempty"`
	DateTo      string   `json:"date_to,omitempty"`
	Limit       int      `json:"limit"`
}
type Request struct {
	ProtocolVersion string            `json:"protocol_version"`
	RequestID       string            `json:"request_id"`
	Operation       string            `json:"operation"`
	ScopeID         string            `json:"scope_id"`
	ProducerID      string            `json:"producer_instance_id"`
	Actor           contextcore.Actor `json:"actor"`
	PolicyID        string            `json:"policy_id"`
	PolicyRevision  int64             `json:"policy_revision"`
	ConsentEpoch    int64             `json:"consent_epoch"`
	ScopeGeneration int64             `json:"scope_generation"`
	CreatedAt       string            `json:"created_at"`
	Auth            Auth              `json:"auth"`
	Query           *Query            `json:"query"`
	Target          *Target           `json:"target"`
}
type ReadResult struct {
	Target     Target      `json:"target"`
	Record     RecordSpec  `json:"record"`
	ScopeID    string      `json:"scope_id"`
	State      string      `json:"state"`
	SourceRefs []SourceRef `json:"source_refs"`
	Markdown   string      `json:"markdown,omitempty"`
	Events     []Event     `json:"events,omitempty"`
	Derivation *Derivation `json:"derivation,omitempty"`
}
type Response struct {
	ProtocolVersion string       `json:"protocol_version"`
	RequestID       string       `json:"request_id"`
	Status          string       `json:"status"`
	Results         []ReadResult `json:"results"`
}

func (q Request) Validate() error {
	if q.ProtocolVersion != Version {
		return fmt.Errorf("unsupported_protocol")
	}
	if !validID(q.RequestID) || !validID(q.ScopeID) || !validID(q.ProducerID) || !validID(q.PolicyID) || q.PolicyRevision < 1 || q.ConsentEpoch < 1 || q.ScopeGeneration < 1 || q.Actor.Type != "ephy" || !token.MatchString(q.Actor.ID) || !token.MatchString(q.Auth.KeyID) || !digest.MatchString(q.Auth.MAC) || !timestamp(q.CreatedAt) {
		return fmt.Errorf("invalid_request")
	}
	switch q.Operation {
	case "read":
		if q.Query != nil || q.Target == nil || !validID(q.Target.DocID) || q.Target.Revision < 1 || !digest.MatchString(q.Target.SHA256) {
			return fmt.Errorf("invalid_target")
		}
	case "search":
		if q.Target != nil || q.Query == nil || len(q.Query.Text) > 2048 || q.Query.Limit < 1 || q.Query.Limit > 100 || len(q.Query.RecordTypes) == 0 || len(q.Query.RecordTypes) > 3 {
			return fmt.Errorf("invalid_query")
		}
		for _, typ := range q.Query.RecordTypes {
			if !oneOf(typ, "conversation", "summary", "ephy_diary") {
				return fmt.Errorf("invalid_query")
			}
		}
	default:
		return fmt.Errorf("unsupported_operation")
	}
	return nil
}
func (s *Service) Query(raw []byte) (Response, error) {
	var q Request
	response := Response{ProtocolVersion: Version, Status: "not_available", Results: []ReadResult{}}
	if e := checkVersion(raw, "protocol_version"); e != nil {
		return response, e
	}
	if e := strict(raw, &q); e != nil {
		return response, e
	}
	response.RequestID = q.RequestID
	if e := q.validateWire(raw); e != nil {
		return response, e
	}
	e := canonical.WithWriter(s.DataRoot, func(w *canonical.Writer) error { var e error; response, e = s.queryLocked(w, q, raw); return e })
	return response, e
}
func (s *Service) queryLocked(w *canonical.Writer, q Request, raw []byte) (Response, error) {
	response := Response{ProtocolVersion: Version, RequestID: q.RequestID, Status: "not_available", Results: []ReadResult{}}
	p := Proposal{ScopeID: q.ScopeID, ProducerID: q.ProducerID, Actor: q.Actor, PolicyID: q.PolicyID, PolicyRevision: q.PolicyRevision, ConsentEpoch: q.ConsentEpoch, ScopeGeneration: q.ScopeGeneration, Auth: q.Auth}
	run := func() error {
		cap := contextcore.CapabilityRead
		if q.Operation == "search" {
			cap = contextcore.CapabilitySearch
		}
		g, e := s.authorize(w, p, raw, cap)
		if e != nil {
			return fmt.Errorf("not_available")
		}
		l, e := loadLedger(w)
		if e != nil {
			return e
		}
		policy, e := loadPrivacy(w)
		if e != nil {
			return e
		}
		ids := make([]string, 0, len(l.Docs))
		if q.Target != nil {
			ids = append(ids, q.Target.DocID)
		} else {
			for id := range l.Docs {
				ids = append(ids, id)
			}
			sort.Strings(ids)
		}
		for _, id := range ids {
			d, ok := l.Docs[id]
			if !ok || d.ScopeID != g.ScopeID || d.ProducerID != g.ProducerID {
				continue
			}
			current, e := w.Read(d.Path)
			if e != nil || canonical.Hash(current) != d.Target.SHA256 {
				continue
			}
			r, e := Parse(current)
			if e != nil {
				continue
			}
			decision, e := policy.Authorize(q.Actor, cap, resource(g, r.Resource()))
			if e != nil || !decision.Allowed {
				continue
			}
			if _, ok = g.Records[r.Meta.Spec.Type]; !ok {
				continue
			}
			target := d.Target
			data := current
			if q.Target != nil && *q.Target != d.Target {
				if q.Target.Revision >= d.Target.Revision {
					continue
				}
				data, e = w.Read(historyPath(*q.Target))
				if e != nil || canonical.Hash(data) != q.Target.SHA256 {
					continue
				}
				r, e = Parse(data)
				if e != nil || r.DocID != d.Target.DocID || r.Meta.ScopeID != g.ScopeID {
					continue
				}
				target = *q.Target
				decision, e = policy.Authorize(q.Actor, cap, resource(g, r.Resource()))
				if e != nil || !decision.Allowed {
					continue
				}
			}
			state := "active"
			refs := []SourceRef{}
			if r.Derivation != nil {
				refs = r.Derivation.InputRefs
				if e = s.checkSources(w, l, g, q.Actor, refs); e != nil {
					state = "stale"
				}
			}
			if q.Query != nil {
				query := q.Query
				spec := r.Meta.Spec
				if state != "active" || !oneOf(spec.Type, query.RecordTypes...) || query.Timezone != "" && query.Timezone != spec.Timezone || query.DateFrom != "" && spec.LocalDate < query.DateFrom || query.DateTo != "" && spec.LocalDate > query.DateTo || !strings.Contains(strings.ToLower(string(data)), strings.ToLower(query.Text)) {
					continue
				}
			}
			result := ReadResult{Target: target, Record: r.Meta.Spec, ScopeID: g.ScopeID, State: state, SourceRefs: refs}
			if q.Operation == "read" {
				result.Markdown = string(data)
				result.Events = r.Events
				result.Derivation = r.Derivation
			}
			response.Results = append(response.Results, result)
			if q.Query != nil && len(response.Results) >= q.Query.Limit {
				break
			}
		}
		if len(response.Results) > 0 || q.Operation == "search" {
			response.Status = "ok"
		}
		return nil
	}
	err := run()
	return response, err
}

func (s *Service) authorizeHuman(w *canonical.Writer, r Record, capability contextcore.Capability) error {
	all, e := s.loadRegistrations()
	if e != nil {
		return e
	}
	reg, ok := all.Scopes[r.Meta.ScopeID]
	if !ok || reg.Grant.ProducerID != r.Meta.ProducerID {
		return fmt.Errorf("not_available")
	}
	policy, e := loadPrivacy(w)
	if e != nil {
		return e
	}
	decision, e := policy.Authorize(contextcore.Actor{Type: "human", ID: "local-human"}, capability, resource(reg.Grant, r.Resource()))
	if e != nil || !decision.Allowed {
		return fmt.Errorf("not_available")
	}
	return nil
}
func (s *Service) LoadHuman(w *canonical.Writer, path string) (bool, string, error) {
	l, e := loadLedger(w)
	if e != nil {
		return true, "", e
	}
	for _, d := range l.Docs {
		if filepath.Clean(path) != filepath.Clean(d.Path) {
			continue
		}
		raw, e := w.Read(d.Path)
		if e != nil {
			return true, "", e
		}
		if canonical.Hash(raw) != d.Target.SHA256 {
			return true, "", fmt.Errorf("conflict")
		}
		r, e := Parse(raw)
		if e != nil {
			return true, "", e
		}
		if e = s.authorizeHuman(w, r, contextcore.CapabilityRead); e != nil {
			return true, "", e
		}
		return true, string(raw), nil
	}
	return false, "", nil
}

func (q Request) validateWire(raw []byte) error {
	if e := requireWireFields(raw, []string{"protocol_version", "request_id", "operation", "scope_id", "producer_instance_id", "actor", "policy_id", "policy_revision", "consent_epoch", "scope_generation", "created_at", "auth", "query", "target"}, "/target", "/query"); e != nil {
		return e
	}
	return q.Validate()
}
