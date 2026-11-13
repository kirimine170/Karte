package contextcore

import (
	"fmt"
	"strings"

	"karte/internal/canonical"
	"karte/internal/decisionlog"
)

// WithDecisionRecipient binds an authenticated transport to a decision
// recipient. The file-backed Context request cannot set this value.
func (service *Service) WithDecisionRecipient(recipient string) *Service {
	bound := *service
	bound.decisionRecipient = recipient
	return &bound
}

func (service *Service) decisionDocuments(actor Actor, scope EffectiveScope) ([]indexedDocument, error) {
	localHuman := actor.Type == "human" && actor.ID == "local-human"
	var tags []string
	if actor.Type == "ephy" && actor.ID == "ephy" ||
		actor.Type == "tool" && service.decisionRecipient == decisionlog.Public {
		tags = []string{decisionlog.Public}
	}
	authenticatedAgent := actor.Type == "tool" && actor.ID == service.decisionRecipient && decisionlog.ValidAgent(actor.ID)
	if authenticatedAgent {
		tags = []string{decisionlog.Public, decisionlog.Internal, actor.ID}
	}
	return service.decisionDocumentsFor(scope, localHuman, tags, authenticatedAgent, nil)
}

func (service *Service) ExportDecisions(actor Actor, policy Policy, recipient string) ([]Document, error) {
	if recipient != decisionlog.Public && !decisionlog.ValidAgent(recipient) {
		return nil, fmt.Errorf("invalid decision export recipient")
	}
	scope, err := policy.ResolveFor(actor, Scope{SensitivityCeiling: "restricted"}, CapabilityExport)
	if err != nil {
		return nil, err
	}
	if !scope.Allowed {
		return nil, fmt.Errorf("decision export is not authorized")
	}
	tags := []string{decisionlog.Public}
	var recipientScope *EffectiveScope
	if recipient != decisionlog.Public {
		tags = append(tags, recipient)
		if _, configured := policy.Actors[recipient]; configured {
			grant, err := policy.ResolveFor(Actor{Type: "tool", ID: recipient}, Scope{SensitivityCeiling: "restricted"}, CapabilityRead)
			if err != nil {
				return nil, err
			}
			recipientScope = &grant
			tags = append(tags, decisionlog.Internal)
		}
	}
	entries, err := service.decisionDocumentsFor(scope, false, tags, recipientScope != nil, recipientScope)
	if err != nil {
		return nil, err
	}
	documents := make([]Document, 0, len(entries))
	for _, entry := range entries {
		documents = append(documents, contextDocument(entry))
	}
	return documents, nil
}

func contextDocument(candidate indexedDocument) Document {
	return Document{
		DocID: candidate.DocID, Title: candidate.Title, Project: candidate.Project, Kind: candidate.Kind,
		Tags: candidate.Tags, Sensitivity: candidate.Sensitivity, RelativePath: candidate.RelativePath,
		UpdatedAt: candidate.UpdatedAt, SHA256: candidate.SHA256, Body: candidate.Body, Provenance: candidate.Provenance,
	}
}

func (service *Service) decisionDocumentsFor(scope EffectiveScope, localHuman bool, recipientTags []string, businessRecipient bool, recipientScope *EffectiveScope) ([]indexedDocument, error) {
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
	for _, entry := range entries {
		tags := recipientTags
		businessScope := scope
		if recipientScope != nil {
			businessScope = *recipientScope
		}
		if businessRecipient && (businessScope.AllProjects || businessScope.Projects[entry.Business]) {
			tags = append(append([]string(nil), tags...), "business:"+entry.Business)
		}
		if !localHuman && !entry.VisibleTo(tags) {
			continue
		}
		label := decisionlog.StatusLabel(entry.Status)
		document := indexedDocument{
			DocID: entry.ID, Title: truncateRunes(entry.Content, 160),
			Project: entry.Business, Kind: "decision",
			Tags:        []string{"decision-log", "decision:" + entry.Status},
			Sensitivity: entry.ContextSensitivity(), RelativePath: decisionlog.Path(entry.ID),
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
		if recipientScope != nil && !recipientScope.permits(document) {
			continue
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
