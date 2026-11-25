package googledrive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"karte/internal/webpchunk"
)

const relationFields = "id,name,mimeType,trashed,parents,description,version,webViewLink,originalFilename,driveId,createdTime,size,capabilities(canDownload)"
const relationFolderFields = "id,name,mimeType,trashed,parents,driveId,createdTime"

type RelationFolder struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	CreatedTime string `json:"createdTime"`
}

type RelationFile struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Title      string   `json:"title"`
	Kind       Kind     `json:"kind"`
	Tags       []string `json:"tags"`
	LinkIDs    []string `json:"linkIds"`
	URL        string   `json:"url"`
	Legacy     string   `json:"legacy"`
	Unresolved int      `json:"unresolved"`
}

type RelationSnapshot struct {
	Folder     RelationFolder `json:"folder"`
	Files      []RelationFile `json:"files"`
	UpdatedAt  string         `json:"updatedAt"`
	Incomplete int            `json:"incomplete"`
}

type locatedFile struct {
	file File
	path string
}

type relationEntry struct {
	version    string
	note       relationMetadata
	incomplete bool
}

// The separate OAuth session can only read Drive data. service.mu also guards
// the derived metadata cache, which is never persisted.
type RelationIndex struct {
	service *Service
	folder  string
	cache   map[string]relationEntry
	owner   Account
}

func NewRelationIndex() *RelationIndex {
	return &RelationIndex{service: newService(readScope)}
}

func (index *RelationIndex) Authorize(ctx context.Context, browser func(string), required Account) (Account, error) {
	account, _, err := index.service.authorize(ctx, browser, false, required)
	return account, err
}

func (index *RelationIndex) Account() Account {
	return index.service.Account()
}

func (index *RelationIndex) CancelAuthorization() {
	index.service.CancelAuthorization()
}

func (index *RelationIndex) Disconnect() {
	s := index.service
	s.CancelAuthorization()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearSession()
	index.folder, index.cache, index.owner = "", nil, Account{}
}

func (index *RelationIndex) Folders(ctx context.Context) ([]RelationFolder, error) {
	s := index.service
	s.mu.Lock()
	defer s.mu.Unlock()
	access, err := s.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	q := "trashed = false and 'root' in parents and name = '" + folderName + "' and mimeType = '" + folderMIME + "'"
	files, err := s.listFields(ctx, access, q, relationFolderFields)
	if err != nil {
		return nil, err
	}
	folders := []RelationFolder{}
	for _, file := range files {
		if myDriveFolder(file) && file.Name == folderName {
			folders = append(folders, asRelationFolder(file))
		}
	}
	sort.Slice(folders, func(i, j int) bool {
		if folders[i].CreatedTime == folders[j].CreatedTime {
			return folders[i].ID < folders[j].ID
		}
		return folders[i].CreatedTime < folders[j].CreatedTime
	})
	return folders, nil
}

func (index *RelationIndex) Refresh(ctx context.Context, folderID string) (RelationSnapshot, error) {
	s := index.service
	s.mu.Lock()
	defer s.mu.Unlock()
	access, err := s.accessToken(ctx)
	if err != nil {
		index.folder, index.cache = "", nil
		return RelationSnapshot{}, err
	}
	if index.folder != folderID || !index.owner.SameUser(s.account) {
		index.folder, index.cache = folderID, nil
		index.owner = s.account
	}
	root, err := s.relationRoot(ctx, access, folderID)
	if err != nil {
		index.cache = nil
		return RelationSnapshot{}, err
	}
	files, incomplete, err := s.relationFiles(ctx, access, root.ID)
	if err != nil {
		index.cache = nil
		return RelationSnapshot{}, err
	}
	cache := make(map[string]relationEntry, len(files))
	visible := files[:0]
	for _, located := range files {
		file := located.file
		entry, found := index.cache[file.ID]
		if !found || file.Version == "" || entry.version != file.Version {
			entry, err = s.relationEntry(ctx, access, file)
			if err != nil {
				if isInaccessible(err) {
					incomplete++
					continue
				}
				index.cache = nil
				return RelationSnapshot{}, err
			}
		}
		if entry.incomplete {
			incomplete++
		}
		cache[file.ID] = entry
		visible = append(visible, located)
	}
	index.cache = cache
	return RelationSnapshot{
		Folder:     asRelationFolder(root),
		Files:      resolveRelations(visible, cache),
		UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
		Incomplete: incomplete,
	}, nil
}

