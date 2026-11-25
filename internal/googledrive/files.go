package googledrive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
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
	"path"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	maxText    = 2 << 20
	maxPhoto   = 5 << 20
	fileFields = "id,name,mimeType,trashed,parents,capabilities(canEdit,canAddChildren)"
	folderName = "Karte"
	folderMIME = "application/vnd.google-apps.folder"
	sheetMIME  = "application/vnd.google-apps.spreadsheet"
)

type Kind string

const (
	Markdown Kind = "markdown"
	Sheet    Kind = "sheet"
	Photo    Kind = "photo"
)

type File struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	MIME          string            `json:"mimeType"`
	Kind          Kind              `json:"kind"`
	Trash         bool              `json:"trashed"`
	Parents       []string          `json:"parents"`
	Description   string            `json:"description,omitempty"`
	Version       string            `json:"version,omitempty"`
	WebViewLink   string            `json:"webViewLink,omitempty"`
	OriginalName  string            `json:"originalFilename,omitempty"`
	AppProperties map[string]string `json:"appProperties,omitempty"`
	DriveID       string            `json:"driveId,omitempty"`
	CreatedTime   string            `json:"createdTime,omitempty"`
	Size          string            `json:"size,omitempty"`
	Capabilities  struct {
		CanEdit        bool  `json:"canEdit"`
		CanAddChildren bool  `json:"canAddChildren"`
		CanDownload    *bool `json:"canDownload,omitempty"`
	} `json:"capabilities"`
}

type Document struct {
	File File `json:"file"`
	// Photos carry base64; Markdown and spreadsheets carry UTF-8 text.
	Content  string `json:"content"`
	Checksum string `json:"checksum"`
	SheetTab string `json:"sheetTab,omitempty"`
}

func (s *Service) List(ctx context.Context) ([]File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	access, err := s.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	folder, err := s.folder(ctx, access)
	if err != nil {
		return nil, err
	}
	files, err := s.listFiles(ctx, access, "trashed = false and '"+folder+"' in parents")
	if err != nil {
		return nil, err
	}
	supported := files[:0]
	for _, file := range files {
		if file.classify() && slices.Contains(file.Parents, folder) {
			supported = append(supported, file)
		}
	}
	return supported, nil
}

func (s *Service) listFiles(ctx context.Context, access, filter string) ([]File, error) {
	return s.listFields(ctx, access, filter, fileFields)
}

func (s *Service) listFields(ctx context.Context, access, filter, fields string) ([]File, error) {
	files := make([]File, 0)
	query := url.Values{
		"q": {filter}, "fields": {"nextPageToken,incompleteSearch,files(" + fields + ")"},
		"pageSize": {"100"},
	}
	seen := map[string]bool{}
	for {
		body, err := s.request(ctx, access, http.MethodGet, s.driveURL+"/files?"+query.Encode(), "", nil, 1<<20)
		if err != nil {
			return nil, err
		}
		var page struct {
			Files            []File
			NextPageToken    string
			IncompleteSearch bool
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, errors.New("Google Driveのファイル一覧を読み取れません")
		}
		if page.IncompleteSearch {
			return nil, errors.New("Google Driveの一覧が不完全です。再読み込みしてください")
		}
		files = append(files, page.Files...)
		if page.NextPageToken == "" {
			return files, nil
		}
		if seen[page.NextPageToken] {
			return nil, errors.New("Google Driveの一覧を最後まで読み取れません")
		}
		seen[page.NextPageToken] = true
		query.Set("pageToken", page.NextPageToken)
	}
}

func (s *Service) Folder(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	access, err := s.accessToken(ctx)
	if err != nil {
		return "", err
	}
	return s.folder(ctx, access)
}

