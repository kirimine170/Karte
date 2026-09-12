// Package ephyrecordsv2 owns the opt-in Runtime record protocol．It never calls
// site build，Git commit，training or an external transport．
package ephyrecordsv2

import (
	"fmt"
	"github.com/google/uuid"
	"karte/internal/contextcore"
	"regexp"
	"strings"
	"time"
)

const Version = "2.0"
const MaxRecordBytes = 1 << 20
const MaxEventBytes = 64 << 10
const stateDir = ".mdsys/ephy/records/v2"
const outboxDir = ".mdsys/ephy/outbox/v2"
const contextDir = ".mdsys/context/v2"

var token = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var projectToken = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
var digest = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validID(s string) bool {
	u, e := uuid.Parse(s)
	return e == nil && u != uuid.Nil && u.String() == s
}
func timestamp(s string) bool { _, e := time.Parse(time.RFC3339, s); return e == nil }

type Auth struct {
	KeyID string `json:"key_id"`
	MAC   string `json:"mac"`
}
type Target struct {
	DocID    string `json:"doc_id"`
	Revision int64  `json:"revision"`
	SHA256   string `json:"sha256"`
}
type EventRef struct {
	EventID       string `json:"event_id"`
	EventRevision int64  `json:"event_revision"`
}
type SourceRef struct {
	DocID          string     `json:"doc_id"`
	Revision       int64      `json:"revision"`
	SHA256         string     `json:"sha256"`
	ConversationID string     `json:"conversation_id"`
	TurnIDs        []string   `json:"turn_ids"`
	Events         []EventRef `json:"events"`
}
type ASR struct {
	Provider      string `json:"provider"`
	ModelRevision string `json:"model_revision"`
	FinalRevision int64  `json:"final_revision"`
}
type SpeechUnit struct {
	UnitID string `json:"unit_id"`
	State  string `json:"state"`
}
type Assistant struct {
	Generation  string       `json:"generation"`
	Display     string       `json:"display"`
	Playback    string       `json:"playback"`
	SpeechUnits []SpeechUnit `json:"speech_units"`
}
type Event struct {
	ConversationID   string     `json:"conversation_id"`
	ScopeID          string     `json:"scope_id"`
	ProducerID       string     `json:"producer_instance_id"`
	EventID          string     `json:"event_id"`
	Seq              int64      `json:"event_seq"`
	Revision         int64      `json:"event_revision"`
	TurnID           string     `json:"turn_id"`
	Type             string     `json:"event_type"`
	InputKind        string     `json:"input_kind,omitempty"`
	Text             string     `json:"text"`
	OccurredAt       string     `json:"occurred_at"`
	Timezone         string     `json:"timezone"`
	LocalDate        string     `json:"local_date"`
	ASR              *ASR       `json:"asr,omitempty"`
	Assistant        *Assistant `json:"assistant,omitempty"`
	ConsentEpoch     int64      `json:"consent_epoch"`
	Corrects         *EventRef  `json:"corrects,omitempty"`
	CorrectionReason string     `json:"correction_reason,omitempty"`
}
type Claim struct {
	Class      string      `json:"class"`
	Text       string      `json:"text"`
	SourceRefs []SourceRef `json:"source_refs"`
}
type Derivation struct {
	Kind             string      `json:"kind"`
	InputRefs        []SourceRef `json:"input_refs"`
	ModelID          string      `json:"model_id"`
	ModelRevision    string      `json:"model_revision"`
	TemplateID       string      `json:"template_id"`
	TemplateRevision string      `json:"template_revision"`
	GeneratedAt      string      `json:"generated_at"`
	JobID            string      `json:"job_id"`
	Claims           []Claim     `json:"claims"`
}
type RecordSpec struct {
	Type           string `json:"record_type"`
	Title          string `json:"title"`
	ConversationID string `json:"conversation_id,omitempty"`
	Segment        int64  `json:"segment_no,omitempty"`
	Timezone       string `json:"timezone"`
	LocalDate      string `json:"local_date"`
}
type Proposal struct {
	SchemaVersion   string            `json:"schema_version"`
	CandidateID     string            `json:"candidate_id"`
	Operation       string            `json:"operation"`
	LogicalKey      string            `json:"logical_record_key"`
	ScopeID         string            `json:"scope_id"`
	ProducerID      string            `json:"producer_instance_id"`
	Actor           contextcore.Actor `json:"actor"`
	PolicyID        string            `json:"policy_id"`
	PolicyRevision  int64             `json:"policy_revision"`
	ConsentEpoch    int64             `json:"consent_epoch"`
	ScopeGeneration int64             `json:"scope_generation"`
	Record          RecordSpec        `json:"record"`
	Target          *Target           `json:"target"`
	Events          []Event           `json:"events,omitempty"`
	Derivation      *Derivation       `json:"derivation,omitempty"`
	CreatedAt       string            `json:"created_at"`
	Auth            Auth              `json:"auth"`
}
type Adoption struct {
	Mode           string `json:"mode"`
	ActorID        string `json:"actor_id"`
	PolicyID       string `json:"policy_id"`
	PolicyRevision int64  `json:"policy_revision"`
	Decision       string `json:"decision"`
}
type Receipt struct {
	SchemaVersion string   `json:"schema_version"`
	CandidateID   string   `json:"candidate_id"`
	ProposalHash  string   `json:"proposal_hash"`
	Status        string   `json:"status"`
	Applied       Target   `json:"applied"`
	EventIDs      []string `json:"applied_event_ids"`
	Adoption      Adoption `json:"adoption"`
}

