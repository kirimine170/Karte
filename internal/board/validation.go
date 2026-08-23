package board

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	pathpkg "path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// LegacyUnknownDate is used only when a legacy Board omits both dates. It
	// keeps load migration deterministic without consulting the wall clock.
	LegacyUnknownDate = "0001-01-01"

	maxCards          = 10_000
	maxEdges          = 50_000
	maxTags           = 128
	maxTagLength      = 128
	maxTitleLength    = 500
	maxPathLength     = 4_096
	maxBodyLength     = 4 * 1_024 * 1_024
	maxDescription    = 16_384
	maxCoordinate     = 1_000_000
	maxDimension      = 100_000
	minViewportZoom   = 0.1
	maxViewportZoom   = 8
	maxMetaDepth      = 16
	maxMetaNodes      = 4_096
	maxMetaCollection = 1_024
	maxMetaString     = 65_536
)

const (
	CardTypeResource = "resource"
	CardTypeBoard    = "board"
	CardTypeQuote    = "quote"
	CardTypeThought  = "thought"
	CardTypeLLMNote  = "llm-note"
	CardTypeSummary  = "summary"
	CardTypeClaim    = "claim"
	CardTypeQuestion = "question"
	CardTypeTask     = "task"
)

const (
	RelationContains    = "contains"
	RelationReferences  = "references"
	RelationQuotes      = "quotes"
	RelationCites       = "cites"
	RelationSupports    = "supports"
	RelationContradicts = "contradicts"
	RelationDerivedFrom = "derived_from"
	RelationSummarizes  = "summarizes"
	RelationExpands     = "expands"
	RelationAnswers     = "answers"
	RelationDependsOn   = "depends_on"
	RelationRelatedTo   = "related_to"
)

