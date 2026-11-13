package decisionlog

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"

	"karte/internal/canonical"
)

// Snapshot is the durable decision state. Derived replacement statuses,
// pending proposals, Context responses and credentials stay outside it.
type Snapshot struct {
	Settings *Settings         `json:"settings"`
	Records  map[string]Record `json:"records"`
}

func ReadSnapshot(w *canonical.Writer) (Snapshot, error) {
	entries, err := load(w)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := Snapshot{Records: make(map[string]Record, len(entries))}
	for id, entry := range entries {
		snapshot.Records[id] = entry.stored
	}
	if _, err := w.ReadLimit(SettingsPath, 64*1024); errors.Is(err, os.ErrNotExist) {
		return snapshot, nil
	} else if err != nil {
		return Snapshot{}, err
	}
	settings, err := LoadSettings(w)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.Settings = &settings
	return snapshot, nil
}

func (s Snapshot) Validate() error {
	if s.Records == nil {
		return fmt.Errorf("decision snapshot is missing records")
	}
	if s.Settings != nil {
		if err := s.Settings.validate(); err != nil {
			return err
		}
	}
	entries := make(map[string]Entry, len(s.Records))
	for id, record := range s.Records {
		if record.ID != id || record.Status == Superseded || record.Validate() != nil {
			return fmt.Errorf("decision snapshot contains an invalid record")
		}
		entries[id] = Entry{Record: record}
	}
	return applyReplacements(entries)
}

var ErrSnapshotConflict = errors.New("決定ログの同期中に別の変更が見つかりました")

// ApplySnapshot can resume a partial application. Every local value must
// still match its before or after state, so a new edit is never overwritten.
func ApplySnapshot(w *canonical.Writer, before, after Snapshot) error {
	if err := after.Validate(); err != nil {
		return err
	}
	current, err := ReadSnapshot(w)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current.Settings, before.Settings) && !reflect.DeepEqual(current.Settings, after.Settings) {
		return ErrSnapshotConflict
	}
	if before.Settings != nil && after.Settings == nil {
		return ErrSnapshotConflict
	}
	for id, record := range current.Records {
		next, exists := after.Records[id]
		if !exists || (!reflect.DeepEqual(record, before.Records[id]) && !reflect.DeepEqual(record, next)) {
			return ErrSnapshotConflict
		}
	}
	for id, previous := range before.Records {
		if _, exists := current.Records[id]; !exists || previous.Business != after.Records[id].Business ||
			previous.Supersedes != after.Records[id].Supersedes {
			return ErrSnapshotConflict
		}
	}
	if !reflect.DeepEqual(current.Settings, after.Settings) {
		if err := writeSettings(w, *after.Settings); err != nil {
			return err
		}
	}

	ids := make([]string, 0, len(after.Records))
	for id := range after.Records {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	written := make(map[string]bool, len(ids))
	for _, id := range ids {
		// Walk back through missing predecessors before creating a successor.
		var chain []string
		for cursor := id; cursor != "" && !written[cursor]; cursor = after.Records[cursor].Supersedes {
			chain = append(chain, cursor)
			written[cursor] = true
		}
		for i := len(chain) - 1; i >= 0; i-- {
			recordID := chain[i]
			next := after.Records[recordID]
			if reflect.DeepEqual(current.Records[recordID], next) {
				continue
			}
			expected, err := w.CurrentHash(Path(recordID))
			if err != nil {
				return err
			}
			if _, err := write(w, next, expected); err != nil {
				return err
			}
		}
	}
	return nil
}
