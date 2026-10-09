package worklog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"karte/internal/canonical"
	"karte/internal/contextcore"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

const stateRoot = ".mdsys/worklog/v1"
const configPath = stateRoot + "/config.json"

type Service struct {
	Root   string
	Origin string
	Now    func() time.Time
}

func New(root, origin string) (*Service, error) {
	if root == "" || (origin != "model_report" && origin != "host_observation") {
		return nil, errors.New("invalid_worklog_binding")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return nil, errors.New("invalid_worklog_root")
	}
	s := &Service{Root: abs, Origin: origin, Now: time.Now}
	err = canonical.WithWriter(abs, func(w *canonical.Writer) error { _, _, err := s.authorize(w, contextcore.CapabilityRead); return err })
	return s, err
}

// Initialize is a human/controller setup operation, never an MCP tool.
// A new empty root gets a narrow privacy policy; an existing policy is retained.
func Initialize(root string, config Config) error {
	if err := config.validate(); err != nil {
		return err
	}
	return canonical.WithWriter(root, func(w *canonical.Writer) error {
		if _, err := w.Read(configPath); !errors.Is(err, os.ErrNotExist) {
			return errors.New("worklog_already_configured")
		}
		policyPath := ".mdsys/context/v1/policy.json"
		if _, err := w.Read(policyPath); errors.Is(err, os.ErrNotExist) {
			policy := contextcore.Policy{ProtocolVersion: contextcore.ProtocolVersion, Actors: map[string]contextcore.ActorPolicy{config.Actor: {
				SensitivityCeiling: config.Sensitivity, Projects: []string{config.Project}, ProvenanceTypes: []string{"canonical"},
				Capabilities: []contextcore.Capability{contextcore.CapabilitySearch, contextcore.CapabilityRead, contextcore.CapabilityPropose},
			}}}
			raw, err := json.Marshal(policy)
			if err != nil {
				return err
			}
			if err := w.WriteCAS(policyPath, nil, raw, 0600); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		raw, _ := json.Marshal(config)
		if err := w.WriteCAS(configPath, nil, raw, 0600); err != nil {
			return err
		}
		return nil
	})
}

func (s *Service) authorize(w *canonical.Writer, cap contextcore.Capability) (Config, contextcore.Policy, error) {
	var c Config
	raw, err := w.ReadLimit(configPath, 16384)
	if err != nil {
		return c, contextcore.Policy{}, ErrDenied
	}
	if decode(raw, &c) != nil || c.validate() != nil || !c.Enabled {
		return c, contextcore.Policy{}, ErrDenied
	}
	raw, err = w.ReadLimit(".mdsys/context/v1/policy.json", 65536)
	if err != nil {
		return c, contextcore.Policy{}, ErrDenied
	}
	var checked contextcore.Policy
	if decode(raw, &checked) != nil {
		return c, checked, ErrDenied
	}
	p, err := contextcore.ParsePolicy(raw)
	if err != nil {
		return c, p, ErrDenied
	}
	decision, err := p.Authorize(contextcore.Actor{Type: "tool", ID: c.Actor}, cap, contextcore.Resource{Project: c.Project, Kind: "note", Tags: []string{"ephy:worklog"}, Sensitivity: c.Sensitivity, ProvenanceTypes: []string{"canonical"}})
	if err != nil || !decision.Allowed {
		return c, p, ErrDenied
	}
	return c, p, nil
}

func taskPath(id string) string { return stateRoot + "/tasks/" + id + ".json" }
func taskView(t Task) string {
	return fmt.Sprintf("content/projects/%s/note/worklog-%s.md", t.Project, t.TaskID)
}
func dailyView(c Config, date string) string {
	return fmt.Sprintf("content/projects/%s/journal/worklog-daily-%s.md", c.Project, date)
}
func digestID(parts ...string) string {
	h := canonical.Hash([]byte(strings.Join(parts, "\x00")))
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[:8], h[8:12], h[12:16], h[16:20], h[20:32])
}