var (
	cardTypes = [...]string{
		CardTypeResource,
		CardTypeBoard,
		CardTypeQuote,
		CardTypeThought,
		CardTypeLLMNote,
		CardTypeSummary,
		CardTypeClaim,
		CardTypeQuestion,
		CardTypeTask,
	}
	relations = [...]string{
		RelationContains,
		RelationReferences,
		RelationQuotes,
		RelationCites,
		RelationSupports,
		RelationContradicts,
		RelationDerivedFrom,
		RelationSummarizes,
		RelationExpands,
		RelationAnswers,
		RelationDependsOn,
		RelationRelatedTo,
	}
	cardTypeSet          = stringSet(cardTypes[:])
	relationSet          = stringSet(relations[:])
	cardIDPattern        = regexp.MustCompile(`^card:[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	edgeIDPattern        = regexp.MustCompile(`^edge:[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	docIDPattern         = regexp.MustCompile(`^board:[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$`)
	reservedCardMetaKeys = stringSet([]string{
		"id",
		"type",
		"title",
		"source",
		"tags",
		"created_by",
		"createdBy",
		"updated_by",
		"updatedBy",
		"reviewed",
		"reviewed_by",
		"reviewedBy",
		"model",
		"body",
		"meta",
	})
)

// Violation is a stable, machine-readable validation finding. Path is a JSON
// Pointer into Document and Code is stable across message wording changes.
type Violation struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

// ValidationError preserves all findings rather than failing at the first
// malformed card, edge, or layout entry.
type ValidationError struct {
	Violations []Violation `json:"violations"`
}

func (e *ValidationError) Error() string {
	if e == nil || len(e.Violations) == 0 {
		return "board validation failed"
	}
	parts := make([]string, 0, len(e.Violations))
	for _, violation := range e.Violations {
		parts = append(parts, fmt.Sprintf("%s at %s", violation.Code, violation.Path))
	}
	return "board validation failed: " + strings.Join(parts, ", ")
}

// SupportedCardTypes returns the complete v1 enum without exposing mutable
// package state.
func SupportedCardTypes() []string {
	return append([]string(nil), cardTypes[:]...)
}

// SupportedRelations returns the complete v1 enum without exposing mutable
// package state.
func SupportedRelations() []string {
	return append([]string(nil), relations[:]...)
}

// Migrate deterministically canonicalizes the only supported legacy shape.
// Version missing/zero migrates to v1. Future, negative, and otherwise
// unsupported integer versions are rejected. It is idempotent and never reads
// the wall clock.
func Migrate(doc *Document) error {
	if doc == nil {
		return &ValidationError{Violations: []Violation{{
			Code: "board.nil", Path: "", Message: "document is nil",
		}}}
	}
	if doc.Version < 0 || doc.Version > CurrentVersion {
		return &ValidationError{Violations: []Violation{{
			Code: "board.version.unsupported", Path: "/version", Message: "only versions 0 and 1 are supported",
		}}}
	}
	if doc.Version == 0 {
		doc.Version = CurrentVersion
	}
	if doc.Type == "" {
		doc.Type = BoardType
	}
	if doc.Tags == nil {
		doc.Tags = []string{}
	}
	if doc.Cards == nil {
		doc.Cards = []Card{}
	}
	if doc.Edges == nil {
		doc.Edges = []Edge{}
	}
	if doc.Layout.Cards == nil {
		doc.Layout.Cards = map[string]CardLayout{}
	}
	if doc.Layout.Viewport.Zoom == 0 {
		doc.Layout.Viewport.Zoom = 1
	}

	switch {
	case doc.Created == "" && doc.Updated == "":
		doc.Created = LegacyUnknownDate
		doc.Updated = LegacyUnknownDate
	case doc.Created == "" && validDate(doc.Updated):
		doc.Created = doc.Updated
	case doc.Updated == "" && validDate(doc.Created):
		doc.Updated = doc.Created
	}
	return nil
}

// ValidateDocument reports every contract violation in deterministic path/code
// order. Call Migrate before validation when accepting version 0 input.
func ValidateDocument(doc *Document) []Violation {
	if doc == nil {
		return []Violation{{Code: "board.nil", Path: "", Message: "document is nil"}}
	}

	violations := make([]Violation, 0)
	add := func(code, path, message string) {
		violations = append(violations, Violation{Code: code, Path: path, Message: message})
	}

	if doc.Type != BoardType {
		add("board.type.invalid", "/type", "type must be karte-board")
	}
	if doc.Version != CurrentVersion {
		add("board.version.unsupported", "/version", "version must be 1 after migration")
	}
	if !validBoardPath(doc.Path) {
		add("board.path.invalid", "/path", "path must be a canonical content/*.board.md path")
	}
	if strings.TrimSpace(doc.Title) == "" || utf8.RuneCountInString(doc.Title) > maxTitleLength || hasControl(doc.Title) {
		add("board.title.invalid", "/title", "title must be non-empty, bounded, and free of control characters")
	}
	if doc.DocID != "" && !docIDPattern.MatchString(doc.DocID) {
		add("board.doc-id.invalid", "/docId", "docId must be empty or use the board: namespace")
	}
	if !validDate(doc.Created) {
		add("board.created.invalid", "/created", "created must be a YYYY-MM-DD date")
	}
	if !validDate(doc.Updated) {
		add("board.updated.invalid", "/updated", "updated must be a YYYY-MM-DD date")
	}
	validateTags(doc.Tags, "/tags", add)
	if len(doc.Cards) > maxCards {
		add("board.cards.limit", "/cards", "card count exceeds the v1 limit")
	}
	if len(doc.Edges) > maxEdges {
		add("board.edges.limit", "/edges", "edge count exceeds the v1 limit")
	}
	if utf8.RuneCountInString(doc.Notes) > maxBodyLength {
		add("board.notes.limit", "/notes", "notes exceed the v1 size limit")
	}

	cardIDs := make(map[string]int, len(doc.Cards))
	for index := range doc.Cards {
		card := &doc.Cards[index]
		base := "/cards/" + strconv.Itoa(index)
		if !cardIDPattern.MatchString(card.ID) {
			add("card.id.invalid", base+"/id", "card id does not match the v1 grammar")
		}
		if first, exists := cardIDs[card.ID]; exists {
			add("card.id.duplicate", base+"/id", fmt.Sprintf("card id duplicates /cards/%d/id", first))
		} else {
			cardIDs[card.ID] = index
		}
		if _, ok := cardTypeSet[card.Type]; !ok {
			add("card.type.invalid", base+"/type", "card type is not in the v1 enum")
		}
		if strings.TrimSpace(card.Title) == "" || utf8.RuneCountInString(card.Title) > maxTitleLength || hasControl(card.Title) {
			add("card.title.invalid", base+"/title", "card title must be non-empty, bounded, and free of control characters")
		}
		if !validSourcePath(card.Source) {
			add("card.source.invalid", base+"/source", "source must be empty or a canonical project resource path")
		}
		validateTags(card.Tags, base+"/tags", add)
		if utf8.RuneCountInString(card.Body) > maxBodyLength {
			add("card.body.limit", base+"/body", "card body exceeds the v1 size limit")
		}
		validateBoundedString(card.CreatedBy, base+"/createdBy", "card.created-by.limit", maxTitleLength, add)
		validateBoundedString(card.UpdatedBy, base+"/updatedBy", "card.updated-by.limit", maxTitleLength, add)
		validateBoundedString(card.ReviewedBy, base+"/reviewedBy", "card.reviewed-by.limit", maxTitleLength, add)
		validateBoundedString(card.Model, base+"/model", "card.model.limit", maxTitleLength, add)
		validateCardMeta(card.Meta, base+"/meta", add)
	}

	for cardID := range cardIDs {
		layout, exists := doc.Layout.Cards[cardID]
		if !exists {
			add("layout.card.missing", "/layout/cards/"+escapeJSONPointer(cardID), "every card must have a layout entry")
			continue
		}
		validateCardLayout(layout, "/layout/cards/"+escapeJSONPointer(cardID), add)
	}
	layoutIDs := make([]string, 0, len(doc.Layout.Cards))
	for cardID := range doc.Layout.Cards {
		layoutIDs = append(layoutIDs, cardID)
	}
	sort.Strings(layoutIDs)
	for _, cardID := range layoutIDs {
		if _, exists := cardIDs[cardID]; exists {
			continue
		}
		base := "/layout/cards/" + escapeJSONPointer(cardID)
		add("layout.card.orphan", base, "layout entry does not reference an existing card")
		validateCardLayout(doc.Layout.Cards[cardID], base, add)
	}
	validateViewport(doc.Layout.Viewport, "/layout/viewport", add)

	edgeIDs := make(map[string]int, len(doc.Edges))
	edgeTuples := make(map[string]int, len(doc.Edges))
	for index := range doc.Edges {
		edge := &doc.Edges[index]
		base := "/edges/" + strconv.Itoa(index)
		if !edgeIDPattern.MatchString(edge.ID) {
			add("edge.id.invalid", base+"/id", "edge id does not match the v1 grammar")
		}
		if first, exists := edgeIDs[edge.ID]; exists {
			add("edge.id.duplicate", base+"/id", fmt.Sprintf("edge id duplicates /edges/%d/id", first))
		} else {
			edgeIDs[edge.ID] = index
		}
		if !cardIDPattern.MatchString(edge.From) {
			add("edge.from.invalid", base+"/from", "from does not match the card id grammar")
		}
		if !cardIDPattern.MatchString(edge.To) {
			add("edge.to.invalid", base+"/to", "to does not match the card id grammar")
		}
		if _, exists := cardIDs[edge.From]; !exists {
			add("edge.from.missing", base+"/from", "from does not reference an existing card")
		}
		if _, exists := cardIDs[edge.To]; !exists {
			add("edge.to.missing", base+"/to", "to does not reference an existing card")
		}
		if edge.From == edge.To {
			add("edge.self", base, "self edges are not allowed")
		}
		if _, ok := relationSet[edge.Relation]; !ok {
			add("edge.relation.invalid", base+"/relation", "relation is not in the v1 enum")
		}
		tuple := edge.From + "\x00" + edge.To + "\x00" + edge.Relation
		if first, exists := edgeTuples[tuple]; exists {
			add("edge.duplicate", base, fmt.Sprintf("edge duplicates /edges/%d", first))
		} else {
			edgeTuples[tuple] = index
		}
		validateBoundedString(edge.Label, base+"/label", "edge.label.limit", maxTitleLength, add)
		validateBoundedString(edge.Description, base+"/description", "edge.description.limit", maxDescription, add)
	}

	sort.SliceStable(violations, func(i, j int) bool {
		if violations[i].Path != violations[j].Path {
			return violations[i].Path < violations[j].Path
		}
		if violations[i].Code != violations[j].Code {
			return violations[i].Code < violations[j].Code
		}
		return violations[i].Message < violations[j].Message
	})
	return violations
}

// UnmarshalJSON makes Card.Meta the only JSON extension point. Unknown fields
// at every typed Document/Card/Edge/Layout/ViewState level are rejected.
func (doc *Document) UnmarshalJSON(data []byte) error {
	type plainDocument Document
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var decoded plainDocument
	if err := decoder.Decode(&decoded); err != nil {
		return fmt.Errorf("decode board document: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode board document: multiple JSON values")
		}
		return fmt.Errorf("decode board document: %w", err)
	}
	*doc = Document(decoded)
	return nil
}

func validateTags(tags []string, base string, add func(string, string, string)) {
	if len(tags) > maxTags {
		add("tags.limit", base, "tag count exceeds the v1 limit")
	}
	seen := make(map[string]int, len(tags))
	for index, tag := range tags {
		path := base + "/" + strconv.Itoa(index)
		if strings.TrimSpace(tag) == "" || utf8.RuneCountInString(tag) > maxTagLength || hasControl(tag) {
			add("tag.invalid", path, "tag must be non-empty, bounded, and free of control characters")
		}
		if first, exists := seen[tag]; exists {
			add("tag.duplicate", path, fmt.Sprintf("tag duplicates %s/%d", base, first))
		} else {
			seen[tag] = index
		}
	}
}

func validateBoundedString(value, path, code string, limit int, add func(string, string, string)) {
	if utf8.RuneCountInString(value) > limit || hasControlExceptWhitespace(value) {
		add(code, path, "value exceeds its limit or contains a forbidden control character")
	}
}

func validateCardMeta(meta map[string]any, base string, add func(string, string, string)) {
	if len(meta) > maxMetaCollection {
		add("card.meta.collection.limit", base, "meta object exceeds the v1 property limit")
		return
	}
	nodes := 1
	keys := make([]string, 0, len(meta))
	for key := range meta {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		path := base + "/" + escapeJSONPointer(key)
		if _, reserved := reservedCardMetaKeys[key]; reserved {
			add("card.meta.reserved", path, "meta key collides with a reserved Card field")
		}
		if strings.TrimSpace(key) == "" || hasControl(key) || utf8.RuneCountInString(key) > maxTitleLength {
			add("card.meta.key.invalid", path, "meta key is empty, too long, or contains a control character")
		}
		validateMetaValue(meta[key], path, 0, &nodes, add)
	}
}

func validateMetaValue(value any, path string, depth int, nodes *int, add func(string, string, string)) {
	*nodes++
	if *nodes > maxMetaNodes {
		if *nodes == maxMetaNodes+1 {
			add("card.meta.nodes.limit", path, "meta node count exceeds the v1 limit")
		}
		return
	}
	if depth > maxMetaDepth {
		add("card.meta.depth", path, "meta nesting exceeds the v1 limit")
		return
	}
	switch typed := value.(type) {
	case nil, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return
	case string:
		if utf8.RuneCountInString(typed) > maxMetaString {
			add("card.meta.string.limit", path, "meta string exceeds the v1 limit")
		}
	case float32:
		if math.IsNaN(float64(typed)) || math.IsInf(float64(typed), 0) {
			add("card.meta.number.non-finite", path, "meta number must be finite")
		}
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			add("card.meta.number.non-finite", path, "meta number must be finite")
		}
	case json.Number:
		if _, err := typed.Float64(); err != nil {
			add("card.meta.number.invalid", path, "meta number is not valid JSON")
		}
	case []any:
		if len(typed) > maxMetaCollection {
			add("card.meta.collection.limit", path, "meta array exceeds the v1 item limit")
			return
		}
		for index, item := range typed {
			validateMetaValue(item, path+"/"+strconv.Itoa(index), depth+1, nodes, add)
		}
	case map[string]any:
		if len(typed) > maxMetaCollection {
			add("card.meta.collection.limit", path, "meta object exceeds the v1 property limit")
			return
		}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			keyPath := path + "/" + escapeJSONPointer(key)
			if strings.TrimSpace(key) == "" || hasControl(key) || utf8.RuneCountInString(key) > maxTitleLength {
				add("card.meta.key.invalid", keyPath, "meta key is empty, too long, or contains a control character")
			}
			validateMetaValue(typed[key], keyPath, depth+1, nodes, add)
		}
	default:
		add("card.meta.value.invalid", path, "meta value must be JSON-compatible")
	}
}

