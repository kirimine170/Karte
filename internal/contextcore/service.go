package contextcore

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	fm "karte/internal/frontmatter"
)

const maxCanonicalDocumentBytes = 2 * 1024 * 1024

type indexedDocument struct {
	DocID        string
	Title        string
	Project      string
	Kind         string
	Tags         []string
	Sensitivity  string
	RelativePath string
	UpdatedAt    string
	SHA256       string
	Body         string
	Provenance   []ProvenanceRef
}

type Service struct {
	dataRoot    string
	contentRoot string
}

func NewService(dataDir string) (*Service, error) {
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("resolve Karte data directory: %w", err)
	}
	realRoot, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve Karte data directory symlinks: %w", err)
	}
	return &Service{dataRoot: realRoot, contentRoot: filepath.Join(realRoot, "content")}, nil
}

func (service *Service) Search(request Request, policy Policy) ([]SearchResult, []Diagnostic, string, error) {
	if err := request.Validate(); err != nil {
		return nil, nil, "invalid", err
	}
	effective, err := policy.ResolveFor(request.Actor, request.Scope, CapabilitySearch)
	if err != nil {
		return nil, nil, "invalid", err
	}
	if !effective.Allowed {
		return []SearchResult{}, []Diagnostic{}, "denied", nil
	}
	documents, diagnostics, err := service.scan()
	if err != nil {
		return nil, nil, "invalid", err
	}
	type rankedDocument struct {
		document indexedDocument
		score    float64
	}
	ranked := make([]rankedDocument, 0, len(documents))
	for _, document := range documents {
		if !effective.permits(document) {
			continue
		}
		score := lexicalScore(*request.Query, document)
		if score > 0 {
			ranked = append(ranked, rankedDocument{document: document, score: score})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return ranked[i].document.DocID < ranked[j].document.DocID
		}
		return ranked[i].score > ranked[j].score
	})
	if len(ranked) > request.Query.TopK {
		ranked = ranked[:request.Query.TopK]
	}
	results := make([]SearchResult, 0, len(ranked))
	for _, item := range ranked {
		document := item.document
		results = append(results, SearchResult{
			DocID: document.DocID, Title: document.Title, Project: document.Project, Kind: document.Kind,
			Tags: document.Tags, Sensitivity: document.Sensitivity, RelativePath: document.RelativePath,
			UpdatedAt: document.UpdatedAt, SHA256: document.SHA256,
			Snippet: selectSnippet(document.Body, request.Query.Text), Score: item.score, Provenance: document.Provenance,
		})
	}
	return results, diagnostics, "ok", nil
}

func (service *Service) Read(request Request, policy Policy) (*Document, []Diagnostic, string, error) {
	if err := request.Validate(); err != nil {
		return nil, nil, "invalid", err
	}
	effective, err := policy.ResolveFor(request.Actor, request.Scope, CapabilityRead)
	if err != nil {
		return nil, nil, "invalid", err
	}
	if !effective.Allowed {
		return nil, []Diagnostic{}, "denied", nil
	}
	documents, diagnostics, err := service.scan()
	if err != nil {
		return nil, nil, "invalid", err
	}
	for _, candidate := range documents {
		if candidate.DocID != *request.DocID {
			continue
		}
		if !effective.permits(candidate) {
			return nil, []Diagnostic{}, "denied", nil
		}
		return &Document{
			DocID: candidate.DocID, Title: candidate.Title, Project: candidate.Project, Kind: candidate.Kind,
			Tags: candidate.Tags, Sensitivity: candidate.Sensitivity, RelativePath: candidate.RelativePath,
			UpdatedAt: candidate.UpdatedAt, SHA256: candidate.SHA256, Body: candidate.Body, Provenance: candidate.Provenance,
		}, diagnostics, "ok", nil
	}
	if !effective.hasCompleteVisibility() {
		return nil, []Diagnostic{}, "denied", nil
	}
	return nil, diagnostics, "not_found", nil
}

func (service *Service) ResourceByRelativePath(relativePath string) (Resource, error) {
	normalized := filepath.ToSlash(filepath.Clean(strings.TrimSpace(relativePath)))
	if normalized == "." || normalized == "" || normalized == ".." || strings.HasPrefix(normalized, "../") || !strings.HasPrefix(normalized, "content/") {
		return Resource{}, fmt.Errorf("canonical document path is invalid")
	}
	documents, _, err := service.scan()
	if err != nil {
		return Resource{}, err
	}
	for _, document := range documents {
		if document.RelativePath != normalized {
			continue
		}
		resource := Resource{
			Project: document.Project, Kind: document.Kind, Tags: append([]string(nil), document.Tags...),
			Sensitivity: document.Sensitivity, SHA256: document.SHA256,
		}
		for _, provenance := range document.Provenance {
			resource.ProvenanceTypes = append(resource.ProvenanceTypes, provenance.Type)
		}
		return resource, nil
	}
	return Resource{}, fmt.Errorf("canonical document is not available")
}

