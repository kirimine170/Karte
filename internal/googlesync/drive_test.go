package googlesync

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"karte/internal/canonical"
	"karte/internal/decisionlog"
)

func TestDriveAppDataRequestContract(t *testing.T) {
	item := revision{Snapshot: decisionlog.Snapshot{Records: map[string]decisionlog.Record{}}}
	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	id := canonical.Hash(raw)
	prefix := "karte-decisions-test-"
	mux := http.NewServeMux()
	mux.HandleFunc("POST /upload", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("uploadType") != "multipart" {
			t.Error("expected multipart Drive upload")
		}
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "multipart/related" {
			t.Error("unexpected upload content type")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		reader := multipart.NewReader(r.Body, params["boundary"])
		metadata, err := reader.NextPart()
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var file struct {
			Name    string   `json:"name"`
			Parents []string `json:"parents"`
		}
		if json.NewDecoder(metadata).Decode(&file) != nil || file.Name != prefix+id ||
			len(file.Parents) != 1 || file.Parents[0] != "appDataFolder" {
			t.Error("upload was not restricted to Drive app data")
		}
		part, err := reader.NextPart()
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		content, err := io.ReadAll(part)
		if err != nil || canonical.Hash(content) != id {
			t.Error("uploaded revision bytes do not match the content hash")
		}
		_, _ = io.WriteString(w, `{"id":"file-id"}`)
	})
	mux.HandleFunc("GET /files", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("spaces") != "appDataFolder" || !strings.Contains(r.URL.Query().Get("q"), prefix) {
			t.Error("list was not restricted to the sync namespace")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"files": []map[string]string{{"id": "file-id", "name": prefix + id}}})
	})
	mux.HandleFunc("GET /files/file-id", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("alt") != "media" {
			t.Error("expected revision download")
		}
		_, _ = w.Write(raw)
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("Drive request did not authenticate")
		}
		mux.ServeHTTP(w, r)
	}))
	defer server.Close()
	drive := driveStore{client: server.Client(), token: "test-token", prefix: prefix, filesURL: server.URL + "/files", uploadURL: server.URL + "/upload"}
	ctx := context.Background()
	if err := drive.create(ctx, id, item, raw); err != nil {
		t.Fatal(err)
	}
	files, err := drive.list(ctx)
	if err != nil || len(files) != 1 {
		t.Fatalf("failed to enumerate revision: %v", err)
	}
	result, err := drive.read(ctx, files[id])
	if err != nil || result.Snapshot.Records == nil {
		t.Fatalf("failed to download revision: %v", err)
	}
}