func validateCardLayout(layout CardLayout, base string, add func(string, string, string)) {
	validateCoordinate(layout.X, base+"/x", add)
	validateCoordinate(layout.Y, base+"/y", add)
	validateDimension(layout.Width, base+"/width", add)
	validateDimension(layout.Height, base+"/height", add)
}

func validateViewport(viewport Viewport, base string, add func(string, string, string)) {
	validateCoordinate(viewport.X, base+"/x", add)
	validateCoordinate(viewport.Y, base+"/y", add)
	if !finite(viewport.Zoom) {
		add("view-state.geometry.non-finite", base+"/zoom", "zoom must be finite")
	} else if viewport.Zoom < minViewportZoom || viewport.Zoom > maxViewportZoom {
		add("view-state.geometry.range", base+"/zoom", "zoom is outside the v1 practical range")
	}
}

func validateCoordinate(value float64, path string, add func(string, string, string)) {
	if !finite(value) {
		add("layout.geometry.non-finite", path, "coordinate must be finite")
	} else if math.Abs(value) > maxCoordinate {
		add("layout.geometry.range", path, "coordinate is outside the v1 practical range")
	}
}

func validateDimension(value float64, path string, add func(string, string, string)) {
	if !finite(value) {
		add("layout.geometry.non-finite", path, "dimension must be finite")
	} else if value <= 0 || value > maxDimension {
		add("layout.geometry.range", path, "dimension is outside the v1 practical range")
	}
}

