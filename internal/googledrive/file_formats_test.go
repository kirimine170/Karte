package googledrive

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
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

func TestDrivePhotoRoundTrip(t *testing.T) {
	photo := func(c color.NRGBA) []byte {
		img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
		img.SetNRGBA(0, 0, c)
		var out bytes.Buffer
		if err := png.Encode(&out, img); err != nil {
			t.Fatal(err)
		}
		return out.Bytes()
	}
	original := photo(color.NRGBA{R: 255, A: 255})
	replacement := photo(color.NRGBA{B: 255, A: 255})
	const metadata = `{"id":"photo","name":"photo.PNG","mimeType":"image/png","parents":["karte-folder"],"capabilities":{"canEdit":true}}`
	var stored []byte
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method + " " + r.URL.Path {
		case "POST /upload/files":
			file, mediaType, media := driveUpload(t, r)
			if file.Name != "photo.PNG" || file.MIME != "image/png" || mediaType != "image/png" ||
				len(file.Parents) != 1 || file.Parents[0] != "karte-folder" ||
				file.Description != "Karte tags: 展示" || !bytes.Equal(media, original) {
				t.Errorf("unexpected photo upload: %+v (%s)", file, mediaType)
			}
			stored = media
			_, _ = io.WriteString(w, metadata)
		case "GET /files":
			_, _ = io.WriteString(w, `{"files":[`+metadata+`]}`)
		case "GET /files/photo":
			if r.URL.Query().Get("alt") == "media" {
				_, _ = w.Write(stored)
			} else {
				_, _ = io.WriteString(w, metadata)
			}
		case "PATCH /upload/files/photo":
			data, _ := io.ReadAll(r.Body)
			if r.Header.Get("Content-Type") != "image/png" || !bytes.Equal(data, replacement) {
				t.Error("photo replacement did not contain the original binary data")
			}
			stored = data
			_, _ = io.WriteString(w, metadata)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service := formatTestService(server.URL)
	ctx := t.Context()
	first := base64.StdEncoding.EncodeToString(original)
	next := base64.StdEncoding.EncodeToString(replacement)

	if _, err := service.Create(ctx, Photo, "wrong.jpg", first); err == nil {
		t.Fatal("uploaded PNG bytes under a JPEG name")
	}
	created, err := service.CreateWithTags(ctx, Photo, "photo.PNG", first, []string{"展示"})
	if err != nil || created.File.Kind != Photo || created.Content != first {
		t.Fatalf("create returned %+v, %v", created, err)
	}
	files, err := service.List(ctx)
	if err != nil || len(files) != 1 || files[0].Kind != Photo {
		t.Fatalf("list returned %+v, %v", files, err)
	}
	loaded, err := service.Load(ctx, created.File.ID)
	if err != nil || loaded.Content != first {
		t.Fatalf("load returned %+v, %v", loaded, err)
	}
	if _, err := service.Save(ctx, loaded.File.ID, loaded.Checksum, next); err != nil {
		t.Fatal(err)
	}
	loaded, err = service.Load(ctx, created.File.ID)
	if err != nil || loaded.Content != next || loaded.Checksum == created.Checksum {
		t.Fatalf("reload returned %+v, %v", loaded, err)
	}
}

func TestDriveCSVUsesNativeSpreadsheet(t *testing.T) {
	const fileMetadata = `{"id":"sheet","name":"在庫","mimeType":"application/vnd.google-apps.spreadsheet","parents":["karte-folder"],"capabilities":{"canEdit":true}}`
	const firstTab = `{"sheetId":0,"title":"在庫一覧","index":0,"sheetType":"GRID","gridProperties":{"rowCount":20,"columnCount":3}}`
	const imported = "商品,数量,計算\nノート,2,=B2+1\n"
	const edited = "商品,数量,計算\nノート,003,=B2+1\n"
	var quantity = `{"numberValue":2}`
	var updates int
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method + " " + r.URL.Path {
		case "POST /upload/files":
			file, mediaType, media := driveUpload(t, r)
			if file.Name != "在庫" || file.MIME != sheetMIME || !strings.HasPrefix(mediaType, "text/csv") ||
				len(file.Parents) != 1 || file.Parents[0] != "karte-folder" || string(media) != imported {
				t.Errorf("CSV was not imported as a native spreadsheet: %+v (%s)", file, mediaType)
			}
			_, _ = io.WriteString(w, fileMetadata)
		case "GET /files":
			_, _ = io.WriteString(w, `{"files":[`+fileMetadata+`,{"id":"other","name":"private","mimeType":"application/vnd.google-apps.spreadsheet","parents":["outside"]}]}`)
		case "GET /files/sheet":
			if r.URL.Query().Has("alt") {
				t.Error("a native spreadsheet must not be downloaded as a media file")
			}
			_, _ = io.WriteString(w, fileMetadata)
		case "GET /sheets/sheet":
			_, _ = io.WriteString(w, `{"sheets":[{"properties":{"sheetId":8,"title":"別タブ","index":1,"sheetType":"GRID"}},{"properties":`+firstTab+`}]}`)
		case "POST /sheets/sheet:getByDataFilter":
			data, _ := io.ReadAll(r.Body)
			if string(data) != `{"dataFilters":[{"gridRange":{"sheetId":0}}]}` {
				t.Errorf("requested cells from the wrong tabs: %s", data)
			}
			_, _ = io.WriteString(w, `{"sheets":[{"properties":`+firstTab+`,"data":[{"rowData":[
				{"values":[{"userEnteredValue":{"stringValue":"商品"}},{"userEnteredValue":{"stringValue":"数量"}},{"userEnteredValue":{"stringValue":"計算"}}]},
				{"values":[{"userEnteredValue":{"stringValue":"ノート"}},{"userEnteredValue":`+quantity+`},{"userEnteredValue":{"formulaValue":"=B2+1"}}]}
			]}]}]}`)
		case "POST /sheets/sheet:batchUpdate":
			var body struct{ Requests []sheetUpdate }
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Requests) != 1 {
				t.Errorf("expected exactly one cell update, got %+v, %v", body, err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			edit := body.Requests[0].Cells
			if edit == nil || edit.Mask != "userEnteredValue" || edit.Start["sheetId"] != 0 ||
				edit.Start["rowIndex"] != 1 || edit.Start["columnIndex"] != 1 ||
				len(edit.Rows) != 1 || len(edit.Rows[0].Cells) != 1 ||
				edit.Rows[0].Cells[0].Value == nil || edit.Rows[0].Cells[0].Value.Text == nil ||
				*edit.Rows[0].Cells[0].Value.Text != "003" {
				t.Errorf("changed cells outside the edit or lost leading zeroes: %+v", edit)
			}
			quantity = `{"stringValue":"003"}`
			updates++
			_, _ = io.WriteString(w, "{}")
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	service := formatTestService(server.URL)
	ctx := t.Context()
	created, err := service.Create(ctx, Sheet, "在庫", imported)
	if err != nil || created.File.Kind != Sheet {
		t.Fatalf("create returned %+v, %v", created, err)
	}
	files, err := service.List(ctx)
	if err != nil || len(files) != 1 || files[0].Kind != Sheet {
		t.Fatalf("list returned %+v, %v", files, err)
	}
	loaded, err := service.Load(ctx, created.File.ID)
	if err != nil || loaded.Content != imported || loaded.SheetTab != "在庫一覧" {
		t.Fatalf("load returned %+v, %v", loaded, err)
	}
	saved, err := service.Save(ctx, loaded.File.ID, loaded.Checksum, edited)
	if err != nil || saved.Content != edited || saved.Checksum == loaded.Checksum {
		t.Fatalf("save returned %+v, %v", saved, err)
	}
	if _, err := service.Save(ctx, loaded.File.ID, loaded.Checksum, imported); err != errContentChanged {
		t.Fatalf("expected conflict on stale spreadsheet, got %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if updates != 1 {
		t.Fatalf("expected one spreadsheet update, got %d", updates)
	}
}

func formatTestService(endpoint string) *Service {
	service := New()
	service.driveURL, service.uploadURL, service.sheetsURL = endpoint, endpoint+"/upload", endpoint+"/sheets"
	service.token = token{AccessToken: "access", expiresAt: time.Now().Add(time.Hour)}
	service.folderID = "karte-folder"
	return service
}

func driveUpload(t *testing.T, r *http.Request) (File, string, []byte) {
	t.Helper()
	contentType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "multipart/related" || r.URL.Query().Get("uploadType") != "multipart" {
		t.Errorf("expected a multipart upload, got %s, %v", contentType, err)
		return File{}, "", nil
	}
	reader := multipart.NewReader(r.Body, params["boundary"])
	part, err := reader.NextPart()
	if err != nil {
		t.Error(err)
		return File{}, "", nil
	}
	var file File
	if err := json.NewDecoder(part).Decode(&file); err != nil {
		t.Error(err)
		return File{}, "", nil
	}
	part, err = reader.NextPart()
	if err != nil {
		t.Error(err)
		return File{}, "", nil
	}
	data, err := io.ReadAll(part)
	if err != nil {
		t.Error(err)
	}
	return file, part.Header.Get("Content-Type"), data
}
