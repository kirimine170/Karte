package googlesync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"regexp"
	"strings"

	"karte/internal/canonical"
	"karte/internal/decisionlog"
)

const (
	driveFilesURL    = "https://www.googleapis.com/drive/v3/files"
	driveUploadURL   = "https://www.googleapis.com/upload/drive/v3/files"
	maxRevisionBytes = 16 << 20
)

var revisionIDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type revision struct {
	Parent   string               `json:"parent"`
	Snapshot decisionlog.Snapshot `json:"snapshot"`
}

type driveRevision struct {
	ID     string
	Parent string
	FileID string
}

type revisionStore interface {
	list(context.Context) (map[string]driveRevision, error)
	read(context.Context, driveRevision) (revision, error)
	create(context.Context, string, revision, []byte) error
}

type driveStore struct {
	client    *http.Client
	token     string
	prefix    string
	filesURL  string
	uploadURL string
}

func (d driveStore) list(ctx context.Context) (map[string]driveRevision, error) {
	params := url.Values{
		"spaces": {"appDataFolder"}, "fields": {"nextPageToken,files(id,name,appProperties)"},
		"pageSize": {"1000"}, "q": {"trashed = false and name contains '" + d.prefix + "'"},
	}
	result := make(map[string]driveRevision)
	seenPages := make(map[string]bool)
	for {
		body, err := d.request(ctx, http.MethodGet, d.filesURL+"?"+params.Encode(), "", nil)
		if err != nil {
			return nil, err
		}
		var page struct {
			Next  string `json:"nextPageToken"`
			Files []struct {
				ID         string            `json:"id"`
				Name       string            `json:"name"`
				Properties map[string]string `json:"appProperties"`
			} `json:"files"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("Google Driveの同期一覧が不正です")
		}
		for _, file := range page.Files {
			id, ok := strings.CutPrefix(file.Name, d.prefix)
			parent := file.Properties["parent"]
			if !ok || file.ID == "" || !revisionIDPattern.MatchString(id) ||
				(parent != "" && !revisionIDPattern.MatchString(parent)) {
				return nil, fmt.Errorf("Google Driveの同期ファイル名が不正です")
			}
			if previous, exists := result[id]; exists {
				if previous.Parent != parent {
					return nil, fmt.Errorf("Google Driveの同期履歴が一致しません")
				}
				if previous.FileID < file.ID {
					continue
				}
			}
			result[id] = driveRevision{ID: id, Parent: parent, FileID: file.ID}
		}
		if page.Next == "" {
			return result, nil
		}
		if seenPages[page.Next] {
			return nil, fmt.Errorf("Google Driveの同期一覧を取得できません")
		}
		seenPages[page.Next] = true
		params.Set("pageToken", page.Next)
	}
}

func (d driveStore) read(ctx context.Context, file driveRevision) (revision, error) {
	raw, err := d.request(ctx, http.MethodGet, d.filesURL+"/"+url.PathEscape(file.FileID)+"?alt=media", "", nil)
	if err != nil {
		return revision{}, err
	}
	if canonical.Hash(raw) != file.ID {
		return revision{}, fmt.Errorf("Google Driveの同期ファイルのハッシュが一致しません")
	}
	var value revision
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || decoder.Decode(new(any)) != io.EOF || value.Parent != file.Parent {
		return revision{}, fmt.Errorf("Google Driveの同期ファイルが不正です")
	}
	if err := value.Snapshot.Validate(); err != nil {
		return revision{}, err
	}
	return value, nil
}

func (d driveStore) create(ctx context.Context, id string, value revision, raw []byte) error {
	var requestBody bytes.Buffer
	multi := multipart.NewWriter(&requestBody)
	header := textproto.MIMEHeader{"Content-Type": {"application/json; charset=UTF-8"}}
	metadata, err := multi.CreatePart(header)
	if err != nil {
		return err
	}
	properties := map[string]string{}
	if value.Parent != "" {
		properties["parent"] = value.Parent
	}
	if err := json.NewEncoder(metadata).Encode(map[string]any{
		"name": d.prefix + id, "parents": []string{"appDataFolder"},
		"mimeType": "application/json", "appProperties": properties,
	}); err != nil {
		return err
	}
	content, err := multi.CreatePart(header)
	if err != nil {
		return err
	}
	if _, err := content.Write(raw); err != nil {
		return err
	}
	if err := multi.Close(); err != nil {
		return err
	}
	_, err = d.request(ctx, http.MethodPost, d.uploadURL+"?uploadType=multipart&fields=id",
		"multipart/related; boundary="+multi.Boundary(), &requestBody)
	return err
}

func (d driveStore) request(ctx context.Context, method, endpoint, contentType string, body io.Reader) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("Google Driveの同期リクエストを作成できません")
	}
	request.Header.Set("Authorization", "Bearer "+d.token)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := d.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("Google Driveに接続できません")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("Google Driveの同期に失敗しました (HTTP %d)", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxRevisionBytes+1))
	if err != nil || len(raw) > maxRevisionBytes {
		return nil, fmt.Errorf("Google Driveの同期ファイルを読み込めません")
	}
	return raw, nil
}
