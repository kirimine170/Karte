package googledrive

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAutomaticFolderSyncWritesKarteDataDirectories(t *testing.T) {
	const root, karte, nested = "my-drive", "karte", "nested"
	type sample struct {
		kind                  Kind
		name, localName, mime string
		data                  []byte
	}
	samples := []sample{
		{Markdown, "note.md", "note.md", "text/markdown", []byte("# Drive synchronization\n")},
		{Markdown, "alias.markdown", "alias.md", "text/plain", []byte("# Markdown alias\n")},
		{Sheet, "table", "table.csv", sheetMIME, []byte("name,value\nexample,21\n")},
	}
	for _, entry := range []struct{ ext, mime string }{
		{"jpg", "image/jpeg"}, {"jpeg", "image/jpeg"}, {"png", "image/png"},
		{"gif", "image/gif"}, {"webp", "image/webp"},
	} {
		data, err := base64.StdEncoding.DecodeString(folderTestPhoto(t, entry.mime))
		if err != nil {
			t.Fatal(err)
		}
		name := "photo." + entry.ext
		samples = append(samples, sample{Photo, name, name, entry.mime, data})
	}
	type remote struct {
		File
		data []byte
	}
	files := map[string]remote{
		"root":    {File: File{ID: root, MIME: folderMIME}},
		karte:     {File: File{ID: karte, Name: "Karte", MIME: folderMIME, Parents: []string{root}}},
		nested:    {File: File{ID: nested, Name: "Incoming", MIME: folderMIME, Parents: []string{karte}}},
		"outside": {File: File{ID: "outside", Name: "excluded.md", MIME: "text/markdown", Parents: []string{root}}},
		"unsafe":  {File: File{ID: "unsafe", Name: "../escape.md", MIME: "text/markdown", Parents: []string{nested}}},
	}
	for i, item := range samples {
		id := fmt.Sprintf("external-%d", i)
		files[id] = remote{File{ID: id, Name: item.name, MIME: item.mime, Parents: []string{nested}}, item.data}
	}
	var mu sync.Mutex
	var uploads []remote
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		route := r.URL.Path
		switch {
		case route == "/files" && r.Method == http.MethodGet:
			var result []File
			for _, file := range files {
				for _, parent := range file.Parents {
					if r.URL.Query().Get("q") == "trashed = false and '"+parent+"' in parents" {
						result = append(result, file.File)
					}
				}
			}
			_ = json.NewEncoder(w).Encode(map[string][]File{"files": result})
		case route == "/files" && r.Method == http.MethodPost:
			var folder File
			if json.NewDecoder(r.Body).Decode(&folder) != nil || folder.Name != "Outgoing" || folder.MIME != folderMIME || !slices.Equal(folder.Parents, []string{karte}) {
				t.Errorf("invalid new folder: %+v", folder)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			folder.ID = "outgoing"
			files[folder.ID] = remote{File: folder}
			_ = json.NewEncoder(w).Encode(folder)
		case route == "/upload/files" && r.Method == http.MethodPost:
			file, mediaType, data := driveUpload(t, r)
			if !slices.Equal(file.Parents, []string{"outgoing"}) || file.AppProperties[folderSyncProperty] == "" {
				t.Errorf("unexpected upload metadata: %+v", file)
			}
			if file.MIME == sheetMIME && !strings.HasPrefix(mediaType, "text/csv") {
				t.Error("CSV upload was not converted to a Google spreadsheet")
			}
			file.ID = fmt.Sprintf("uploaded-%d", len(uploads))
			stored := remote{file, data}
			files[file.ID] = stored
			uploads = append(uploads, stored)
			if file.MIME == "image/gif" {
				// Drive accepts the upload, but the response to Karte is lost.
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			_ = json.NewEncoder(w).Encode(file)
		case r.Method == http.MethodGet && strings.HasPrefix(route, "/files/"):
			id := strings.TrimSuffix(strings.TrimPrefix(route, "/files/"), "/export")
			file, ok := files[id]
			if !ok {
				http.NotFound(w, r)
				return
			}
			if r.URL.Query().Get("alt") == "media" || strings.HasSuffix(route, "/export") {
				if (file.MIME == sheetMIME) != (r.URL.Query().Get("mimeType") == "text/csv") {
					t.Errorf("wrong download mechanism for %s", file.MIME)
				}
				_, _ = w.Write(file.data)
			} else {
				_ = json.NewEncoder(w).Encode(file.File)
			}
		default:
			t.Errorf("unexpected request %s %s", r.Method, route)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dataDir := t.TempDir()
	put := func(rel string, data []byte) {
		t.Helper()
		file := filepath.Join(dataDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, data, 0644); err != nil {
			t.Fatal(err)
		}
		settled := time.Now().Add(-5 * time.Second)
		if err := os.Chtimes(file, settled, settled); err != nil {
			t.Fatal(err)
		}
	}
	put("content/README.md", []byte("# Existing local-only document\n"))
	put("content/Incoming/note.md", []byte("# Existing note with same name\n"))
	account := Account{Email: "sync@example.invalid", PermissionID: "sync-user"}
	reader := NewRelationIndex()
	reader.service.driveURL, reader.service.account = server.URL, account
	reader.service.token = token{AccessToken: "reader", expiresAt: time.Now().Add(time.Hour)}
	writer := formatTestService(server.URL)
	engine, err := NewFolderSync(t.Context(), dataDir, karte, reader, writer)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range samples {
		name := item.name
		if item.kind == Sheet {
			name = item.localName
		}
		put(syncLocalDir(item.kind)+"/Outgoing/"+name, item.data)
	}
	readOnly, err := engine.Run(t.Context())
	if err != nil || readOnly.Downloaded != len(samples) || readOnly.WriterReady || readOnly.Uploaded != 0 {
		t.Fatalf("read-only cycle: %+v %v", readOnly, err)
	}
	for _, item := range samples {
		file := filepath.Join(dataDir, syncLocalDir(item.kind), "Incoming", item.localName)
		if item.name == "note.md" {
			if got, err := os.ReadFile(file); err != nil || string(got) != "# Existing note with same name\n" {
				t.Fatalf("synchronization changed existing local data: %q %v", got, err)
			}
			matches, err := filepath.Glob(filepath.Join(filepath.Dir(file), "note (Drive *).md"))
			if err != nil || len(matches) != 1 {
				t.Fatalf("missing collision-safe download: %v %v", matches, err)
			}
			file = matches[0]
		}
		if got, err := os.ReadFile(file); err != nil || !bytes.Equal(got, item.data) {
			t.Fatalf("missing or invalid local copy %s: %v", file, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dataDir, "content", "escape.md")); !os.IsNotExist(err) {
		t.Fatalf("unsafe filename escaped its folder: %v", err)
	}
	writer.account = account
	result, err := engine.Run(t.Context())
	if err != nil || result.Uploaded != len(samples)-1 || result.Downloaded != 0 {
		t.Fatalf("write cycle: %+v %v", result, err)
	}
	engine.Close()
	resumed, err := NewFolderSync(t.Context(), dataDir, karte, reader, writer)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	result, err = resumed.Run(t.Context())
	if err != nil || result.Downloaded != 0 || result.Uploaded != 0 || len(resumed.state.Pending) != 0 {
		t.Fatalf("resume did not reconcile the interrupted upload: %+v %v", result, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(uploads) != len(samples) {
		t.Fatalf("unexpected remote upload count: %d", len(uploads))
	}
	for _, item := range samples {
		if !slices.ContainsFunc(uploads, func(upload remote) bool { return upload.Name == item.name && bytes.Equal(upload.data, item.data) }) {
			t.Errorf("missing remote file %s", item.name)
		}
	}
}