func loadTask(w *canonical.Writer, c Config, id string) (Task, []byte, error) {
	var t Task
	if !token.MatchString(id) {
		return t, nil, errors.New("invalid_worklog_task_id")
	}
	raw, err := w.ReadLimit(taskPath(id), maxRecordBytes)
	if err != nil {
		return t, nil, err
	}
	if decode(raw, &t) != nil || t.Version != Version || t.TaskID != id || t.Project != c.Project || t.Actor != c.Actor || t.Timezone != c.Timezone || t.Sensitivity != c.Sensitivity || t.DocID != digestID(c.Project, id) || len(t.Events) == 0 || len(t.Events) > 4096 {
		return t, nil, errors.New("invalid_worklog_record")
	}
	if !textOK(t.Title, 512) || strings.TrimSpace(t.Title) == "" || !textOK(t.Objective, 8192) || strings.TrimSpace(t.Objective) == "" || !textOK(t.Repository, 1024) || !textOK(t.BaseRevision, 128) {
		return t, nil, errors.New("invalid_worklog_record")
	}
	seen := map[string]bool{}
	status := "running"
	previousTime := time.Time{}
	for i, event := range t.Events {
		if event.Sequence != i+1 || seen[event.EventID] || !token.MatchString(event.EventID) || (event.Origin != "model_report" && event.Origin != "host_observation") {
			return t, nil, errors.New("invalid_worklog_record")
		}
		seen[event.EventID] = true
		when, err := time.Parse(time.RFC3339Nano, event.RecordedAt)
		if err != nil || when.Before(previousTime) {
			return t, nil, errors.New("invalid_worklog_record")
		}
		previousTime = when
		if i == 0 {
			if event.Kind != "start" || event.Text != t.Objective || event.OccurredAt != "" || event.Outcome != "" || event.NextAction != "" || len(event.Evidence) != 0 {
				return t, nil, errors.New("invalid_worklog_record")
			}
		} else {
			if event.EventInput.validate() != nil {
				return t, nil, errors.New("invalid_worklog_record")
			}
			status, err = statusAfter(status, event.EventInput)
			if err != nil {
				return t, nil, errors.New("invalid_worklog_record")
			}
		}
	}
	if status != t.Status {
		return t, nil, errors.New("invalid_worklog_record")
	}
	return t, raw, nil
}

