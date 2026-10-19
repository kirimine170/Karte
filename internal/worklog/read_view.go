package worklog

import (
	"bytes"
	"errors"
	"karte/internal/canonical"
	"karte/internal/contextcore"
	"karte/internal/ephyrecordsv2"
	"os"
)

var (
	ErrViewMissing  = errors.New("worklog_view_missing")
	ErrViewConflict = errors.New("worklog_view_conflict")
	ErrViewRecord   = errors.New("worklog_view_invalid_record")
	ErrViewV2       = errors.New("worklog_view_v2_conflict")
)

// ReadViewResult identifies an existing derived view separately from its
// authoritative task ledger. Markdown is never synthesized or repaired here.
type ReadViewResult struct {
	TaskID         string
	DocID          string
	RelativePath   string
	SourceSHA256   string
	MarkdownSHA256 string
	Markdown       []byte
}

// ReadView authorizes every call under the existing canonical lease. It reads
// only a validated local worklog task and its exact existing generated view.
func (s *Service) ReadView(id string) (ReadViewResult, error) {
	var result ReadViewResult
	err := canonical.WithWriter(s.Root, func(w *canonical.Writer) error {
		c, _, err := s.authorize(w, contextcore.CapabilityRead)
		if err != nil {
			return ErrDenied
		}
		task, source, err := loadTask(w, c, id)
		if err != nil || !validViewBase(task) {
			return ErrViewRecord
		}
		path := taskView(task)
		managed, err := ephyrecordsv2.IsManagedPath(w, path)
		if err != nil || managed {
			return ErrViewV2
		}
		view, err := w.ReadLimit(path, maxRecordBytes)
		if errors.Is(err, os.ErrNotExist) {
			return ErrViewMissing
		}
		if err != nil || !bytes.Equal(view, renderTask(task)) {
			return ErrViewConflict
		}
		result = ReadViewResult{TaskID: task.TaskID, DocID: task.DocID, RelativePath: path,
			SourceSHA256: canonical.Hash(source), MarkdownSHA256: canonical.Hash(view), Markdown: view}
		return nil
	})
	return result, err
}

// ViewBaseHash records the prior generated view (the CAS base), not the current
// view. Reconstruct only that existing generator's prior state to verify it.
func validViewBase(task Task) bool {
	if len(task.Events) == 1 {
		return task.ViewBaseHash == nil
	}
	if len(task.Events) < 2 || task.ViewBaseHash == nil {
		return false
	}
	prior := task
	prior.Events = task.Events[:len(task.Events)-1]
	prior.Status = "running"
	for _, event := range prior.Events[1:] {
		var err error
		prior.Status, err = statusAfter(prior.Status, event.EventInput)
		if err != nil {
			return false
		}
	}
	return *task.ViewBaseHash == canonical.Hash(renderTask(prior))
}
