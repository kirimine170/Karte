package runner

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"karte-desktop/internal/site"
)

// sanitize and resolve path under content/
func resolveContentPath(root, rel string) (abs string, ok bool) {
	rel = filepath.ToSlash(rel)
	rel = strings.TrimPrefix(rel, "/")
	if !strings.HasPrefix(rel, "content/") {
		return "", false
	}
	abs = filepath.Join(root, filepath.FromSlash(rel))
	canonical, err := filepath.Abs(abs)
	if err != nil {
		return "", false
	}
	contentRoot, _ := filepath.Abs(filepath.Join(root, "content"))
	relToContent, err := filepath.Rel(contentRoot, canonical)
	if err != nil {
		return "", false
	}
	if filepath.IsAbs(relToContent) {
		return "", false
	}
	slashRel := filepath.ToSlash(relToContent)
	if slashRel == ".." || strings.HasPrefix(slashRel, "../") {
		return "", false
	}
	return canonical, true
}

func serveEditor(root string, w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	if rel == "" {
		http.Error(w, "missing ?path=content/xxx.md", http.StatusBadRequest)
		return
	}
	if _, ok := resolveContentPath(root, rel); !ok {
		http.Error(w, "invalid path", http.StatusForbidden)
		return
	}
	if b, err := os.ReadFile(filepath.Join(root, "themes", "default", "editor.html")); err == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
		return
	}
	http.Error(w, "editor.html not found", http.StatusNotFound)
}

func handleRaw(root string, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rel := r.URL.Query().Get("path")
	abs, ok := resolveContentPath(root, rel)
	if !ok {
		http.Error(w, "invalid path", http.StatusForbidden)
		return
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(b)
}

// Store draft content under .mdsys/drafts/<path relative to root>
func draftPath(root, rel string) (string, string, bool) {
	abs, ok := resolveContentPath(root, rel)
	if !ok {
		return "", "", false
	}
	projRel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", "", false
	}
	projRel = filepath.ToSlash(projRel)
	draftAbs := filepath.Join(root, ".mdsys", "drafts", filepath.FromSlash(projRel))
	return draftAbs, projRel, true
}

// POST /__draft?path=content/xxx.md  (body: draft markdown)
func handleDraft(root string, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rel := r.URL.Query().Get("path")
	if decoded, err := url.QueryUnescape(rel); err == nil {
		rel = decoded
	}
	draftAbs, _, ok := draftPath(root, rel)
	if !ok {
		http.Error(w, "invalid path", http.StatusForbidden)
		return
	}
	defer r.Body.Close()
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(r.Body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := os.MkdirAll(filepath.Dir(draftAbs), 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(draftAbs, buf.Bytes(), 0o644); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /__preview?path=content/xxx.md -> render draft if exists otherwise source
func handlePreview(root string, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rel := r.URL.Query().Get("path")
	abs, ok := resolveContentPath(root, rel)
	if !ok {
		http.Error(w, "invalid path", http.StatusForbidden)
		return
	}
	draftAbs, _, _ := draftPath(root, rel)
	srcPath := abs
	if draftAbs != "" {
		if b, err := os.Stat(draftAbs); err == nil && !b.IsDir() {
			srcPath = draftAbs
		}
	}
	hardwrap := r.URL.Query().Get("hardwrap") == "1"
	html, _, err := site.RenderMarkdownWithOptions(root, srcPath, hardwrap)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(html))
}

func handleSave(root string, hub *sseHub, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rel := r.URL.Query().Get("path")
	abs, ok := resolveContentPath(root, rel)
	if !ok {
		http.Error(w, "invalid path", http.StatusForbidden)
		return
	}
	defer r.Body.Close()
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(r.Body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := os.WriteFile(abs, buf.Bytes(), 0o644); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if draftAbs, _, ok := draftPath(root, rel); ok {
		_ = os.Remove(draftAbs)
	}
	_ = Build(root)
	hub.notify()
	w.WriteHeader(http.StatusNoContent)
}

// ---------- List markdown files (for sidebar) ----------
func handleList(root string, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	base := filepath.Join(root, "content")
	type item struct{ Path, Title string }
	var out []item
	filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(info.Name()), ".md") {
			rel, _ := filepath.Rel(root, p)
			title := info.Name()
			if b, err := os.ReadFile(p); err == nil {
				s := string(b)
				if strings.HasPrefix(s, "---") {
					if i := strings.Index(s, "\n---"); i > 0 {
						fm := s[:i]
						for _, ln := range strings.Split(fm, "\n") {
							if strings.HasPrefix(strings.TrimSpace(ln), "title:") {
								title = strings.TrimSpace(strings.TrimPrefix(ln, "title:"))
								title = strings.Trim(title, `"' `)
								break
							}
						}
					}
				}
			}
			out = append(out, item{Path: filepath.ToSlash(rel), Title: title})
		}
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(out)
}