func (p Proposal) Validate() error {
	if p.SchemaVersion != Version {
		return fmt.Errorf("unsupported_protocol")
	}
	if !validID(p.CandidateID) || !validID(p.ScopeID) || !validID(p.ProducerID) || !validID(p.PolicyID) || !token.MatchString(p.Actor.ID) || p.Actor.Type != "ephy" || !token.MatchString(p.Auth.KeyID) || !digest.MatchString(p.Auth.MAC) || p.PolicyRevision < 1 || p.ConsentEpoch < 1 || p.ScopeGeneration < 1 || !timestamp(p.CreatedAt) {
		return fmt.Errorf("invalid_envelope")
	}
	s := p.Record
	loc, e := time.LoadLocation(s.Timezone)
	date, de := time.Parse("2006-01-02", s.LocalDate)
	if e != nil || de != nil || date.Format("2006-01-02") != s.LocalDate || len(s.Title) == 0 || len(s.Title) > 512 || strings.ContainsAny(s.Title, "\r\n\x00") {
		return fmt.Errorf("invalid_record")
	}
	expected := ""
	switch s.Type {
	case "conversation":
		if !validID(s.ConversationID) || s.Segment < 1 {
			return fmt.Errorf("invalid_record")
		}
		expected = fmt.Sprintf("conversation:%s:%d", s.ConversationID, s.Segment)
	case "summary":
		if !validID(s.ConversationID) || s.Segment != 0 {
			return fmt.Errorf("invalid_record")
		}
		expected = "summary:" + s.ConversationID + ":" + p.ScopeID
	case "ephy_diary":
		if s.ConversationID != "" || s.Segment != 0 {
			return fmt.Errorf("invalid_record")
		}
		expected = "diary:" + p.ProducerID + ":" + p.ScopeID + ":" + s.Timezone + ":" + s.LocalDate
	default:
		return fmt.Errorf("invalid_record_type")
	}
	if p.LogicalKey != expected {
		return fmt.Errorf("invalid_logical_key")
	}
	if p.Operation == "create_record" {
		if p.Target != nil {
			return fmt.Errorf("invalid_target")
		}
	} else if p.Operation == "append_events" || p.Operation == "revise_derivation" {
		if p.Target == nil || !validID(p.Target.DocID) || p.Target.Revision < 1 || !digest.MatchString(p.Target.SHA256) {
			return fmt.Errorf("invalid_target")
		}
	} else {
		return fmt.Errorf("unsupported_operation")
	}
	if s.Type == "conversation" {
		if len(p.Events) != 1 || p.Derivation != nil || p.Operation == "revise_derivation" {
			return fmt.Errorf("invalid_event_count")
		}
		v := p.Events[0]
		if !validID(v.EventID) || !validID(v.TurnID) || v.ConversationID != s.ConversationID || v.ScopeID != p.ScopeID || v.ProducerID != p.ProducerID || v.Seq < 1 || v.Revision != 1 || v.ConsentEpoch != p.ConsentEpoch || v.Timezone != s.Timezone || v.LocalDate != s.LocalDate || len(v.Text) > MaxEventBytes || !timestamp(v.OccurredAt) {
			return fmt.Errorf("invalid_event")
		}
		instant, _ := time.Parse(time.RFC3339, v.OccurredAt)
		if instant.In(loc).Format("2006-01-02") != s.LocalDate {
			return fmt.Errorf("invalid_event_date")
		}
		switch v.Type {
		case "user_final":
			if v.InputKind != "text" && v.InputKind != "asr_final" {
				return fmt.Errorf("invalid_input_kind")
			}
			if v.InputKind == "asr_final" && (v.ASR == nil || !token.MatchString(v.ASR.Provider) || !token.MatchString(v.ASR.ModelRevision) || v.ASR.FinalRevision < 1) {
				return fmt.Errorf("invalid_asr")
			}
			if v.Assistant != nil || v.Corrects != nil {
				return fmt.Errorf("invalid_event")
			}
		case "assistant_result":
			if v.Assistant == nil || v.ASR != nil || v.InputKind != "" || v.Corrects != nil {
				return fmt.Errorf("invalid_assistant")
			}
			a := v.Assistant
			if !oneOf(a.Generation, "completed", "canceled", "failed") || !oneOf(a.Display, "confirmed_full", "confirmed_prefix", "none") || !oneOf(a.Playback, "completed", "interrupted", "failed", "unknown", "not_started") || len(a.SpeechUnits) > 256 {
				return fmt.Errorf("invalid_assistant")
			}
			seen := map[string]bool{}
			for _, u := range a.SpeechUnits {
				if !token.MatchString(u.UnitID) || seen[u.UnitID] || !oneOf(u.State, "started", "completed", "interrupted", "unknown") {
					return fmt.Errorf("invalid_playback")
				}
				seen[u.UnitID] = true
			}
		case "correction":
			if v.Corrects == nil || !validID(v.Corrects.EventID) || v.Corrects.EventRevision < 1 || !oneOf(v.CorrectionReason, "asr_error", "user_content") || v.Assistant != nil || v.ASR != nil {
				return fmt.Errorf("invalid_correction")
			}
		default:
			return fmt.Errorf("unsupported_event_type")
		}
	} else {
		d := p.Derivation
		if d == nil || len(p.Events) != 0 || p.Operation == "append_events" || d.Kind != s.Type || len(d.InputRefs) == 0 || len(d.InputRefs) > 64 || !token.MatchString(d.ModelID) || !token.MatchString(d.ModelRevision) || !token.MatchString(d.TemplateID) || !token.MatchString(d.TemplateRevision) || !timestamp(d.GeneratedAt) || !validID(d.JobID) || len(d.Claims) == 0 || len(d.Claims) > 256 {
			return fmt.Errorf("invalid_derivation")
		}
		for _, r := range d.InputRefs {
			if e := r.Validate(); e != nil {
				return e
			}
		}
		for _, c := range d.Claims {
			if !oneOf(c.Class, "observed_utterance", "user_report", "ephy_interpretation") || len(c.Text) == 0 || len(c.Text) > MaxEventBytes || len(c.SourceRefs) == 0 {
				return fmt.Errorf("invalid_claim")
			}
			for _, r := range c.SourceRefs {
				if e := r.Validate(); e != nil {
					return e
				}
				found := false
				for _, i := range d.InputRefs {
					a, _ := encodeCanonical(i)
					b, _ := encodeCanonical(r)
					if string(a) == string(b) {
						found = true
					}
				}
				if !found {
					return fmt.Errorf("claim_source_not_input")
				}
			}
		}
	}
	return nil
}
func (r SourceRef) Validate() error {
	if !validID(r.DocID) || r.Revision < 1 || !digest.MatchString(r.SHA256) || !validID(r.ConversationID) || len(r.TurnIDs) == 0 || len(r.TurnIDs) > 256 || len(r.Events) == 0 || len(r.Events) > 256 {
		return fmt.Errorf("invalid_source")
	}
	for _, t := range r.TurnIDs {
		if !validID(t) {
			return fmt.Errorf("invalid_source")
		}
	}
	for _, e := range r.Events {
		if !validID(e.EventID) || e.EventRevision < 1 {
			return fmt.Errorf("invalid_source")
		}
	}
	return nil
}
func oneOf(s string, values ...string) bool {
	for _, v := range values {
		if s == v {
			return true
		}
	}
	return false
}
