package main

import "testing"

func TestSetHTMLDataPrintoutAttrWithDoctype(t *testing.T) {
	in := `<!doctype html><meta charset="utf-8"><body><article>ok</article></body>`
	got := setHTMLDataPrintoutAttr(in, "B5")
	want := `<!doctype html><html data-printout="B5"><meta charset="utf-8"><body><article>ok</article></body></html>`
	if got != want {
		t.Fatalf("unexpected html:\nwant: %s\ngot:  %s", want, got)
	}
}

func TestSetHTMLDataPrintoutAttrReplacesExistingAttr(t *testing.T) {
	in := `<html data-printout="A4"><body></body></html>`
	got := setHTMLDataPrintoutAttr(in, "B5")
	want := `<html data-printout="B5"><body></body></html>`
	if got != want {
		t.Fatalf("unexpected html:\nwant: %s\ngot:  %s", want, got)
	}
}
