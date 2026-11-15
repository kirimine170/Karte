package portablepackage

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
	"gopkg.in/yaml.v3"

	"karte/internal/contextcore"
	fm "karte/internal/frontmatter"
)

var (
	ErrNotStandalone  = errors.New("the Markdown must be standalone")
	legacyIDPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	decisionIDPattern = regexp.MustCompile(`^decision-[0-9a-f]{64}$`)
	directivePattern  = regexp.MustCompile(`(?im)^\s*@[a-z][a-z0-9_-]*(?:\(|\s)`)
)

// PersonalProjection serializes only metadata supported by this format. The
// policy resource must describe the exact source bytes and be authorized by
// the calling application before projection.
func PersonalProjection(source []byte, titleFallback string, resource contextcore.Resource) (Snapshot, error) {
	if !utf8.Valid(source) || hash(source) != resource.SHA256 {
		return Snapshot{}, ErrNotStandalone
	}
	frontmatter, body := fm.ParseFrontMatter(string(source))
	if frontmatter == nil {
		return Snapshot{}, ErrNotStandalone
	}
	// Marp has its own rendering and metadata rules.
	if frontmatter.Marp {
		return Snapshot{}, ErrNotStandalone
	}
	doc := document{
		DocID: strings.TrimSpace(frontmatter.DocID), Title: strings.TrimSpace(frontmatter.Title),
		Project: resource.Project, Kind: resource.Kind, Tags: strings.Join(resource.Tags, ", "),
		Sensitivity: resource.Sensitivity,
	}
	if doc.Title == "" {
		doc.Title = titleFallback
	}
	yamlBytes, err := yaml.Marshal(doc)
	if err != nil {
		return Snapshot{}, err
	}
	return New(bytes.Join([][]byte{[]byte("---\n"), yamlBytes, []byte("---\n"), []byte(body)}, nil))
}

type document struct {
	DocID       string `yaml:"doc_id"`
	Title       string `yaml:"title"`
	Project     string `yaml:"project"`
	Kind        string `yaml:"kind"`
	Tags        string `yaml:"tags,omitempty"`
	Sensitivity string `yaml:"sensitivity"`
}

// Resource describes the received content for the local Context policy.
func (snapshot Snapshot) Resource() (contextcore.Resource, error) {
	doc, err := parseDocument(snapshot.Markdown)
	if err != nil {
		return contextcore.Resource{}, err
	}
	return contextcore.Resource{
		Project: doc.Project, Kind: doc.Kind, Tags: fm.NormalizeTags(doc.Tags),
		Sensitivity: doc.Sensitivity, ProvenanceTypes: []string{"canonical"},
		SHA256: snapshot.Manifest.Blob.SHA256,
	}, nil
}

func (snapshot Snapshot) Title() string {
	frontmatter, _ := fm.ParseFrontMatter(string(snapshot.Markdown))
	return frontmatter.Title
}

func parseDocument(data []byte) (document, error) {
	var doc document
	if len(data) > MaxMarkdownBytes || !utf8.Valid(data) || bytes.ContainsRune(data, 0) {
		return doc, ErrNotStandalone
	}
	metadata, body, ok := bytes.Cut(bytes.TrimPrefix(data, []byte("---\n")), []byte("\n---\n"))
	if !bytes.HasPrefix(data, []byte("---\n")) || !ok {
		return doc, ErrNotStandalone
	}
	decoder := yaml.NewDecoder(bytes.NewReader(metadata))
	decoder.KnownFields(true)
	if err := decoder.Decode(&doc); err != nil {
		return doc, ErrNotStandalone
	}
	// An exact projected metadata representation also rules out YAML aliases,
	// extra documents and alternate parsings by the editor's frontmatter parser.
	canonicalMetadata, err := yaml.Marshal(doc)
	if err != nil || !bytes.Equal(metadata, bytes.TrimSuffix(canonicalMetadata, []byte("\n"))) {
		return doc, ErrNotStandalone
	}
	if !legacyIDPattern.MatchString(doc.DocID) || decisionIDPattern.MatchString(doc.DocID) ||
		strings.TrimSpace(doc.Title) == "" || utf8.RuneCountInString(doc.Title) > 240 || strings.ContainsAny(doc.Title, "\r\n\t\x00") {
		return doc, ErrNotStandalone
	}
	if !legacyIDPattern.MatchString(doc.Project) || !legacyIDPattern.MatchString(doc.Kind) {
		return doc, ErrNotStandalone
	}
	switch doc.Sensitivity {
	case "public", "internal", "confidential", "restricted":
	default:
		return doc, ErrNotStandalone
	}
	// The current editor expands Karte directives and wiki links outside the
	// CommonMark parser. Keep them out of the personal snapshot format.
	if directivePattern.Match(body) || bytes.Contains(body, []byte("[[")) ||
		bytes.Contains(data, []byte("runtime_record:")) || bytes.Contains(data, []byte("karte-v2:item")) {
		return doc, ErrNotStandalone
	}
	// Match the production renderer's extensions when recognizing links and HTML.
	parser := goldmark.New(goldmark.WithExtensions(extension.GFM, extension.Footnote, extension.DefinitionList)).Parser()
	err = ast.Walk(parser.Parse(text.NewReader(body)), func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node := node.(type) {
		case *ast.Link:
			if bytes.HasPrefix(node.Destination, []byte("#")) {
				break
			}
			return ast.WalkStop, ErrNotStandalone
		case *ast.Image, *ast.RawHTML, *ast.HTMLBlock, *ast.AutoLink:
			return ast.WalkStop, ErrNotStandalone
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return doc, fmt.Errorf("embedded content, links and raw HTML are outside this format: %w", err)
	}
	return doc, nil
}
