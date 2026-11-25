package googledrive

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"karte/internal/webpchunk"
)

func TestRelationReadOnlyAuthorizationDoesNotReplaceWriter(t *testing.T) {
	t.Setenv("KARTE_GOOGLE_OAUTH_EXPECTED_ACCOUNT", "")
	setDesktopCredentials(t, `{"installed":{"client_id":"karte-test.apps.googleusercontent.com"}}`)
	writer := New()
	writer.account = Account{Email: "test@example.com", PermissionID: "member"}
	writer.token = token{AccessToken: "writer"}
	index := NewRelationIndex()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			granted := readScope
			if r.Form.Get("code") == "writer-scope" {
				granted = scope
			}
			_ = json.NewEncoder(w).Encode(token{AccessToken: "reader", Scope: granted, Type: "Bearer", ExpiresIn: 3600})
		case "/about":
			_, _ = io.WriteString(w, `{"user":{"emailAddress":"test@example.com","permissionId":"member"}}`)
		default:
			t.Errorf("read-only session sent an unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	index.service.tokenURL, index.service.driveURL = server.URL+"/token", server.URL
	authorize := func(code string) (Account, error) {
		return index.Authorize(t.Context(), func(raw string) {
			target, err := url.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			query := target.Query()
			if query.Get("scope") != readScope || query.Has("trigger_onepick") || query.Get("login_hint") != "test@example.com" {
				t.Fatal("search authorization did not request its own read-only scope")
			}
			callback := url.Values{"state": {query.Get("state")}}
			if code == "" {
				callback.Set("error", "access_denied")
				assertCallback(t, query.Get("redirect_uri")+"?"+callback.Encode(), http.StatusBadRequest)
			} else {
				callback.Set("code", code)
				assertCallback(t, query.Get("redirect_uri")+"?"+callback.Encode(), http.StatusOK)
			}
		}, writer.Account())
	}
	for _, denied := range []string{"", "writer-scope"} {
		if _, err := authorize(denied); err == nil {
			t.Fatal("search connected without a read-only grant")
		}
		if writer.token.AccessToken != "writer" || index.Account().Email != "" {
			t.Fatal("failure changed one of the sessions")
		}
	}
	if account, err := authorize("test-code"); err != nil || !account.SameUser(writer.Account()) {
		t.Fatalf("authorization = %+v, %v", account, err)
	}
	if _, err := index.service.request(t.Context(), "reader", http.MethodPatch, server.URL+"/file",
		"", nil, 1000); err == nil {
		t.Fatal("read-only session accepted a write")
	}
	if writer.token.AccessToken != "writer" {
		t.Fatal("read-only authorization replaced the writer")
	}
}

func TestDriveRelationIndexFollowsFolderAndFileIDs(t *testing.T) {
	const folderType = "application/vnd.google-apps.folder"
	folder := func(id, name, parent string) File {
		return File{ID: id, Name: name, MIME: folderType, Parents: []string{parent}}
	}
	var mu sync.Mutex
	var phase, noteReads, imageReads int
	legacyWebP := testTaggedWebP(t, "旧写真")
	note := "---\ntitle: 展示ノート\ntags: [旧タグ]\n---\n[表](https://docs.google.com/spreadsheets/d/sheet/edit)\n[[Same]]\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer reader" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		encode := func(v any) { _ = json.NewEncoder(w).Encode(v) }
		page := func(next string, entries ...File) {
			encode(map[string]any{"files": entries, "nextPageToken": next})
		}
		switch r.URL.Path {
		case "/files/root":
			encode(folder("my-drive", "My Drive", ""))
		case "/files/karte":
			if phase == 2 {
				http.Error(w, "", http.StatusForbidden)
			} else if phase == 1 {
				encode(folder("karte", "改名済み", "new-parent"))
			} else {
				encode(folder("karte", "Karte", "my-drive"))
			}
		case "/files/new-parent":
			encode(folder("new-parent", "Archive", "my-drive"))
		case "/files":
			q := r.URL.Query()
			switch {
			case strings.Contains(q.Get("q"), "'root' in parents"):
				page("", folder("karte", "Karte", "my-drive"), folder("other-karte", "Karte", "my-drive"))
			case strings.Contains(q.Get("q"), "'sub' in parents"):
				page("", File{ID: "sheet", Name: "在庫", MIME: sheetMIME, Parents: []string{"sub"}, Description: "Karte tags: 展示", Version: "1"},
					File{ID: "legacy-webp", Name: "original.webp", MIME: "image/webp", Parents: []string{"sub"}, Version: "1"})
			case strings.Contains(q.Get("q"), "'karte' in parents"):
				if q.Get("pageToken") == "next" {
					page("", File{ID: "same-a", Name: "Same.md", MIME: "text/markdown", Parents: []string{"karte"}, Version: "1"},
						File{ID: "same-b", Name: "same.md", MIME: "text/markdown", Parents: []string{"karte"}, Version: "1"})
					return
				}
				title, version := "old.md", "1"
				if phase == 1 {
					title, version = "改名（拡張子なし）", "2"
				}
				entries := []File{
					folder("sub", "素材", "karte"),
					{ID: "note", Name: title, MIME: "text/markdown", Parents: []string{"karte"}, Description: "Karte tags: 展示", Version: version},
					{ID: "unrelated", Name: "no.md", MIME: "text/markdown", Parents: []string{"elsewhere"}},
					{ID: "shortcut", Name: "fake.md", MIME: "application/vnd.google-apps.shortcut", Parents: []string{"karte"}},
				}
				if phase == 0 {
					entries = append(entries, File{ID: "photo", Name: "写真", MIME: "image/png", Parents: []string{"karte"}, Version: "1"})
				}
				page("next", entries...)
			default:
				t.Errorf("listed a folder outside the search root: %s", q.Get("q"))
				http.NotFound(w, r)
			}
		case "/files/note":
			noteReads++
			_, _ = io.WriteString(w, note)
		case "/files/same-a", "/files/same-b":
			_, _ = io.WriteString(w, "# Duplicate")
		case "/files/legacy-webp":
			imageReads++
			_, _ = w.Write(legacyWebP)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	index := NewRelationIndex()
	index.service.driveURL = server.URL
	index.service.account = Account{Email: "test@example.com", PermissionID: "member"}
	index.service.token = token{AccessToken: "reader", expiresAt: time.Now().Add(time.Hour)}
	folders, err := index.Folders(t.Context())
	if err != nil || len(folders) != 2 {
		t.Fatalf("folder candidates = %+v, %v", folders, err)
	}
	check := func(snapshot RelationSnapshot, err error, count int) {
		t.Helper()
		if err != nil || len(snapshot.Files) != count {
			t.Fatalf("snapshot = %+v, %v", snapshot, err)
		}
		got := map[string]RelationFile{}
		for _, file := range snapshot.Files {
			got[file.ID] = file
		}
		main := got["note"]
		if !slices.Equal(main.Tags, []string{"展示"}) || !slices.Equal(main.LinkIDs, []string{"sheet"}) || main.Unresolved != 1 {
			t.Errorf("note metadata = %+v", main)
		}
		if !strings.Contains(got["legacy-webp"].Legacy, "Karte tags: 旧写真") {
			t.Error("legacy photo tags could not be carried into the description")
		}
		if !slices.Equal(got["sheet"].Tags, []string{"展示"}) {
			t.Error("sheet tags were not indexed from Drive metadata")
		}
	}
	first, err := index.Refresh(t.Context(), "karte")
	check(first, err, 6)
	second, err := index.Refresh(t.Context(), "karte")
	check(second, err, 6)
	mu.Lock()
	if noteReads != 1 || imageReads != 1 {
		t.Errorf("unchanged content was fetched again: note %d, photo %d", noteReads, imageReads)
	}
	phase = 1
	mu.Unlock()
	third, err := index.Refresh(t.Context(), "karte")
	check(third, err, 5)
	if third.Folder.Name != "改名済み" {
		t.Fatalf("folder move changed selection: %+v", third.Folder)
	}
	mu.Lock()
	if noteReads != 2 || imageReads != 1 {
		t.Errorf("version change did not trigger selective download: note %d, photo %d", noteReads, imageReads)
	}
	phase = 2
	mu.Unlock()
	if snapshot, err := index.Refresh(t.Context(), "karte"); err == nil || len(snapshot.Files) != 0 || len(index.cache) != 0 {
		t.Fatalf("lost access retained data: %+v, %v", snapshot, err)
	}
	index.Disconnect()
	if _, err := index.Refresh(t.Context(), "karte"); !errors.Is(err, errDisconnected) {
		t.Fatalf("refresh after disconnect = %v", err)
	}
}

func TestDescriptionMetadataAndMarkdownLinks(t *testing.T) {
	tags, hasTags, ids := descriptionMetadata(`<p>Karte tags: 展示, HACKSICK, hacksick</p><div>Karte link: <a href="https://drive.google.com/file/d/photo/view">写真</a></div>`)
	if !hasTags || !slices.Equal(tags, []string{"展示", "HACKSICK"}) || !slices.Equal(ids, []string{"photo"}) {
		t.Fatalf("description did not round-trip: %+v, %v, %+v", tags, hasTags, ids)
	}
	if tags, ok, _ := descriptionMetadata("Karte tags: "); !ok || len(tags) != 0 {
		t.Fatal("clearing tags would bring legacy tags back")
	}
	note := markdownRelationMetadata("[real](https://drive.google.com/open?id=file)\n\n[[One|label]] and ![old](../images/photo.png)\n\n```md\n[[Fake]]\n```\n")
	if !slices.Equal(note.linkIDs, []string{"file"}) || !slices.Equal(note.wiki, []string{"One"}) ||
		!slices.Equal(note.relative, []string{"../images/photo.png"}) {
		t.Fatalf("links = %+v", note)
	}
	if linkedDriveID("https://drive.google.com.example/file/d/not-a-drive-file/view") != "" {
		t.Fatal("an external URL was accepted as a Drive link")
	}
	if got := initialDescription(Markdown, "text/markdown", []byte("---\ntags: [展示]\n---\n[写真](https://drive.google.com/open?id=photo)"), nil); !strings.Contains(got, "Karte tags: 展示\nKarte link: ") {
		t.Fatalf("new note description = %q", got)
	}
	if got := initialDescription(Photo, "image/webp", testTaggedWebP(t, "旧写真"), []string{"付属"}); got != "Karte tags: 付属, 旧写真" {
		t.Fatalf("new photo description = %q", got)
	}
}

func testTaggedWebP(t *testing.T, tag string) []byte {
	t.Helper()
	chunk, err := webpchunk.CreateTagChunk([]string{tag})
	if err != nil {
		t.Fatal(err)
	}
	var result bytes.Buffer
	result.WriteString("RIFF")
	padding := len(chunk) % 2
	_ = binary.Write(&result, binary.LittleEndian, uint32(12+len(chunk)+padding))
	result.WriteString("WEBP" + webpchunk.ChunkID)
	_ = binary.Write(&result, binary.LittleEndian, uint32(len(chunk)))
	result.Write(chunk)
	if padding != 0 {
		result.WriteByte(0)
	}
	return result.Bytes()
}
