package contextcore

import (
	"fmt"
	"strings"

	"karte/internal/canonical"
	"karte/internal/decisionlog"
)

func (service *Service) decisionDocuments(actor Actor, scope EffectiveScope) ([]indexedDocument, error) {
	var entries []decisionlog.Entry
	err := canonical.WithWriter(service.dataRoot, func(w *canonical.Writer) error {
		var err error
		entries, err = decisionlog.List(w)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("decision log is unavailable: %w", err)
	}

	documents := make([]indexedDocument, 0, len(entries))
	visible := make(map[string]bool)
	localHuman := actor.Type == "human" && actor.ID == "local-human"
	for _, entry := range entries {
		if entry.Visibility == decisionlog.Private && !localHuman {
			continue
		}
		label := decisionlog.StatusLabel(entry.Status)
		document := indexedDocument{
			DocID: entry.ID, Title: truncateRunes(entry.Content, 160),
			Project: entry.Business, Kind: "decision",
			Tags:        []string{"decision-log", "decision:" + entry.Status},
			Sensitivity: decisionlog.Sensitivity(entry.Visibility), RelativePath: decisionlog.Path(entry.ID),
			UpdatedAt: entry.UpdatedAt, SHA256: entry.SHA256, DecisionStatus: entry.Status,
			Body: fmt.Sprintf("状態　%s\n事業　%s\n日付　%s\n決定者　%s\n\n%s", label, entry.DisplayBusiness(), entry.Date, entry.Decider, entry.Content),
		}
		if decisionIsHistory(entry.Status) {
			document.Tags = append(document.Tags, "decision:history")
		}
		for _, source := range entry.Sources {
			document.Provenance = append(document.Provenance, ProvenanceRef{
				Type: "meeting-summary", Reference: source.URL, SHA256: source.SHA256,
			})
		}
		// Relationship visibility follows the actor's policy. Request tag filters
		// only select results and do not hide permitted links to a different state.
		linkScope := scope
		linkScope.RequestedTags = nil
		if linkScope.permits(document) {
			visible[entry.ID] = true
		}
		if scope.permits(document) {
			documents = append(documents, document)
		}
	}
	byID := make(map[string]decisionlog.Entry, len(entries))
	for _, entry := range entries {
		byID[entry.ID] = entry
	}
	for i := range documents {
		document := &documents[i]
		entry := byID[document.DocID]
		if visible[entry.Supersedes] {
			document.Body += "\n\n置換元　" + entry.Supersedes
		}
		if visible[entry.ReplacedBy] {
			document.Body += "\n\n置換先　" + entry.ReplacedBy
		}
		document.SHA256 = canonical.Hash([]byte(entry.SHA256 + "\n" + document.Body))
	}
	return documents, nil
}

func decisionIsHistory(status string) bool {
	return status == decisionlog.Withdrawn || status == decisionlog.Superseded
}

func decisionQuery(query SearchQuery, scope EffectiveScope) (SearchQuery, string, bool) {
	history := scope.RequestedTags["decision:history"] || scope.RequestedTags["decision:withdrawn"] || scope.RequestedTags["decision:superseded"]
	status := ""
	words := []string{}
	for _, word := range strings.Fields(query.Text) {
		switch word {
		case "履歴":
			history = true
		case "未決":
			status = decisionlog.Open
		case "決定":
			status = decisionlog.Decided
		case "撤回":
			status, history = decisionlog.Withdrawn, true
		case "置換":
			status, history = decisionlog.Superseded, true
		default:
			words = append(words, word)
		}
	}
	query.Text = strings.Join(words, " ")
	return query, status, history
}