func (s *Service) Start(in StartInput) (Receipt, error) {
	if !token.MatchString(in.TaskID) || !token.MatchString(in.EventID) || !textOK(in.Title, 512) || strings.TrimSpace(in.Title) == "" || !textOK(in.Objective, 8192) || strings.TrimSpace(in.Objective) == "" || !textOK(in.Repository, 1024) || !textOK(in.BaseRevision, 128) {
		return Receipt{}, errors.New("invalid_worklog_start")
	}
	var result Receipt
	err := canonical.WithWriter(s.Root, func(w *canonical.Writer) error {
		c, _, err := s.authorize(w, contextcore.CapabilityPropose)
		if err != nil {
			return err
		}
		t, raw, err := loadTask(w, c, in.TaskID)
		if err == nil {
			if t.Title != in.Title || t.Objective != in.Objective || t.Repository != in.Repository || t.BaseRevision != in.BaseRevision || t.Events[0].EventID != in.EventID || t.Events[0].Origin != s.Origin {
				return ErrConflict
			}
			result, err = s.materialize(w, c, t, raw, true, eventDate(t.Events[0], c.Timezone))
			return err
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		entries, err := w.Entries(stateRoot + "/tasks")
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if len(entries) >= 4096 {
			return errors.New("worklog_capacity")
		}
		t = Task{Version: Version, TaskID: in.TaskID, DocID: digestID(c.Project, in.TaskID), Project: c.Project, Actor: c.Actor, Timezone: c.Timezone, Sensitivity: c.Sensitivity, Title: in.Title, Objective: in.Objective, Repository: in.Repository, BaseRevision: in.BaseRevision, Status: "running", Events: []Event{{EventInput: EventInput{EventID: in.EventID, Kind: "start", Text: in.Objective}, Sequence: 1, Origin: s.Origin, RecordedAt: s.Now().UTC().Format(time.RFC3339Nano)}}}
		raw, err = json.Marshal(t)
		if err != nil {
			return err
		}
		if err := w.WriteCAS(taskPath(t.TaskID), nil, raw, 0600); err != nil {
			return err
		}
		result, err = s.materialize(w, c, t, raw, false)
		return err
	})
	return result, err
}

func (s *Service) Append(id string, in EventInput) (Receipt, error) {
	if len(in.Evidence) == 0 {
		in.Evidence = nil
	}
	if err := in.validate(); err != nil {
		return Receipt{}, err
	}
	var result Receipt
	err := canonical.WithWriter(s.Root, func(w *canonical.Writer) error {
		c, _, err := s.authorize(w, contextcore.CapabilityPropose)
		if err != nil {
			return err
		}
		t, raw, err := loadTask(w, c, id)
		if err != nil {
			return err
		}
		for _, e := range t.Events {
			if e.EventID == in.EventID {
				if !reflect.DeepEqual(e.EventInput, in) || e.Origin != s.Origin {
					return ErrConflict
				}
				result, err = s.materialize(w, c, t, raw, true, eventDate(e, c.Timezone))
				return err
			}
		}
		if len(t.Events) >= 4096 {
			return errors.New("worklog_capacity")
		}
		if in.OccurredAt != "" {
			when, _ := time.Parse(time.RFC3339Nano, in.OccurredAt)
			if when.After(s.Now().Add(time.Minute)) {
				return errors.New("worklog_future_event")
			}
		}
		status, err := statusAfter(t.Status, in)
		if err != nil {
			return err
		}
		previousTime, _ := time.Parse(time.RFC3339Nano, t.Events[len(t.Events)-1].RecordedAt)
		if s.Now().Before(previousTime) {
			return errors.New("worklog_clock_regressed")
		}
		previous := canonical.Hash(renderTask(t))
		t.ViewBaseHash = &previous
		t.Events = append(t.Events, Event{EventInput: in, Sequence: len(t.Events) + 1, RecordedAt: s.Now().UTC().Format(time.RFC3339Nano), Origin: s.Origin})
		t.Status = status
		next, err := json.Marshal(t)
		if err != nil {
			return err
		}
		if len(next) > maxRecordBytes {
			return errors.New("worklog_capacity")
		}
		h := canonical.Hash(raw)
		if err := w.WriteCAS(taskPath(id), &h, next, 0600); err != nil {
			return err
		}
		result, err = s.materialize(w, c, t, next, false)
		return err
	})
	return result, err
}

func syncView(w *canonical.Writer, path string, base *string, wanted []byte) error {
	current, err := w.CurrentHash(path)
	if err != nil {
		return err
	}
	if current != nil && *current == canonical.Hash(wanted) {
		return nil
	}
	if current == nil {
		base = nil
	} // A missing generated view can be rebuilt.
	if err := w.WriteCAS(path, base, wanted, 0600); err != nil {
		if errors.Is(err, canonical.ErrConflict) {
			return ErrConflict
		}
		return err
	}
	return nil
}

func (s *Service) materialize(w *canonical.Writer, c Config, t Task, raw []byte, duplicate bool, retryDates ...string) (Receipt, error) {
	r := Receipt{TaskID: t.TaskID, DocID: t.DocID, Sequence: len(t.Events), Status: t.Status, Recorded: true, Duplicate: duplicate, SHA256: canonical.Hash(raw)}
	if err := syncView(w, taskView(t), t.ViewBaseHash, renderTask(t)); err != nil {
		return r, err
	}
	// Recording is authoritative before derived views. Retry uses the same event ID.
	date := eventDate(t.Events[len(t.Events)-1], c.Timezone)
	if _, err := s.buildDaily(w, c, date); err != nil {
		return r, err
	}
	for _, retryDate := range retryDates {
		if retryDate != date {
			if _, err := s.buildDaily(w, c, retryDate); err != nil {
				return r, err
			}
		}
	}
	r.ViewsReady = true
	return r, nil
}

func tasks(w *canonical.Writer, c Config) ([]Task, map[string][]byte, error) {
	entries, err := w.Entries(stateRoot + "/tasks")
	if errors.Is(err, os.ErrNotExist) {
		return []Task{}, map[string][]byte{}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if len(entries) > 4096 {
		return nil, nil, errors.New("worklog_capacity")
	}
	all := []Task{}
	raw := map[string][]byte{}
	totalBytes := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			return nil, nil, errors.New("invalid_worklog_inventory")
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		t, b, err := loadTask(w, c, id)
		if err != nil {
			return nil, nil, err
		}
		totalBytes += len(b)
		if totalBytes > 32<<20 {
			return nil, nil, errors.New("worklog_capacity")
		}
		all = append(all, t)
		raw[id] = b
	}
	sort.Slice(all, func(i, j int) bool { return all[i].TaskID < all[j].TaskID })
	return all, raw, nil
}

func (s *Service) Read(id string) (Task, error) {
	var t Task
	err := canonical.WithWriter(s.Root, func(w *canonical.Writer) error {
		c, _, err := s.authorize(w, contextcore.CapabilityRead)
		if err != nil {
			return err
		}
		t, _, err = loadTask(w, c, id)
		return err
	})
	return t, err
}

func (s *Service) Search(query string, limit int) ([]Task, error) {
	if !textOK(query, 2048) || limit < 1 || limit > 20 {
		return nil, errors.New("invalid_worklog_query")
	}
	result := []Task{}
	err := canonical.WithWriter(s.Root, func(w *canonical.Writer) error {
		c, _, err := s.authorize(w, contextcore.CapabilitySearch)
		if err != nil {
			return err
		}
		all, _, err := tasks(w, c)
		if err != nil {
			return err
		}
		words := strings.Fields(strings.ToLower(query))
		for _, t := range all {
			b, _ := json.Marshal(t)
			text := strings.ToLower(string(b))
			matches := true
			for _, word := range words {
				if !strings.Contains(text, word) {
					matches = false
					break
				}
			}
			if matches {
				result = append(result, t)
			}
		}
		sort.SliceStable(result, func(i, j int) bool {
			a, _ := time.Parse(time.RFC3339Nano, result[i].Events[len(result[i].Events)-1].RecordedAt)
			b, _ := time.Parse(time.RFC3339Nano, result[j].Events[len(result[j].Events)-1].RecordedAt)
			return a.After(b)
		})
		if len(result) > limit {
			result = result[:limit]
		}
		return nil
	})
	return result, err
}

func (s *Service) Daily(date string) (Daily, error) {
	var d Daily
	err := canonical.WithWriter(s.Root, func(w *canonical.Writer) error {
		c, _, err := s.authorize(w, contextcore.CapabilityPropose)
		if err != nil {
			return err
		}
		d, err = s.buildDaily(w, c, date)
		return err
	})
	return d, err
}

func (s *Service) buildDaily(w *canonical.Writer, c Config, date string) (Daily, error) {
	if _, err := time.Parse("2006-01-02", date); err != nil || len(date) != 10 {
		return Daily{}, errors.New("invalid_worklog_date")
	}
	all, _, err := tasks(w, c)
	if err != nil {
		return Daily{}, err
	}
	selected := []Task{}
	sources := []Evidence{}
	for _, t := range all {
		dayTask, ok := taskOnDay(t, date)
		if !ok {
			continue
		}
		selected = append(selected, dayTask)
		projection, err := json.Marshal(dayTask)
		if err != nil {
			return Daily{}, err
		}
		h := canonical.Hash(projection)
		sources = append(sources, Evidence{Reference: "task:" + t.TaskID, SHA256: h})
		path := stateRoot + "/snapshots/" + h + ".json"
		if b, err := w.ReadLimit(path, maxRecordBytes); err == nil {
			if !bytes.Equal(b, projection) {
				return Daily{}, ErrConflict
			}
		} else if errors.Is(err, os.ErrNotExist) {
			if err := w.WriteCAS(path, nil, projection, 0600); err != nil {
				return Daily{}, err
			}
		} else {
			return Daily{}, err
		}
	}
	d := Daily{Version: Version, Date: date, Timezone: c.Timezone, DocID: digestID(c.Project, c.Timezone, date), Sources: sources, Body: renderDailyBody(selected, date)}
	statePath := stateRoot + "/daily/" + date + ".json"
	var prior Daily
	previous, err := w.ReadLimit(statePath, maxRecordBytes)
	var base *string
	if err == nil {
		if decode(previous, &prior) != nil || prior.Version != Version || prior.DocID != d.DocID || prior.Timezone != c.Timezone || prior.Date != date {
			return Daily{}, ErrConflict
		}
		if err := syncView(w, dailyView(c, date), prior.ViewBaseHash, renderDaily(c, prior)); err != nil {
			return Daily{}, err
		}
		oldView := canonical.Hash(renderDaily(c, prior))
		d.ViewBaseHash = &oldView
		h := canonical.Hash(previous)
		base = &h
		if prior.Body == d.Body && reflect.DeepEqual(prior.Sources, d.Sources) {
			return prior, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Daily{}, err
	}
	next, _ := json.Marshal(d)
	if len(next) > maxRecordBytes {
		return Daily{}, errors.New("worklog_capacity")
	}
	if err := w.WriteCAS(statePath, base, next, 0600); err != nil {
		return Daily{}, err
	}
	if err := syncView(w, dailyView(c, date), d.ViewBaseHash, renderDaily(c, d)); err != nil {
		return d, err
	}
	return d, nil
}
