package googledrive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"unicode/utf8"
)

const (
	maxMarkdown = 2 << 20
	fileFields  = "id,name,mimeType,trashed,capabilities(canEdit)"
)

type File struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	MIME         string `json:"mimeType"`
	Trash        bool   `json:"trashed"`
	Capabilities struct {
		CanEdit bool `json:"canEdit"`
	} `json:"capabilities"`
}

type Document struct {
	File     File   `json:"file"`
	Content  string `json:"content"`
	Checksum string `json:"checksum"`
}

func (s *Service) List(ctx context.Context) ([]File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	access, err := s.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	files := make([]File, 0)
	query := url.Values{
		"q": {"trashed = false"}, "fields": {"nextPageToken,files(" + fileFields + ")"},
		"pageSize": {"100"}, "supportsAllDrives": {"true"}, "includeItemsFromAllDrives": {"true"},
	}
	for {
		body, err := s.request(ctx, access, http.MethodGet, s.driveURL+"/files?"+query.Encode(), "", nil, 1<<20)
		if err != nil {
			return nil, err
		}
		var page struct {
			Files         []File
			NextPageToken string
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, errors.New("Google Driveのファイル一覧を読み取れません")
		}
		for _, file := range page.Files {
			if file.isMarkdown() {
				files = append(files, file)
			}
		}
		if page.NextPageToken == "" {
			return files, nil
		}
		query.Set("pageToken", page.NextPageToken)
	}
}

func (s *Service) Load(ctx context.Context, id string) (Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	access, err := s.accessToken(ctx)
	if err != nil {
		return Document{}, err
	}
	return s.load(ctx, access, id)
}

func (s *Service) load(ctx context.Context, access, id string) (Document, error) {
	if !fileIDPattern.MatchString(id) {
		return Document{}, errors.New("Google DriveのファイルIDが不正です")
	}
	query := url.Values{"fields": {fileFields}, "supportsAllDrives": {"true"}}
	endpoint := s.driveURL + "/files/" + id
	body, err := s.request(ctx, access, http.MethodGet, endpoint+"?"+query.Encode(), "", nil, 32<<10)
	if err != nil {
		return Document{}, err
	}
	var file File
	if err := json.Unmarshal(body, &file); err != nil || file.ID != id || !file.isMarkdown() {
		return Document{}, errors.New("Drive上のMarkdownファイルを選択してください")
	}
	content, err := s.request(ctx, access, http.MethodGet, endpoint+"?alt=media&supportsAllDrives=true", "", nil, maxMarkdown)
	if err != nil {
		return Document{}, err
	}
	if !utf8.Valid(content) {
		return Document{}, errors.New("UTF-8のMarkdownファイルを選択してください")
	}
	return Document{File: file, Content: string(content), Checksum: checksum(content)}, nil
}

func (s *Service) Create(ctx context.Context, name, content string) (Document, error) {
	name = strings.TrimSpace(name)
	if !markdownName(name) || strings.ContainsAny(name, "/\\\r\n\x00") {
		return Document{}, errors.New("ファイル名は .md または .markdown で終わる名前を指定してください")
	}
	if err := validateContent(content); err != nil {
		return Document{}, err
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	metadata, err := writer.CreatePart(textproto.MIMEHeader{"Content-Type": {"application/json; charset=utf-8"}})
	if err != nil {
		return Document{}, err
	}
	if err := json.NewEncoder(metadata).Encode(map[string]string{"name": name, "mimeType": "text/markdown"}); err != nil {
		return Document{}, err
	}
	media, err := writer.CreatePart(textproto.MIMEHeader{"Content-Type": {"text/markdown; charset=utf-8"}})
	if err != nil {
		return Document{}, err
	}
	if _, err := io.WriteString(media, content); err != nil {
		return Document{}, err
	}
	if err := writer.Close(); err != nil {
		return Document{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	access, err := s.accessToken(ctx)
	if err != nil {
		return Document{}, err
	}
	query := url.Values{"uploadType": {"multipart"}, "fields": {fileFields}}
	contentType := mime.FormatMediaType("multipart/related", map[string]string{"boundary": writer.Boundary()})
	result, err := s.request(ctx, access, http.MethodPost, s.uploadURL+"/files?"+query.Encode(), contentType, &body, 32<<10)
	if err != nil {
		return Document{}, err
	}
	var file File
	if err := json.Unmarshal(result, &file); err != nil || !file.isMarkdown() {
		return Document{}, errors.New("作成したGoogle Driveファイルを確認できません。一覧を更新してください")
	}
	return Document{File: file, Content: content, Checksum: checksum([]byte(content))}, nil
}

func (s *Service) Save(ctx context.Context, id, expectedChecksum, content string) (Document, error) {
	if err := validateContent(content); err != nil {
		return Document{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	access, err := s.accessToken(ctx)
	if err != nil {
		return Document{}, err
	}
	current, err := s.load(ctx, access, id)
	if err != nil {
		return Document{}, err
	}
	if !current.File.Capabilities.CanEdit {
		return Document{}, errors.New("Google Driveでこのファイルを編集する権限がありません")
	}
	if current.Checksum != expectedChecksum {
		return Document{}, errors.New("Drive上の内容が変わりました。編集内容を控えてから再読み込みしてください")
	}
	query := url.Values{"uploadType": {"media"}, "supportsAllDrives": {"true"}, "fields": {fileFields}}
	result, err := s.request(ctx, access, http.MethodPatch, s.uploadURL+"/files/"+id+"?"+query.Encode(), "text/markdown; charset=utf-8", strings.NewReader(content), 32<<10)
	if err != nil {
		return Document{}, err
	}
	var updated File
	if err := json.Unmarshal(result, &updated); err != nil || updated.ID != id || !updated.isMarkdown() {
		return Document{}, errors.New("保存したGoogle Driveファイルを確認できません。再読み込みしてください")
	}
	return Document{File: updated, Content: content, Checksum: checksum([]byte(content))}, nil
}

func (s *Service) request(ctx context.Context, access, method, endpoint, contentType string, upload io.Reader, maxResponse int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, upload)
	if err != nil {
		return nil, errors.New("Google Driveへのリクエストを作成できません")
	}
	req.Header.Set("Authorization", "Bearer "+access)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, errors.New("Google Driveに接続できません")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized && access == s.token.AccessToken {
		s.clearSession()
		return nil, errReauthorize
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Google Driveへの操作に失敗しました (HTTP %d)", resp.StatusCode)
	}
	return readBounded(resp.Body, maxResponse)
}

func (f File) isMarkdown() bool {
	return fileIDPattern.MatchString(f.ID) && !f.Trash &&
		!strings.HasPrefix(f.MIME, "application/vnd.google-apps.") && markdownName(f.Name)
}

func markdownName(name string) bool {
	name = strings.ToLower(name)
	return strings.HasSuffix(name, ".md") && len(name) > len(".md") ||
		strings.HasSuffix(name, ".markdown") && len(name) > len(".markdown")
}

func validateContent(content string) error {
	if len(content) > maxMarkdown || !utf8.ValidString(content) {
		return errors.New("2 MiB以下のUTF-8 Markdownを指定してください")
	}
	return nil
}

func checksum(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
