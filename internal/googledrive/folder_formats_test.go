package googledrive

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDriveFolderAdditionsCoverAllSupportedFormats(t *testing.T) {
	const rootID, folderID, nestedID = "my-drive", "karte-folder", "drive-imports"
	const markdown = "---\ntags: [folder-test]\n---\n# Test note\n"
	const csv = "source,value\nkarte,23\n"
	type format struct {
		name, mime, uploadType, content string
		kind                            Kind
	}
	form := []format{
		{"note.md", "text/markdown", "text/markdown; charset=utf-8", markdown, Markdown},
		{"note.markdown", "text/markdown", "text/markdown; charset=utf-8", markdown, Markdown},
		{"table", sheetMIME, "text/csv; charset=utf-8", csv, Sheet},
	}
	for _, photo := range []struct{ ext, mime string }{
		{"jpg", "image/jpeg"}, {"jpeg", "image/jpeg"}, {"png", "image/png"},
		{"gif", "image/gif"}, {"webp", "image/webp"},
	} {
		form = append(form, format{"photo." + photo.ext, photo.mime, photo.mime, folderTestPhoto(t, photo.mime), Photo})
	}
	type storedFile struct {
		file       File
		media      []byte
		uploadType string
	}
	files := map[string]storedFile{
		"root":   {file: File{ID: rootID, MIME: folderMIME}},
		folderID: {file: File{ID: folderID, Name: "Karte", MIME: folderMIME, Parents: []string{rootID}}},
	}
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/upload/files":
			file, uploadType, media := driveUpload(t, r)
			file.ID, file.Version = fmt.Sprintf("karte-%d", len(files)), "1"
			files[file.ID] = storedFile{file, media, uploadType}
			_ = json.NewEncoder(w).Encode(file)
		case r.Method == http.MethodGet && r.URL.Path == "/files":
			children := []File{}
			for _, stored := range files {
				for _, parent := range stored.file.Parents {
					if r.URL.Query().Get("q") == "trashed = false and '"+parent+"' in parents" {
						children = append(children, stored.file)
					}
				}
			}
			_ = json.NewEncoder(w).Encode(map[string][]File{"files": children})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/files/"):
			stored, ok := files[strings.TrimPrefix(r.URL.Path, "/files/")]
			if !ok {
				http.NotFound(w, r)
				return
			}
			if r.URL.Query().Get("alt") == "media" {
				_, _ = w.Write(stored.media)
			} else {
				_ = json.NewEncoder(w).Encode(stored.file)
			}
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	writer := formatTestService(server.URL)
	reader := NewRelationIndex()
	reader.service.driveURL = server.URL
	reader.service.token = token{AccessToken: "reader", expiresAt: time.Now().Add(time.Hour)}
	if before, err := reader.Refresh(t.Context(), folderID); err != nil || len(before.Files) != 0 {
		t.Fatalf("initial folder scan: %+v, %v", before, err)
	}

	mu.Lock()
	files[nestedID] = storedFile{file: File{ID: nestedID, Name: "Drive imports", MIME: folderMIME, Parents: []string{folderID}}}
	files["raw-csv"] = storedFile{file: File{ID: "raw-csv", Name: "raw.csv", MIME: "text/csv", Parents: []string{nestedID}}}
	mu.Unlock()
	wantByID := map[string]format{}
	for _, f := range form {
		t.Run(f.name, func(t *testing.T) {
			media := []byte(f.content)
			if f.kind == Photo {
				var err error
				media, err = base64.StdEncoding.DecodeString(f.content)
				if err != nil {
					t.Fatal(err)
				}
			}

			created, err := writer.Create(t.Context(), f.kind, f.name, f.content)
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			outgoing := files[created.File.ID]
			id := "drive-" + created.File.ID
			files[id] = storedFile{
				file:  File{ID: id, Version: "1", Name: f.name, MIME: f.mime, Parents: []string{nestedID}},
				media: media,
			}
			mu.Unlock()
			if outgoing.file.MIME != f.mime || outgoing.uploadType != f.uploadType || !bytes.Equal(outgoing.media, media) ||
				!slices.Equal(outgoing.file.Parents, []string{folderID}) {
				t.Fatalf("unexpected Drive upload: %q, %q, parents %v", outgoing.file.MIME, outgoing.uploadType, outgoing.file.Parents)
			}
			wantByID[created.File.ID], wantByID[id] = f, f
		})
	}

	after, err := reader.Refresh(t.Context(), folderID)
	if err != nil || after.Incomplete != 0 || len(after.Files) != 2*len(form) {
		t.Fatalf("updated folder scan should include both directions and exclude raw CSV: %+v, %v", after, err)
	}
	for _, file := range after.Files {
		want, found := wantByID[file.ID]
		if !found || file.Name != want.name || file.Kind != want.kind {
			t.Errorf("unexpected indexed file %q (%s, %s)", file.ID, file.Name, file.Kind)
		}
		delete(wantByID, file.ID)
	}
	if len(wantByID) != 0 {
		t.Errorf("missing uploads or nested Drive additions: %v", wantByID)
	}
}

func folderTestPhoto(t *testing.T, mimeType string) string {
	t.Helper()
	if mimeType == "image/webp" {
		// A 1×1 lossless WebP fixture. Go's WebP package only provides a decoder.
		return "UklGRhwAAABXRUJQVlA4TA8AAAAvAAAAAAcQ/Y/+ByKi/wEA"
	}
	var data bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	var err error
	switch mimeType {
	case "image/jpeg":
		err = jpeg.Encode(&data, img, nil)
	case "image/png":
		err = png.Encode(&data, img)
	case "image/gif":
		err = gif.Encode(&data, img, nil)
	default:
		t.Fatalf("no fixture for %s", mimeType)
	}
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(data.Bytes())
}
