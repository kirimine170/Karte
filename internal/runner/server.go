package runner

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
)

func Serve(root string, port int) error {
	// convert to absolute path
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	root = absRoot

	// initial build
	if err := Build(root); err != nil {
		return err
	}

	hub := newHub()

	mux := http.NewServeMux()
	publicDir := filepath.Join(root, "public")
	mux.Handle("/", http.FileServer(http.Dir(publicDir)))
	mux.HandleFunc("/__livereload", hub.serve)
	// editor assets
	mux.HandleFunc("/__editor.js", func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Join(root, "themes", "default", "editor.js")
		b, err := os.ReadFile(p)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write(b)
	})

	// editor endpoints
	mux.HandleFunc("/__edit", func(w http.ResponseWriter, r *http.Request) { serveEditor(root, w, r) })
	mux.HandleFunc("/__raw", func(w http.ResponseWriter, r *http.Request) { handleRaw(root, w, r) })
	mux.HandleFunc("/__save", func(w http.ResponseWriter, r *http.Request) { handleSave(root, hub, w, r) })
	mux.HandleFunc("/__list", func(w http.ResponseWriter, r *http.Request) { handleList(root, w, r) })
	mux.HandleFunc("/__draft", func(w http.ResponseWriter, r *http.Request) { handleDraft(root, w, r) })
	mux.HandleFunc("/__preview", func(w http.ResponseWriter, r *http.Request) { handlePreview(root, w, r) })

	// health
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })

	// watch content/data/themes
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()
	for _, d := range []string{"content", "data", "themes"} {
		if err := w.Add(filepath.Join(root, d)); err != nil {
			return err
		}
	}
	go func() {
		for {
			select {
			case ev := <-w.Events:
				if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
					log.Println("change:", ev.Name)
					_ = Build(root)
					hub.notify()
				}
			case err := <-w.Errors:
				log.Println("watch error:", err)
			}
		}
	}()

	srv := &http.Server{Addr: fmt.Sprintf(":%d", port), Handler: injectLiveReload(publicDir, mux)}
	log.Printf("karte serve http://localhost:%d\n", port)
	return srv.ListenAndServe()
}