// The caller holds s.mu, so the first list and create cannot race within this session.
func (s *Service) folder(ctx context.Context, access string) (string, error) {
	if s.folderID != "" {
		return s.folderID, nil
	}
	filter := "trashed = false and 'root' in parents and name = '" + folderName + "' and mimeType = '" + folderMIME + "'"
	folders, err := s.listFiles(ctx, access, filter)
	if err != nil {
		return "", err
	}
	if len(folders) > 1 {
		return "", errors.New("マイドライブにKarteフォルダが複数あります。Driveで確認してください")
	}
	var folder File
	if len(folders) == 1 {
		folder = folders[0]
	} else {
		metadata, err := json.Marshal(map[string]any{"name": folderName, "mimeType": folderMIME, "parents": []string{"root"}})
		if err != nil {
			return "", err
		}
		query := url.Values{"fields": {fileFields}}
		body, err := s.request(ctx, access, http.MethodPost, s.driveURL+"/files?"+query.Encode(), "application/json", bytes.NewReader(metadata), 32<<10)
		if err != nil {
			return "", err
		}
		if err := json.Unmarshal(body, &folder); err != nil {
			return "", errors.New("作成したKarteフォルダを確認できません")
		}
	}
	if !fileIDPattern.MatchString(folder.ID) || folder.Name != folderName || folder.MIME != folderMIME || folder.Trash || !folder.Capabilities.CanAddChildren {
		return "", errors.New("マイドライブのKarteフォルダを利用できません")
	}
	s.folderID = folder.ID
	return s.folderID, nil
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
	file, err := s.folderFile(ctx, access, id)
	if err != nil {
		return Document{}, err
	}
	if file.Kind == Sheet {
		sheet, err := s.readSheet(ctx, access, id)
		if err != nil {
			return Document{}, err
		}
		return sheet.document(file)
	}
	return s.loadMedia(ctx, access, file)
}

func (s *Service) folderFile(ctx context.Context, access, id string) (File, error) {
	if !fileIDPattern.MatchString(id) {
		return File{}, errors.New("Google DriveのファイルIDが不正です")
	}
	query := url.Values{"fields": {fileFields}, "supportsAllDrives": {"true"}}
	endpoint := s.driveURL + "/files/" + id
	body, err := s.request(ctx, access, http.MethodGet, endpoint+"?"+query.Encode(), "", nil, 32<<10)
	if err != nil {
		return File{}, err
	}
	var file File
	if err := json.Unmarshal(body, &file); err != nil || file.ID != id || !file.classify() {
		return File{}, errors.New("Drive上のMarkdown、スプレッドシート、写真を選択してください")
	}
	folder, err := s.folder(ctx, access)
	if err != nil {
		return File{}, err
	}
	if !slices.Contains(file.Parents, folder) {
		return File{}, errors.New("マイドライブのKarteフォルダにあるファイルを選択してください")
	}
	return file, nil
}

func (s *Service) loadMedia(ctx context.Context, access string, file File) (Document, error) {
	var limit int64 = maxText
	if file.Kind == Photo {
		limit = maxPhoto
	}
	endpoint := s.driveURL + "/files/" + file.ID + "?alt=media&supportsAllDrives=true"
	content, err := s.request(ctx, access, http.MethodGet, endpoint, "", nil, limit)
	if err != nil {
		return Document{}, err
	}
	if file.Kind == Photo {
		if http.DetectContentType(content) != file.MIME {
			return Document{}, errors.New("拡張子と一致するJPEG、PNG、GIF、WebPを選択してください")
		}
		return Document{File: file, Content: base64.StdEncoding.EncodeToString(content), Checksum: checksum(content)}, nil
	}
	if !utf8.Valid(content) {
		return Document{}, errors.New("UTF-8のMarkdownファイルを選択してください")
	}
	return Document{File: file, Content: string(content), Checksum: checksum(content)}, nil
}

func (s *Service) Create(ctx context.Context, kind Kind, name, content string) (Document, error) {
	return s.CreateWithTags(ctx, kind, name, content, nil)
}

