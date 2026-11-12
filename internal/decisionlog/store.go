package decisionlog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"karte/internal/canonical"
)

const maxRecordBytes = 256 * 1024

type Entry struct {
	Record
	SHA256 string
	stored Record
}

func Path(id string) string { return Directory + "/" + id + ".md" }

func IsPath(path string) bool {
	path = strings.ToLower(filepath.ToSlash(path))
	return path == Directory || strings.HasPrefix(path, Directory+"/")
}

func IsStoragePath(path string) bool {
	path = strings.ToLower(filepath.ToSlash(path))
	return path == SettingsPath || strings.HasPrefix(path, ".mdsys/context/v1/responses/") || IsPath(path) || (strings.HasPrefix(path, ".mdsys/ephy/outbox/") &&
		strings.HasSuffix(path, ".json") && ValidID(strings.TrimSuffix(filepath.Base(path), ".json")))
}

func Decode(data []byte) (Record, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record Record
	if err := decoder.Decode(&record); err != nil {
		return Record{}, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Record{}, fmt.Errorf("invalid decision record JSON")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return Record{}, err
	}
	for _, key := range []string{"supersedes", "replaced_by"} {
		if value, ok := fields[key]; !ok || bytes.Equal(value, []byte("null")) {
			return Record{}, fmt.Errorf("decision field %s is required", key)
		}
	}
	return record, record.Validate()
}

func load(w *canonical.Writer) (map[string]Entry, error) {
	entries := make(map[string]Entry)
	files, err := w.Entries(Directory)
	if errors.Is(err, os.ErrNotExist) {
		return entries, nil
	}
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		id, ok := strings.CutSuffix(file.Name(), ".md")
		if !ok || !ValidID(id) {
			continue
		}
		data, err := w.ReadLimit(Path(id), maxRecordBytes)
		if err != nil {
			return nil, err
		}
		encoded, _, ok := strings.Cut(strings.TrimPrefix(string(data), "---\n"), "\n---\n")
		if !ok || !bytes.HasPrefix(data, []byte("---\n")) {
			return nil, fmt.Errorf("invalid decision document")
		}
		record, err := Decode([]byte(encoded))
		if err != nil || record.ID != id || record.Status == Superseded {
			return nil, fmt.Errorf("invalid stored decision")
		}
		entries[id] = Entry{Record: record, stored: record, SHA256: canonical.Hash(data)}
	}
	if err := applyReplacements(entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func applyReplacements(entries map[string]Entry) error {
	for id, next := range entries {
		if next.Supersedes == "" {
			continue
		}
		old, ok := entries[next.Supersedes]
		if !ok || old.Business != next.Business || old.ReplacedBy != "" || old.Status == Withdrawn {
			return fmt.Errorf("invalid or competing decision replacement")
		}
		old.Status = Superseded
		old.ReplacedBy = id
		oldTime, _ := time.Parse(time.RFC3339Nano, old.UpdatedAt)
		nextTime, _ := time.Parse(time.RFC3339Nano, next.UpdatedAt)
		if nextTime.After(oldTime) {
			old.UpdatedAt = next.UpdatedAt
		}
		entries[old.ID] = old
	}
	visited := 0
	for id, entry := range entries {
		if entry.Supersedes == "" {
			for cursor := id; cursor != ""; cursor = entries[cursor].ReplacedBy {
				visited++
			}
		}
	}
	if visited != len(entries) {
		return fmt.Errorf("cyclic decision replacement")
	}
	return nil
}

func List(w *canonical.Writer) ([]Entry, error) {
	entries, err := load(w)
	if err != nil {
		return nil, err
	}
	settings, err := LoadSettings(w)
	if err != nil {
		return nil, err
	}
	result := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if settings.requiresPrivate(entry.Record) {
			entry.Visibility = Private
			entry.PublicationRestriction = WorkspacePrivate
		}
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Date == result[j].Date {
			return result[i].ID < result[j].ID
		}
		return result[i].Date < result[j].Date
	})
	return result, nil
}

func write(w *canonical.Writer, record Record, expected *string) (Entry, error) {
	if err := record.Validate(); err != nil {
		return Entry{}, err
	}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return Entry{}, err
	}
	data := []byte("---\n" + string(encoded) + "\n---\n" + record.Content + "\n")
	if len(data) > maxRecordBytes {
		return Entry{}, fmt.Errorf("decision document exceeds capacity")
	}
	if err := w.WriteCAS(Path(record.ID), expected, data, 0600); err != nil {
		return Entry{}, err
	}
	return Entry{Record: record, stored: record, SHA256: canonical.Hash(data)}, nil
}

// Accept writes one immutable replacement edge, so the old and new status change together.
func Accept(w *canonical.Writer, record Record) (Entry, error) {
	entries, err := load(w)
	if err != nil {
		return Entry{}, err
	}
	if _, exists := entries[record.ID]; exists {
		return Entry{}, fmt.Errorf("decision ID is already recorded")
	}
	if record.Status != Decided && record.Status != Open {
		return Entry{}, fmt.Errorf("a reviewed proposal must be decided or open")
	}
	settings, err := LoadSettings(w)
	if err != nil {
		return Entry{}, err
	}
	if record.Supersedes != "" {
		old, ok := entries[record.Supersedes]
		if !ok || old.Business != record.Business || (old.Status != Decided && old.Status != Open) {
			return Entry{}, fmt.Errorf("replacement requires an active decision in the same business")
		}
		if record.Visibility == Public && (old.Visibility == Private || settings.requiresPrivate(old.Record)) {
			return Entry{}, fmt.Errorf("a public decision cannot replace a private decision")
		}
	}
	if record.Visibility == Public && settings.requiresPrivate(record) {
		return Entry{}, fmt.Errorf("decision is restricted to the workspace")
	}
	return write(w, record, nil)
}

func Withdraw(w *canonical.Writer, id, now string) error {
	entries, err := load(w)
	if err != nil {
		return err
	}
	entry, ok := entries[id]
	if !ok || entry.Status == Superseded {
		return fmt.Errorf("withdrawal requires an active decision")
	}
	if entry.Status == Withdrawn {
		return nil
	}
	entry.Status, entry.UpdatedAt = Withdrawn, now
	_, err = write(w, entry.Record, &entry.SHA256)
	return err
}

// MergeSources preserves the first observed revision of each source URL.
func MergeSources(existing, additional []Source) []Source {
	result := append([]Source(nil), existing...)
	seen := make(map[string]bool, len(existing))
	for _, source := range result {
		seen[source.URL] = true
	}
	for _, source := range additional {
		if !seen[source.URL] {
			result = append(result, source)
			seen[source.URL] = true
		}
	}
	return result
}