func validBoardPath(value string) bool {
	return validCanonicalPath(value, []string{"content/"}) && strings.HasSuffix(value, ".board.md")
}

func validSourcePath(value string) bool {
	if value == "" {
		return true
	}
	if strings.HasPrefix(value, "data/csv/") {
		name := strings.TrimPrefix(value, "data/csv/")
		return name != "" && !strings.Contains(name, "/") && strings.HasSuffix(name, ".csv") && validCanonicalPath(value, []string{"data/csv/"})
	}
	return validCanonicalPath(value, []string{"content/", "data/image/", "data/audio/", "data/csv/"})
}

func validCanonicalPath(value string, roots []string) bool {
	if value == "" || utf8.RuneCountInString(value) > maxPathLength || hasControl(value) || strings.ContainsAny(value, `\:`) {
		return false
	}
	if pathpkg.IsAbs(value) || pathpkg.Clean(value) != value {
		return false
	}
	for _, root := range roots {
		if strings.HasPrefix(value, root) && len(value) > len(root) {
			return true
		}
	}
	return false
}

func validDate(value string) bool {
	parsed, err := time.Parse(time.DateOnly, value)
	return err == nil && parsed.Format(time.DateOnly) == value
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func hasControl(value string) bool {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func hasControlExceptWhitespace(value string) bool {
	for _, r := range value {
		if (r < 0x20 && r != '\n' && r != '\r' && r != '\t') || r == 0x7f {
			return true
		}
	}
	return false
}

func escapeJSONPointer(value string) string {
	value = strings.ReplaceAll(value, "~", "~0")
	return strings.ReplaceAll(value, "/", "~1")
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