func (s *Service) CreateWithTags(ctx context.Context, kind Kind, name, content string, tags []string) (Document, error) {
	name = strings.TrimSpace(name)
	mimeType, mediaType, media, err := prepareDriveUpload(kind, name, content)
	if err != nil {
		return Document{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	access, err := s.accessToken(ctx)
	if err != nil {
		return Document{}, err
	}
	folder, err := s.folder(ctx, access)
	if err != nil {
		return Document{}, err
	}
	description := initialDescription(kind, mimeType, media, tags)
	file, err := s.createFile(ctx, access, folder, name, mimeType, mediaType, description, "", media)
	if err != nil {
		return Document{}, err
	}
	// Sheets can normalize imported values. Its editor is populated by Load.
	return Document{File: file, Content: content, Checksum: checksum(media)}, nil
}

func prepareDriveUpload(kind Kind, name, content string) (mimeType, mediaType string, media []byte, err error) {
	if name == "" || strings.ContainsAny(name, "/\\\r\n\x00") {
		return "", "", nil, errors.New("ファイル名を指定してください。パスは使用できません")
	}
	mimeType = "text/markdown"
	mediaType = mimeType + "; charset=utf-8"
	switch kind {
	case Markdown:
		if !markdownName(name) {
			return "", "", nil, errors.New("ファイル名は .md または .markdown で終わる名前を指定してください")
		}
		if err := validateMarkdown(content); err != nil {
			return "", "", nil, err
		}
		media = []byte(content)
	case Sheet:
		if _, err := parseCSV(content); err != nil {
			return "", "", nil, err
		}
		mimeType, mediaType = sheetMIME, "text/csv; charset=utf-8"
		media = []byte(content)
		if len(media) == 0 {
			// Give the CSV converter an empty cell for a blank spreadsheet.
			media = []byte("\"\"\n")
		}
	case Photo:
		mimeType = photoMIME(name)
		mediaType = mimeType
		media, err = decodePhoto(mimeType, content)
		if err != nil {
			return "", "", nil, err
		}
	default:
		return "", "", nil, errors.New("対応していないファイル形式です")
	}
	return mimeType, mediaType, media, nil
}

func (s *Service) createFile(ctx context.Context, access, folder, name, mimeType, mediaType, description, operation string, content []byte) (File, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	metadata, err := writer.CreatePart(textproto.MIMEHeader{"Content-Type": {"application/json; charset=utf-8"}})
	if err != nil {
		return File{}, err
	}
	properties := map[string]any{"name": name, "mimeType": mimeType, "parents": []string{folder}}
	if description != "" {
		properties["description"] = description
	}
	if operation != "" {
		properties["appProperties"] = map[string]string{folderSyncProperty: operation}
	}
	if err := json.NewEncoder(metadata).Encode(properties); err != nil {
		return File{}, err
	}
	media, err := writer.CreatePart(textproto.MIMEHeader{"Content-Type": {mediaType}})
	if err != nil {
		return File{}, err
	}
	if _, err := media.Write(content); err != nil {
		return File{}, err
	}
	if err := writer.Close(); err != nil {
		return File{}, err
	}
	query := url.Values{"uploadType": {"multipart"}, "fields": {fileFields}}
	contentType := mime.FormatMediaType("multipart/related", map[string]string{"boundary": writer.Boundary()})
	result, err := s.request(ctx, access, http.MethodPost, s.uploadURL+"/files?"+query.Encode(), contentType, &body, 32<<10)
	if err != nil {
		return File{}, err
	}
	var file File
	if err := json.Unmarshal(result, &file); err != nil || !file.classify() || file.MIME != mimeType || !slices.Contains(file.Parents, folder) {
		return File{}, errors.New("作成したGoogle Driveファイルを確認できません。一覧を更新してください")
	}
	return file, nil
}

func (s *Service) Save(ctx context.Context, id, expectedChecksum, content string) (Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	access, err := s.accessToken(ctx)
	if err != nil {
		return Document{}, err
	}
	file, err := s.folderFile(ctx, access, id)
	if err != nil {
		return Document{}, err
	}
	if !file.Capabilities.CanEdit {
		return Document{}, errors.New("Google Driveでこのファイルを編集する権限がありません")
	}
	if file.Kind == Sheet {
		return s.saveSheet(ctx, access, file, expectedChecksum, content)
	}
	current, err := s.loadMedia(ctx, access, file)
	if err != nil {
		return Document{}, err
	}
	if current.Checksum != expectedChecksum {
		return Document{}, errContentChanged
	}
	mediaType := "text/markdown; charset=utf-8"
	var media []byte
	if file.Kind == Photo {
		mediaType = file.MIME
		media, err = decodePhoto(mediaType, content)
	} else {
		media = []byte(content)
		err = validateMarkdown(content)
	}
	if err != nil {
		return Document{}, err
	}
	query := url.Values{"uploadType": {"media"}, "supportsAllDrives": {"true"}, "fields": {fileFields}}
	result, err := s.request(ctx, access, http.MethodPatch, s.uploadURL+"/files/"+id+"?"+query.Encode(), mediaType, bytes.NewReader(media), 32<<10)
	if err != nil {
		return Document{}, err
	}
	var updated File
	if err := json.Unmarshal(result, &updated); err != nil || updated.ID != id || !updated.classify() ||
		updated.Kind != file.Kind || (file.Kind == Photo && updated.MIME != file.MIME) || !slices.Contains(updated.Parents, s.folderID) {
		return Document{}, errors.New("保存したGoogle Driveファイルを確認できません。再読み込みしてください")
	}
	return Document{File: updated, Content: content, Checksum: checksum(media)}, nil
}

func (s *Service) request(ctx context.Context, access, method, endpoint, contentType string, upload io.Reader, maxResponse int64) ([]byte, error) {
	if s.scope == readScope && method != http.MethodGet {
		return nil, errors.New("Drive関係検索の接続は読み取り専用です")
	}
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
		return nil, driveHTTPError(resp.StatusCode)
	}
	return readBounded(resp.Body, maxResponse)
}

type driveHTTPError int

func (err driveHTTPError) Error() string {
	return fmt.Sprintf("Google Driveへの操作に失敗しました (HTTP %d)", int(err))
}

var errContentChanged = errors.New("Drive上の内容が変わりました。編集内容を控えてから再読み込みしてください")

func (f *File) classify() bool {
	switch {
	case !fileIDPattern.MatchString(f.ID) || f.Trash:
		return false
	case f.MIME == sheetMIME:
		f.Kind = Sheet
	case strings.HasPrefix(f.MIME, "application/vnd.google-apps."):
		return false
	case markdownName(f.Name):
		f.Kind = Markdown
	default:
		mimeType := photoMIME(f.Name)
		if mimeType == "" {
			return false
		}
		f.Kind, f.MIME = Photo, mimeType
	}
	return true
}

func markdownName(name string) bool {
	ext := strings.ToLower(path.Ext(name))
	return len(name) > len(ext) && (ext == ".md" || ext == ".markdown")
}

func photoMIME(name string) string {
	ext := strings.ToLower(path.Ext(name))
	if len(name) <= len(ext) {
		return ""
	}
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return ""
	}
}

func decodePhoto(mimeType, value string) ([]byte, error) {
	if mimeType == "" || len(value) > base64.StdEncoding.EncodedLen(maxPhoto) {
		return nil, errors.New("5 MiB以下のJPEG、PNG、GIF、WebPを指定してください")
	}
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(data) > maxPhoto || http.DetectContentType(data) != mimeType {
		return nil, errors.New("5 MiB以下で拡張子と一致するJPEG、PNG、GIF、WebPを指定してください")
	}
	return data, nil
}

func validateMarkdown(content string) error {
	if len(content) > maxText || !utf8.ValidString(content) {
		return errors.New("2 MiB以下のUTF-8 Markdownを指定してください")
	}
	return nil
}

func checksum(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
