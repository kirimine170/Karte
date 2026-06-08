package main

import "testing"

func TestGraphNodeKindForPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{path: "content/notes/example.md", want: "note"},
		{path: "content/notes/example.board.md", want: "board"},
		{path: "content/notes/EXAMPLE.BOARD.MD", want: "board"},
	}

	for _, tt := range tests {
		if got := graphNodeKindForPath(tt.path); got != tt.want {
			t.Fatalf("graphNodeKindForPath(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestGraphNodeDefaultTitleForPath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{path: "content/notes/example.md", want: "example"},
		{path: "content/notes/example.board.md", want: "example"},
	}

	for _, tt := range tests {
		if got := graphNodeDefaultTitleForPath(tt.path); got != tt.want {
			t.Fatalf("graphNodeDefaultTitleForPath(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}
