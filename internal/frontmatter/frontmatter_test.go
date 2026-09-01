package frontmatter

import (
	"strings"
	"testing"
)

func TestParseFrontMatterPrintout(t *testing.T) {
	content := "---\nprintout: a4\ntitle: test\n---\nhello"
	fm, body := ParseFrontMatter(content)
	if fm == nil {
		t.Fatal("frontmatter should parse")
	}
	if fm.Printout != "A4" {
		t.Fatalf("printout should normalize to A4, got %q", fm.Printout)
	}
	if strings.TrimSpace(body) != "hello" {
		t.Fatalf("unexpected body: %q", body)
	}
}

func TestFormatFrontMatterPrintout(t *testing.T) {
	fm := &FrontMatter{Title: "doc", Printout: "b5"}
	out := FormatFrontMatter(fm)
	if !strings.Contains(out, `printout: "B5"`) {
		t.Fatalf("formatted frontmatter missing normalized printout: %s", out)
	}
}

func TestNormalizePrintoutFallback(t *testing.T) {
	if got := NormalizePrintout("unknown-size"); got != "infinite" {
		t.Fatalf("expected infinite fallback, got %q", got)
	}
}

func TestParseAndFormatFrontMatterPreservesYAMLListTags(t *testing.T) {
	content := "---\ntitle: Ephy proposal\ntags:\n  - e2e\n  - karte-integration\n  - e2e\n---\nbody\n"
	parsed, body := ParseFrontMatter(content)
	if parsed == nil {
		t.Fatal("frontmatter should parse")
	}
	if parsed.Tags != "e2e,karte-integration,e2e" {
		t.Fatalf("unexpected parsed tags: %q", parsed.Tags)
	}
	formatted := FormatFrontMatter(parsed)
	if !strings.Contains(formatted, `tags: "e2e, karte-integration"`) {
		t.Fatalf("formatted frontmatter lost YAML list tags: %s", formatted)
	}
	if strings.TrimSpace(body) != "body" {
		t.Fatalf("unexpected body: %q", body)
	}
}
