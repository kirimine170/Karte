package googledrive

import (
	"bytes"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"karte/internal/frontmatter"
	"karte/internal/webpchunk"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"golang.org/x/net/html"
)

const tagLine = "Karte tags:"
const linkLine = "Karte link:"

var wikiPattern = regexp.MustCompile("\\[\\[([^|\\]\\r\\n]+)(?:\\|[^\\]\\r\\n]*)?\\]\\]")
var richDescription = regexp.MustCompile("(?i)<(?:p|div|br|span|a)(?:[ >\\n\\t]|/>)")

type relationMetadata struct {
	title    string
	tags     []string
	linkIDs  []string
	relative []string
	wiki     []string
}

func normalizeRelationTags(tags []string) []string {
	result := []string{}
	seen := map[string]bool{}
	for _, tag := range tags {
		value := frontmatter.NormalizeTag(tag)
		key := strings.ToLower(value)
		if key != "" && !seen[key] {
			seen[key] = true
			result = append(result, value)
		}
	}
	return result
}

func descriptionMetadata(description string) (tags []string, tagLineFound bool, links []string) {
	for _, line := range strings.Split(descriptionText(description), "\n") {
		line = strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(line, tagLine); ok {
			tagLineFound = true
			tags = append(tags, frontmatter.NormalizeTags(value)...)
		}
		if value, ok := strings.CutPrefix(line, linkLine); ok {
			if id := linkedDriveID(strings.Trim(strings.TrimSpace(value), "<>")); id != "" && !slices.Contains(links, id) {
				links = append(links, id)
			}
		}
	}
	return normalizeRelationTags(tags), tagLineFound, links
}

// Drive descriptions can be edited as rich text. Only their text and known
// Drive hyperlinks are relevant to the line format.
func descriptionText(description string) string {
	if !richDescription.MatchString(description) {
		return description
	}
	z := html.NewTokenizer(strings.NewReader(description))
	var out strings.Builder
	var link string
	var linkStart int
	for {
		switch z.Next() {
		case html.ErrorToken:
			return out.String()
		case html.TextToken:
			out.Write(z.Text())
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
			t := z.Token()
			switch t.Data {
			case "p", "div", "br", "li":
				out.WriteByte('\n')
			case "a":
				if t.Type == html.StartTagToken {
					link, linkStart = "", out.Len()
					for _, attr := range t.Attr {
						if attr.Key == "href" && linkedDriveID(attr.Val) != "" {
							link = attr.Val
						}
					}
				} else if t.Type == html.EndTagToken && link != "" {
					current := out.String()
					start := strings.LastIndexByte(current[:linkStart], '\n') + 1
					if strings.TrimSpace(current[start:linkStart]) == linkLine {
						out.Reset()
						out.WriteString(current[:linkStart])
						out.WriteString(link)
					}
					link = ""
				}
			}
		}
	}
}

func markdownRelationMetadata(content string) relationMetadata {
	var result relationMetadata
	meta, body := frontmatter.ParseFrontMatter(strings.TrimPrefix(content, "\uFEFF"))
	if meta != nil {
		result.title = strings.TrimSpace(meta.Title)
		result.tags = frontmatter.NormalizeTags(meta.Tags)
	}
	source := []byte(body)
	doc := goldmark.DefaultParser().Parse(text.NewReader(source))
	var wikiText strings.Builder
	flushWiki := func() {
		for _, match := range wikiPattern.FindAllStringSubmatch(wikiText.String(), -1) {
			result.wiki = append(result.wiki, strings.TrimSpace(match[1]))
		}
		wikiText.Reset()
	}
	addLink := func(destination string, allowRelative bool) {
		if id := linkedDriveID(destination); id != "" {
			result.linkIDs = append(result.linkIDs, id)
		} else if u, err := url.Parse(destination); allowRelative && err == nil && u.Scheme == "" && u.Host == "" && u.Path != "" {
			result.relative = append(result.relative, u.Path)
		}
	}
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		switch v := node.(type) {
		case *ast.Paragraph, *ast.Heading:
			flushWiki()
		case *ast.Text:
			if entering {
				wikiText.Write(v.Segment.Value(source))
				if v.SoftLineBreak() || v.HardLineBreak() {
					flushWiki()
				}
			}
		case *ast.Link:
			if entering {
				flushWiki()
				addLink(string(v.Destination), true)
				return ast.WalkSkipChildren, nil
			}
		case *ast.Image:
			if entering {
				flushWiki()
				addLink(string(v.Destination), true)
				return ast.WalkSkipChildren, nil
			}
		case *ast.AutoLink:
			if entering {
				flushWiki()
				addLink(string(v.URL(source)), false)
			}
		case *ast.CodeSpan:
			flushWiki()
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	flushWiki()
	return result
}

func linkedDriveID(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	var id string
	switch u.Hostname() {
	case "drive.google.com":
		if u.Path == "/open" && len(u.Query()["id"]) == 1 {
			id = u.Query().Get("id")
		} else if len(parts) >= 3 && parts[0] == "file" && parts[1] == "d" {
			id = parts[2]
		}
	case "docs.google.com":
		if len(parts) >= 3 && (parts[0] == "document" || parts[0] == "spreadsheets") && parts[1] == "d" {
			id = parts[2]
		}
	}
	if fileIDPattern.MatchString(id) {
		return id
	}
	return ""
}

func openDriveURL(id string) string {
	return "https://drive.google.com/open?id=" + url.QueryEscape(id)
}

func describeRelations(tags []string, includeTags bool, links []string) string {
	var lines []string
	if includeTags {
		lines = append(lines, tagLine+" "+strings.Join(normalizeRelationTags(tags), ", "))
	}
	seen := map[string]bool{}
	for _, id := range links {
		if !seen[id] && fileIDPattern.MatchString(id) {
			lines = append(lines, linkLine+" "+openDriveURL(id))
			seen[id] = true
		}
	}
	return strings.Join(lines, "\n")
}

func initialDescription(kind Kind, mimeType string, content []byte, knownTags []string) string {
	var links []string
	switch {
	case kind == Markdown:
		note := markdownRelationMetadata(string(content))
		knownTags = append(knownTags, note.tags...)
		links = note.linkIDs
	case kind == Photo && mimeType == "image/webp":
		if tags, err := webpchunk.ReadTags(bytes.NewReader(content)); err == nil {
			knownTags = append(knownTags, tags...)
		}
	}
	tags := normalizeRelationTags(knownTags)
	return describeRelations(tags, len(tags) > 0, links)
}
