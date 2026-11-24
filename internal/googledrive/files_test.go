package googledrive

import (
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDriveMarkdownRoundTrip(t *testing.T) {
	const metadata = `{"id":"sample","name":"note.md","mimeType":"text/markdown","capabilities":{"canEdit":true}}`
	initial := "---\r\ndoc_id: note-1\r\n---\r\n# 初回\r\n"
	edited := strings.Replace(initial, "初回", "更新", 1)
	var mu sync.Mutex
	content := ""
	updates := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer access" {
			t.Error("Drive request is missing its bearer token")
		}
		query := r.URL.Query()
		switch {
		case r.Method == "POST" && r.URL.Path == "/upload/files":
			contentType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if query.Get("uploadType") != "multipart" || err != nil || contentType != "multipart/related" {
				t.Error("creation did not send a multipart/related upload")
				w.WriteHeader(400)
				return
			}
			reader := multipart.NewReader(r.Body, params["boundary"])
			first, err := reader.NextPart()
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			var file File
			if err := json.NewDecoder(first).Decode(&file); err != nil || file.Name != "note.md" || file.MIME != "text/markdown" {
				t.Errorf("unexpected upload metadata: %+v, %v", file, err)
			}
			second, err := reader.NextPart()
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			body, err := io.ReadAll(second)
			if err != nil || string(body) != initial {
				t.Errorf("upload did not preserve Markdown bytes: %q, %v", body, err)
			}
			content = string(body)
			_, _ = io.WriteString(w, metadata)
		case r.Method == "GET" && r.URL.Path == "/files":
			if query.Get("pageToken") == "next" {
				_, _ = io.WriteString(w, `{"files":[`+metadata+`]}`)
			} else {
				_, _ = io.WriteString(w, `{"nextPageToken":"next","files":[{"id":"google-doc","name":"other.md","mimeType":"application/vnd.google-apps.document"}]}`)
			}
		case r.Method == "GET" && r.URL.Path == "/files/sample":
			if query.Get("alt") == "media" {
				_, _ = io.WriteString(w, content)
			} else {
				_, _ = io.WriteString(w, metadata)
			}
		case r.Method == "PATCH" && r.URL.Path == "/upload/files/sample":
			if query.Get("uploadType") != "media" {
				t.Error("update did not send a media upload")
			}
			body, _ := io.ReadAll(r.Body)
			content = string(body)
			updates++
			_, _ = io.WriteString(w, metadata)
		default:
			http.NotFound(w, r)
		}
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	service := New()
	service.driveURL, service.uploadURL = server.URL, server.URL+"/upload"
	service.token = token{AccessToken: "access", expiresAt: time.Now().Add(time.Hour)}

	created, err := service.Create(t.Context(), "note.md", initial)
	if err != nil || created.Content != initial {
		t.Fatalf("create returned %+v, %v", created, err)
	}
	files, err := service.List(t.Context())
	if err != nil || len(files) != 1 || files[0].ID != created.File.ID {
		t.Fatalf("list returned %+v, %v", files, err)
	}
	loaded, err := service.Load(t.Context(), created.File.ID)
	if err != nil || loaded.Content != initial {
		t.Fatalf("load returned %+v, %v", loaded, err)
	}
	saved, err := service.Save(t.Context(), loaded.File.ID, loaded.Checksum, edited)
	if err != nil || saved.File.ID != loaded.File.ID {
		t.Fatalf("save returned %+v, %v", saved, err)
	}
	loaded, err = service.Load(t.Context(), loaded.File.ID)
	if err != nil || loaded.Content != edited || loaded.Checksum != saved.Checksum {
		t.Fatalf("reload returned %+v, %v", loaded, err)
	}

	mu.Lock()
	content = "Edited in Google Drive"
	mu.Unlock()
	if _, err := service.Save(t.Context(), saved.File.ID, saved.Checksum, "stale edit"); err == nil {
		t.Fatal("saved stale content over a browser edit")
	}
	mu.Lock()
	defer mu.Unlock()
	if updates != 1 || content != "Edited in Google Drive" {
		t.Fatalf("unexpected server state after a stale save: %d updates, %q", updates, content)
	}
}
