package printout

import "testing"

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"":         Infinite,
		"   ":      Infinite,
		"infinite": Infinite,
		"INFINITE": Infinite,
		"a4":       "A4",
		"B6":       "B6",
		"foo":      Infinite,
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Fatalf("Normalize(%q)=%q want=%q", in, got, want)
		}
	}
}

func TestResolve(t *testing.T) {
	a4 := Resolve("a4")
	if a4.Infinite {
		t.Fatal("a4 should not be infinite")
	}
	if a4.Name != "A4" || a4.WidthMM != 210 || a4.HeightMM != 297 {
		t.Fatalf("unexpected A4 spec: %+v", a4)
	}
	inf := Resolve("unknown")
	if !inf.Infinite || inf.Name != Infinite {
		t.Fatalf("unexpected infinite fallback: %+v", inf)
	}
}

func TestParseFromHTML(t *testing.T) {
	html := `<html data-printout="B5"><head><meta name="karte-printout" content="A4"></head><body></body></html>`
	spec := ParseFromHTML(html)
	if spec.Name != "A4" {
		t.Fatalf("expected meta precedence A4, got %s", spec.Name)
	}
	fromAttr := ParseFromHTML(`<html data-printout="b4"><body></body></html>`)
	if fromAttr.Name != "B4" {
		t.Fatalf("expected B4, got %s", fromAttr.Name)
	}
}