func (service *Service) scan() ([]indexedDocument, []Diagnostic, error) {
	if _, err := os.Stat(service.contentRoot); os.IsNotExist(err) {
		return []indexedDocument{}, []Diagnostic{}, nil
	} else if err != nil {
		return nil, nil, fmt.Errorf("inspect Karte content directory: %w", err)
	}
	counts := map[string]int{}
	documents := make([]indexedDocument, 0)
	err := filepath.WalkDir(service.contentRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			counts["unreadable_path"]++
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			counts["symlink_ignored"]++
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			counts["unreadable_document"]++
			return nil
		}
		if info.Size() > maxCanonicalDocumentBytes {
			counts["document_too_large"]++
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			counts["unreadable_document"]++
			return nil
		}
		frontmatter, body := fm.ParseFrontMatter(string(data))
		if frontmatter == nil {
			counts["invalid_frontmatter"]++
			return nil
		}
		docID := strings.TrimSpace(frontmatter.DocID)
		if docID == "" {
			counts["missing_doc_id"]++
			return nil
		}
		relativePath, err := filepath.Rel(service.dataRoot, path)
		if err != nil || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
			counts["path_escape"]++
			return nil
		}
		relativePath = filepath.ToSlash(relativePath)
		project := rawString(frontmatter.Raw, "project")
		kind := rawString(frontmatter.Raw, "kind")
		if project == "" || kind == "" {
			pathProject, pathKind := projectKindFromPath(relativePath)
			if project == "" {
				project = pathProject
			}
			if kind == "" {
				kind = pathKind
			}
		}
		if !projectPattern.MatchString(project) || strings.TrimSpace(kind) == "" {
			counts["invalid_classification"]++
			return nil
		}
		sensitivity := rawString(frontmatter.Raw, "sensitivity")
		if sensitivity == "" {
			sensitivity = "internal"
		}
		if _, ok := sensitivityRank[sensitivity]; !ok {
			counts["invalid_sensitivity"]++
			return nil
		}
		title := strings.TrimSpace(frontmatter.Title)
		if title == "" {
			title = strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		}
		digest := sha256.Sum256(data)
		hash := hex.EncodeToString(digest[:])
		documents = append(documents, indexedDocument{
			DocID: docID, Title: title, Project: project, Kind: kind, Tags: fm.NormalizeTags(frontmatter.Tags),
			Sensitivity: sensitivity, RelativePath: relativePath, UpdatedAt: info.ModTime().UTC().Format(time.RFC3339),
			SHA256: hash, Body: body,
			Provenance: []ProvenanceRef{{Type: "canonical", Reference: relativePath, SHA256: hash}},
		})
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("scan Karte content: %w", err)
	}

	byDocID := map[string]int{}
	for _, document := range documents {
		byDocID[document.DocID]++
	}
	filtered := documents[:0]
	for _, document := range documents {
		if byDocID[document.DocID] > 1 {
			continue
		}
		filtered = append(filtered, document)
	}
	for _, count := range byDocID {
		if count > 1 {
			counts["duplicate_doc_id"] += count
		}
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].DocID < filtered[j].DocID })
	diagnostics := make([]Diagnostic, 0, len(counts))
	for code, count := range counts {
		if count > 0 {
			diagnostics = append(diagnostics, Diagnostic{Code: code, Count: count})
		}
	}
	sort.Slice(diagnostics, func(i, j int) bool { return diagnostics[i].Code < diagnostics[j].Code })
	return filtered, diagnostics, nil
}

func rawString(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return strings.ToLower(strings.TrimSpace(value))
}

func projectKindFromPath(relativePath string) (string, string) {
	parts := strings.Split(relativePath, "/")
	if len(parts) >= 5 && parts[0] == "content" && parts[1] == "projects" {
		return strings.ToLower(parts[2]), strings.ToLower(parts[3])
	}
	return "", ""
}

func lexicalScore(query SearchQuery, document indexedDocument) float64 {
	phrase := strings.ToLower(strings.TrimSpace(query.Text))
	title := strings.ToLower(document.Title)
	body := strings.ToLower(document.Body)
	project := strings.ToLower(document.Project)
	kind := strings.ToLower(document.Kind)
	tags := strings.ToLower(strings.Join(document.Tags, " "))
	score := 0.0
	if strings.Contains(title, phrase) {
		score += 8
	}
	if strings.Contains(tags, phrase) {
		score += 5
	}
	if strings.Contains(body, phrase) {
		score += 3
	}
	for _, token := range queryTokens(phrase) {
		if strings.Contains(title, token) {
			score += 3
		}
		if strings.Contains(tags, token) {
			score += 2
		}
		if strings.Contains(project, token) || strings.Contains(kind, token) {
			score += 1
		}
		if strings.Contains(body, token) {
			score += 1
		}
	}
	return score
}

func queryTokens(value string) []string {
	seen := map[string]bool{}
	tokens := []string{}
	for _, token := range strings.FieldsFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
	}) {
		token = strings.TrimSpace(token)
		if token != "" && !seen[token] {
			seen[token] = true
			tokens = append(tokens, token)
		}
	}
	return tokens
}

func selectSnippet(body, query string) string {
	paragraphs := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n\n")
	needles := append([]string{strings.ToLower(strings.TrimSpace(query))}, queryTokens(strings.ToLower(query))...)
	for _, paragraph := range paragraphs {
		lower := strings.ToLower(paragraph)
		for _, needle := range needles {
			if needle != "" && strings.Contains(lower, needle) {
				return truncateRunes(strings.TrimSpace(paragraph), 320)
			}
		}
	}
	return truncateRunes(strings.TrimSpace(body), 320)
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}
