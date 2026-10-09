// Package worklog owns append-only task observations and derived daily reports.
package worklog

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const Version = "1.0"
const maxRecordBytes = 2 << 20

var token = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)
var projectToken = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
var ErrDenied = errors.New("worklog_permission_denied")
var ErrConflict = errors.New("worklog_conflict")

type Config struct {
	Version     string `json:"schema_version"`
	Enabled     bool   `json:"enabled"`
	Actor       string `json:"actor"`
	Project     string `json:"project"`
	Timezone    string `json:"timezone"`
	Sensitivity string `json:"sensitivity"`
}

type Evidence struct {
	Reference string `json:"reference"`
	SHA256    string `json:"sha256,omitempty"`
}

// EventInput is caller testimony. Origin and receipt time are assigned by the host.
type EventInput struct {
	EventID    string     `json:"event_id"`
	Kind       string     `json:"kind"`
	Text       string     `json:"text"`
	OccurredAt string     `json:"occurred_at,omitempty"`
	Outcome    string     `json:"outcome,omitempty"`
	NextAction string     `json:"next_action,omitempty"`
	Evidence   []Evidence `json:"evidence,omitempty"`
}

type Event struct {
	EventInput
	Sequence   int    `json:"sequence"`
	RecordedAt string `json:"recorded_at"`
	Origin     string `json:"origin"`
}

type StartInput struct {
	TaskID       string `json:"task_id"`
	EventID      string `json:"event_id"`
	Title        string `json:"title"`
	Objective    string `json:"objective"`
	Repository   string `json:"repository,omitempty"`
	BaseRevision string `json:"base_revision,omitempty"`
}

type Task struct {
	Version      string  `json:"schema_version"`
	TaskID       string  `json:"task_id"`
	DocID        string  `json:"doc_id"`
	Project      string  `json:"project"`
	Actor        string  `json:"actor"`
	Timezone     string  `json:"timezone"`
	Sensitivity  string  `json:"sensitivity"`
	Title        string  `json:"title"`
	Objective    string  `json:"objective"`
	Repository   string  `json:"repository,omitempty"`
	BaseRevision string  `json:"base_revision,omitempty"`
	Status       string  `json:"status"`
	Events       []Event `json:"events"`
	ViewBaseHash *string `json:"view_base_hash"`
}

type Receipt struct {
	TaskID     string `json:"task_id"`
	DocID      string `json:"doc_id"`
	Sequence   int    `json:"sequence"`
	Status     string `json:"status"`
	Recorded   bool   `json:"recorded"`
	ViewsReady bool   `json:"views_ready"`
	Duplicate  bool   `json:"duplicate"`
	SHA256     string `json:"sha256"`
}

type Daily struct {
	Version      string     `json:"schema_version"`
	Date         string     `json:"date"`
	Timezone     string     `json:"timezone"`
	DocID        string     `json:"doc_id"`
	Sources      []Evidence `json:"sources"`
	Body         string     `json:"body"`
	ViewBaseHash *string    `json:"view_base_hash"`
}

func decode(raw []byte, out any) error {
	if len(raw) > maxRecordBytes || !utf8.Valid(raw) {
		return errors.New("invalid_worklog_json")
	}
	// Reject duplicate keys at every depth, including embedded event objects.
	d := json.NewDecoder(bytes.NewReader(raw))
	var walk func() error
	walk = func() error {
		t, err := d.Token()
		if err != nil {
			return err
		}
		if delim, ok := t.(json.Delim); ok {
			switch delim {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					key, err := d.Token()
					if err != nil {
						return err
					}
					name, ok := key.(string)
					if !ok || seen[name] {
						return errors.New("duplicate_json_key")
					}
					seen[name] = true
					if err := walk(); err != nil {
						return err
					}
				}
			case '[':
				for d.More() {
					if err := walk(); err != nil {
						return err
					}
				}
			default:
				return errors.New("invalid_json_delimiter")
			}
			_, err = d.Token()
			return err
		}
		return nil
	}
	if err := walk(); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("trailing_json")
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(out)
}

func Decode(raw []byte, out any) error { return decode(raw, out) }
func textOK(s string, limit int) bool {
	return utf8.ValidString(s) && len(s) <= limit && !strings.ContainsRune(s, '\x00')
}
func (c Config) validate() error {
	if c.Version != Version || !token.MatchString(c.Actor) || !projectToken.MatchString(c.Project) || (c.Sensitivity != "internal" && c.Sensitivity != "confidential" && c.Sensitivity != "restricted") {
		return errors.New("invalid_worklog_config")
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return errors.New("invalid_worklog_timezone")
	}
	if c.Timezone == "" || c.Timezone == "Local" {
		return errors.New("invalid_worklog_timezone")
	}
	return nil
}
func (e EventInput) validate() error {
	if !token.MatchString(e.EventID) || !textOK(e.Text, 16384) || strings.TrimSpace(e.Text) == "" || !textOK(e.NextAction, 4096) || len(e.Evidence) > 16 {
		return errors.New("invalid_worklog_event")
	}
	switch e.Kind {
	case "checkpoint", "tool", "verification", "failure", "note", "finish", "resume":
	default:
		return errors.New("invalid_worklog_event_kind")
	}
	if e.Kind == "finish" {
		switch e.Outcome {
		case "completed", "failed", "cancelled", "paused":
		default:
			return errors.New("invalid_worklog_outcome")
		}
	} else if e.Outcome != "" {
		return errors.New("unexpected_worklog_outcome")
	}
	if e.OccurredAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, e.OccurredAt); err != nil {
			return errors.New("invalid_worklog_timestamp")
		}
	}
	for _, ref := range e.Evidence {
		if !textOK(ref.Reference, 1024) || strings.TrimSpace(ref.Reference) == "" {
			return errors.New("invalid_worklog_evidence")
		}
		if ref.SHA256 != "" && !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(ref.SHA256) {
			return errors.New("invalid_worklog_evidence_hash")
		}
	}
	return nil
}

func statusAfter(status string, e EventInput) (string, error) {
	if e.Kind == "note" {
		return status, nil
	}
	if e.Kind == "resume" {
		if status != "paused" {
			return "", errors.New("worklog_not_paused")
		}
		return "running", nil
	}
	if status != "running" {
		return "", errors.New("worklog_task_closed")
	}
	if e.Kind == "finish" {
		return e.Outcome, nil
	}
	return status, nil
}