func asRelationFolder(f File) RelationFolder {
	return RelationFolder{ID: f.ID, Name: f.Name, CreatedTime: f.CreatedTime}
}

func myDriveFolder(f File) bool {
	return fileIDPattern.MatchString(f.ID) && f.MIME == folderMIME && !f.Trash && f.DriveID == ""
}

// Validate a saved folder by ID, including after a rename or a move within My Drive.
func (s *Service) relationRoot(ctx context.Context, access, id string) (File, error) {
	if !fileIDPattern.MatchString(id) || id == "root" {
		return File{}, errors.New("検索するKarteフォルダを選択してください")
	}
	myDrive, err := s.relationFolder(ctx, access, "root")
	if err != nil {
		return File{}, err
	}
	current, err := s.relationFolder(ctx, access, id)
	if err != nil {
		return File{}, err
	}
	selected := current
	visited := map[string]bool{current.ID: true}
	for current.ID != myDrive.ID {
		if len(current.Parents) != 1 || visited[current.Parents[0]] {
			return File{}, errors.New("マイドライブ内のKarteフォルダを選択してください")
		}
		parent := current.Parents[0]
		if parent == myDrive.ID || parent == "root" {
			return selected, nil
		}
		current, err = s.relationFolder(ctx, access, parent)
		if err != nil {
			return File{}, err
		}
		visited[current.ID] = true
	}
	return File{}, errors.New("マイドライブ直下の対象フォルダを選択してください")
}

func (s *Service) relationFolder(ctx context.Context, access, id string) (File, error) {
	if !fileIDPattern.MatchString(id) {
		return File{}, errors.New("Google DriveのフォルダIDが不正です")
	}
	query := url.Values{"fields": {relationFolderFields}}
	data, err := s.request(ctx, access, http.MethodGet, s.driveURL+"/files/"+id+"?"+query.Encode(), "", nil, 32<<10)
	if err != nil {
		return File{}, err
	}
	var file File
	if err := json.Unmarshal(data, &file); err != nil || !myDriveFolder(file) || (id != "root" && file.ID != id) {
		return File{}, errors.New("検索対象のフォルダを利用できません")
	}
	return file, nil
}

func (s *Service) relationFiles(ctx context.Context, access, root string) ([]locatedFile, int, error) {
	type folder struct {
		id, path string
	}
	queue := []folder{{id: root}}
	seen := map[string]bool{root: true}
	files := []locatedFile{}
	incomplete := 0
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		children, err := s.listFields(ctx, access, "trashed = false and '"+current.id+"' in parents", relationFields)
		if err != nil {
			if current.id != root && isInaccessible(err) {
				incomplete++
				continue
			}
			return nil, 0, err
		}
		for _, child := range children {
			if child.Trash || child.DriveID != "" || seen[child.ID] || !slices.Contains(child.Parents, current.id) {
				continue
			}
			childPath := path.Join(current.path, child.Name)
			if myDriveFolder(child) {
				seen[child.ID] = true
				queue = append(queue, folder{id: child.ID, path: childPath})
			} else if classifyRelationFile(&child) {
				seen[child.ID] = true
				files = append(files, locatedFile{file: child, path: childPath})
			}
		}
	}
	return files, incomplete, nil
}

func classifyRelationFile(f *File) bool {
	if !fileIDPattern.MatchString(f.ID) || f.Trash {
		return false
	}
	switch f.MIME {
	case "text/markdown", "text/x-markdown":
		f.Kind = Markdown
		return true
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		f.Kind = Photo
		return true
	}
	if f.classify() {
		return true
	}
	if !strings.HasPrefix(f.MIME, "application/vnd.google-apps.") && f.OriginalName != "" {
		original := *f
		original.Name = f.OriginalName
		if original.classify() {
			f.Kind, f.MIME = original.Kind, original.MIME
			return true
		}
	}
	return false
}

