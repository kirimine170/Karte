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

func TestFormatFrontMatterPreservesMarp(t *testing.T) {
	fm := &FrontMatter{
		Title: "deck",
		Theme: "default",
		Marp:  true,
		Raw: map[string]any{
			"paginate": true,
			"size":     "16:9",
		},
	}
	out := FormatFrontMatter(fm)
	if !strings.Contains(out, "marp: true") {
		t.Fatalf("formatted frontmatter missing marp flag: %s", out)
	}
	if strings.Count(out, "marp:") != 1 {
		t.Fatalf("formatted frontmatter should include marp once: %s", out)
	}
	if !strings.Contains(out, `theme: "default"`) || !strings.Contains(out, `size: "16:9"`) || !strings.Contains(out, "paginate: true") {
		t.Fatalf("formatted frontmatter missing marp fields: %s", out)
	}
}

func TestNormalizePrintoutFallback(t *testing.T) {
	if got := NormalizePrintout("unknown-size"); got != "infinite" {
		t.Fatalf("expected infinite fallback, got %q", got)
	}
}
