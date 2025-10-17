package runner

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type sseHub struct{ ch chan struct{} }

func newHub() *sseHub { return &sseHub{ch: make(chan struct{}, 1)} }

func (h *sseHub) notify() {
	select {
	case h.ch <- struct{}{}:
	default:
	}
}

func (h *sseHub) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.ch:
			fmt.Fprintf(w, "data: reload\n\n")
			flusher.Flush()
		}
	}
}

// very small middleware: if serving .html, inject livereload script
func injectLiveReload(root string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Normalize URL path and map "/" or "/dir/" to "index.html"
		reqPath := r.URL.Path
		var rel string
		if strings.HasSuffix(reqPath, "/") { // e.g. "/" or "/foo/" -> "index.html"
			rel = strings.TrimPrefix(reqPath, "/") + "index.html"
		} else if strings.HasSuffix(reqPath, ".html") {
			rel = strings.TrimPrefix(reqPath, "/")
		} else {
			next.ServeHTTP(w, r)
			return
		}
		p := filepath.Join(root, filepath.Clean(rel))
		b, err := os.ReadFile(p)
		if err != nil {
			// Fallback to default handler if not found
			next.ServeHTTP(w, r)
			return
		}
		snippet := `<script>const es=new EventSource('/__livereload');es.onmessage=()=>location.reload();</script>`
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(append(b, []byte(snippet)...))
	})
}