func (s *Service) relationEntry(ctx context.Context, access string, file File) (relationEntry, error) {
	entry := relationEntry{version: file.Version}
	_, hasTags, _ := descriptionMetadata(file.Description)
	limit := int64(maxText)
	switch {
	case file.Kind == Markdown:
	case file.Kind == Photo && file.MIME == "image/webp" && !hasTags:
		limit = maxPhoto
	default:
		return entry, nil
	}
	if file.Capabilities.CanDownload != nil && !*file.Capabilities.CanDownload {
		entry.incomplete = true
		return entry, nil
	}
	if size, err := strconv.ParseInt(file.Size, 10, 64); err == nil && size > limit {
		entry.incomplete = true
		return entry, nil
	}
	content, err := s.request(ctx, access, http.MethodGet, s.driveURL+"/files/"+file.ID+"?alt=media", "", nil, limit)
	if errors.Is(err, errResponseLimit) {
		entry.incomplete = true
		return entry, nil
	}
	if err != nil {
		return relationEntry{}, err
	}
	if file.Kind == Photo {
		entry.note.tags, err = webpchunk.ReadTags(bytes.NewReader(content))
		entry.incomplete = err != nil
	} else if utf8.Valid(content) {
		entry.note = markdownRelationMetadata(string(content))
	} else {
		entry.incomplete = true
	}
	return entry, nil
}

func isInaccessible(err error) bool {
	var status driveHTTPError
	return errors.As(err, &status) && (status == http.StatusForbidden || status == http.StatusNotFound)
}

func resolveRelations(files []locatedFile, cache map[string]relationEntry) []RelationFile {
	byID := make(map[string]bool, len(files))
	byPath := make(map[string][]string, len(files))
	byTitle := make(map[string][]string, len(files))
	for _, located := range files {
		file, note := located.file, cache[located.file.ID].note
		byID[file.ID] = true
		byPath[located.path] = append(byPath[located.path], file.ID)
		if file.Kind == Markdown {
			keys := []string{strings.ToLower(strings.TrimSuffix(file.Name, path.Ext(file.Name))), strings.ToLower(note.title)}
			for _, key := range keys {
				if key != "" && !slices.Contains(byTitle[key], file.ID) {
					byTitle[key] = append(byTitle[key], file.ID)
				}
			}
		}
	}
	result := make([]RelationFile, 0, len(files))
	for _, located := range files {
		file, note := located.file, cache[located.file.ID].note
		tags, tagged, metadataLinks := descriptionMetadata(file.Description)
		legacyTags := !tagged && len(note.tags) > 0
		if legacyTags {
			tags = normalizeRelationTags(note.tags)
		}
		view := RelationFile{ID: file.ID, Name: file.Name, Title: note.title, Kind: file.Kind, Tags: tags, LinkIDs: []string{}, URL: file.WebViewLink}
		if view.Title == "" {
			view.Title = view.Name
		}
		if linkedDriveID(view.URL) != file.ID {
			view.URL = openDriveURL(file.ID)
		}
		seenLinks := map[string]bool{}
		add := func(id, key string) {
			if seenLinks[key] || id == file.ID {
				return
			}
			seenLinks[key] = true
			if byID[id] {
				view.LinkIDs = append(view.LinkIDs, id)
			} else {
				view.Unresolved++
			}
		}
		for _, id := range slices.Concat(metadataLinks, note.linkIDs) {
			add(id, id)
		}
		for _, relative := range note.relative {
			target := path.Clean(path.Join(path.Dir(located.path), relative))
			if strings.HasPrefix(relative, "/") {
				target = strings.TrimPrefix(path.Clean(relative), "/")
			}
			ids := byPath[target]
			if len(ids) == 1 {
				add(ids[0], ids[0])
			} else {
				add("", "path:"+target)
			}
		}
		for _, wiki := range note.wiki {
			title := strings.ToLower(strings.TrimSpace(wiki))
			if markdownName(title) {
				title = strings.TrimSuffix(title, path.Ext(title))
			}
			ids := byTitle[title]
			if len(ids) == 1 {
				add(ids[0], ids[0])
			} else {
				add("", "wiki:"+title)
			}
		}
		slices.Sort(view.LinkIDs)
		var toCopy []string
		for _, id := range view.LinkIDs {
			if !slices.Contains(metadataLinks, id) {
				toCopy = append(toCopy, id)
			}
		}
		view.Legacy = describeRelations(tags, legacyTags, toCopy)
		result = append(result, view)
	}
	sort.Slice(result, func(i, j int) bool {
		if strings.EqualFold(result[i].Title, result[j].Title) {
			return result[i].ID < result[j].ID
		}
		return strings.ToLower(result[i].Title) < strings.ToLower(result[j].Title)
	})
	return result
}
