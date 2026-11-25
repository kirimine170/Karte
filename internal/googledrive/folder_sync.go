package googledrive

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const folderSyncStatePath = ".mdsys/google-drive-sync.json"

type FolderSyncReport struct {
	Downloaded  int    `json:"downloaded"`
	Uploaded    int    `json:"uploaded"`
	Issues      int    `json:"issues"`
	Message     string `json:"message"`
	WriterReady bool   `json:"writerReady"`
}

func (r *FolderSyncReport) issue(name string, err error) {
	r.Issues++
	if r.Message == "" {
		r.Message = fmt.Sprintf("%s: %v", name, err)
	}
}

type folderSyncState struct {
	Folder  string            `json:"folder"`
	Account string            `json:"account"`
	Local   map[string]bool   `json:"local"`
	Remote  map[string]string `json:"remote"`
	Pending map[string]string `json:"pending"`
}

// FolderSync transfers newly added files. The state tracks paths and Drive IDs
// so a download cannot become a new upload in a later pass.
type FolderSync struct {
	mu             sync.Mutex
	reader, writer *Service
	account        Account
	root           *os.Root
	state          folderSyncState
}

func NewFolderSync(ctx context.Context, dataDir, folderID string, index *RelationIndex, writer *Service) (*FolderSync, error) {
	if dataDir == "" || !fileIDPattern.MatchString(folderID) || folderID == "root" {
		return nil, errors.New("同期するKarteフォルダを選択してください")
	}
	account := index.Account()
	if account.Email == "" {
		return nil, errors.New("Drive関係検索を読み取り権限で接続してください")
	}
	if err := index.service.validateSyncFolder(ctx, account, folderID); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dataDir)
	if err != nil {
		return nil, err
	}
	s := &FolderSync{reader: index.service, writer: writer, account: account, root: root}
	if err := s.loadState(folderID); err != nil {
		root.Close()
		return nil, err
	}
	return s, nil
}

func (s *FolderSync) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.root.Close()
}

func (s *FolderSync) loadState(folderID string) error {
	accountID := s.account.PermissionID
	if accountID == "" {
		accountID = strings.ToLower(s.account.Email)
	}
	data, err := s.root.ReadFile(folderSyncStatePath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err == nil {
		if json.Unmarshal(data, &s.state) != nil || s.state.Local == nil || s.state.Remote == nil || s.state.Pending == nil {
			return errors.New("Drive同期の記録を読み取れません")
		}
		if s.state.Folder == folderID && s.state.Account == accountID {
			return nil
		}
	}
	local := map[string]bool{}
	if err := s.walkLocal(func(name string, _ Kind) { local[localSyncKey(name)] = true }); err != nil {
		return err
	}
	s.state = folderSyncState{Folder: folderID, Account: accountID, Local: local, Remote: map[string]string{}, Pending: map[string]string{}}
	return s.saveState()
}

func (s *FolderSync) saveState() error {
	if err := s.root.MkdirAll(".mdsys", 0700); err != nil {
		return err
	}
	data, err := json.Marshal(&s.state)
	if err != nil {
		return err
	}
	name, err := s.stageFile(".mdsys", data, 0600)
	if err != nil {
		return err
	}
	defer s.root.Remove(name)
	return s.root.Rename(name, folderSyncStatePath)
}

func (s *FolderSync) Run(ctx context.Context) (FolderSyncReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	report := FolderSyncReport{WriterReady: s.account.SameUser(s.writer.Account())}
	tree, err := s.reader.readSyncTree(ctx, s.account, s.state.Folder)
	if err != nil {
		return report, err
	}
	if tree.skipped > 0 {
		report.Issues = tree.skipped
		report.Message = "一部のDriveファイルまたはフォルダを取得できません"
	}
	pendingByOperation := make(map[string]string, len(s.state.Pending))
	for local, operation := range s.state.Pending {
		pendingByOperation[operation] = local
	}
	occupied := make(map[string]bool, len(s.state.Remote))
	for _, local := range s.state.Remote {
		occupied[localSyncKey(local)] = true
	}
	for _, item := range tree.files {
		if ctx.Err() != nil {
			return report, ctx.Err()
		}
		if _, found := s.state.Remote[item.ID]; found {
			continue
		}
		if local, found := pendingByOperation[item.AppProperties[folderSyncProperty]]; found {
			s.recordFile(local, item.ID)
			if err := s.saveState(); err != nil {
				return report, err
			}
			continue
		}
		data, err := s.reader.downloadSyncFile(ctx, s.account, item)
		if err != nil {
			report.issue(item.Name, err)
			continue
		}
		if err := ctx.Err(); err != nil {
			return report, err
		}
		local, created, err := s.installDownload(item, data, occupied)
		if err != nil {
			report.issue(item.Name, err)
			continue
		}
		s.recordFile(local, item.ID)
		if err := s.saveState(); err != nil {
			return report, err
		}
		occupied[localSyncKey(local)] = true
		if created {
			report.Downloaded++
		}
	}
	if !report.WriterReady {
		return report, nil
	}
	var candidates []struct {
		name string
		kind Kind
	}
	if err := s.walkLocal(func(name string, kind Kind) {
		if !s.state.Local[localSyncKey(name)] {
			candidates = append(candidates, struct {
				name string
				kind Kind
			}{name, kind})
		}
	}); err != nil {
		return report, err
	}
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return report, ctx.Err()
		}
		if err := s.upload(ctx, &tree, candidate.name, candidate.kind); err != nil {
			report.issue(candidate.name, err)
			if errors.Is(err, errSyncStateWrite) {
				return report, err
			}
			continue
		}
		if s.state.Local[localSyncKey(candidate.name)] {
			report.Uploaded++
		}
	}
	return report, nil
}

var errSyncStateWrite = errors.New("Drive同期の記録を書き込めません")

func (s *FolderSync) recordFile(local, remote string) {
	key := localSyncKey(local)
	s.state.Local[key] = true
	s.state.Remote[remote] = local
	delete(s.state.Pending, key)
}

func (s *FolderSync) upload(ctx context.Context, tree *syncTree, local string, kind Kind) error {
	key := localSyncKey(local)
	if s.state.Pending[key] != "" {
		return errors.New("前回のアップロード結果の確認を待っています")
	}
	info, err := s.root.Lstat(local)
	if errors.Is(err, fs.ErrNotExist) || err == nil && time.Since(info.ModTime()) < 2*time.Second {
		return nil
	}
	if err != nil {
		return err
	}
	limit := int64(maxText)
	if kind == Photo {
		limit = maxPhoto
	}
	data, err := s.readLocal(local, limit)
	if err != nil {
		return err
	}
	rel := strings.TrimPrefix(local, syncLocalDir(kind)+"/")
	name, content := path.Base(rel), string(data)
	if kind == Sheet {
		name = strings.TrimSuffix(name, path.Ext(name))
	}
	if kind == Photo {
		content = base64.StdEncoding.EncodeToString(data)
	}
	mimeType, mediaType, media, err := prepareDriveUpload(kind, name, content)
	if err != nil {
		return err
	}
	parent, err := s.ensureRemoteDir(ctx, tree, path.Dir(rel))
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	operation := rand.Text()
	s.state.Pending[key] = operation
	if err := s.saveState(); err != nil {
		delete(s.state.Pending, key)
		return fmt.Errorf("%w: %v", errSyncStateWrite, err)
	}
	description := initialDescription(kind, mimeType, media, nil)
	file, err := s.writer.uploadSyncFile(ctx, s.account, parent, name, mimeType, mediaType, description, operation, media)
	if err != nil {
		// An ambiguous response may have created the file. A later Drive scan
		// uses the operation property to recover without creating a duplicate.
		var status driveHTTPError
		if errors.As(err, &status) && status >= 400 && status < 500 && status != 408 && status != 409 && status != 429 {
			delete(s.state.Pending, key)
			if stateErr := s.saveState(); stateErr != nil {
				return fmt.Errorf("%w: %v", errSyncStateWrite, stateErr)
			}
		}
		return err
	}
	s.recordFile(local, file.ID)
	if err := s.saveState(); err != nil {
		return fmt.Errorf("%w: %v", errSyncStateWrite, err)
	}
	return nil
}

func (s *FolderSync) ensureRemoteDir(ctx context.Context, tree *syncTree, dir string) (string, error) {
	parent := tree.dirs["."]
	if dir == "." {
		return parent, nil
	}
	rel := "."
	for _, name := range strings.Split(dir, "/") {
		rel = path.Join(rel, name)
		id, exists := tree.dirs[rel]
		if exists && id == "" {
			return "", errors.New("Drive上に同じ名前のフォルダが複数あります")
		}
		if !exists {
			var err error
			id, err = s.writer.createSyncDirectory(ctx, s.account, parent, name)
			if err != nil {
				return "", err
			}
			tree.dirs[rel] = id
		}
		parent = id
	}
	return parent, nil
}

func (s *FolderSync) walkLocal(visit func(string, Kind)) error {
	for _, kind := range []Kind{Markdown, Photo, Sheet} {
		if err := s.ensureLocalDir(syncLocalDir(kind)); err != nil {
			return err
		}
		err := fs.WalkDir(s.root.FS(), syncLocalDir(kind), func(name string, entry fs.DirEntry, err error) error {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			if !safeSyncName(entry.Name()) || strings.HasPrefix(entry.Name(), ".") {
				if entry.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if entry.Type().IsRegular() && localSyncKind(kind, name) && !s.generatedWebP(kind, name) {
				visit(name, kind)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *FolderSync) generatedWebP(kind Kind, name string) bool {
	if kind != Photo || path.Ext(name) != ".webp" {
		return false
	}
	base := path.Base(name)
	if len(base) < 17 || base[15] != '_' {
		return false
	}
	if _, err := time.Parse("20060102-150405", base[:15]); err != nil {
		return false
	}
	stem := strings.TrimSuffix(name, ".webp")
	for _, suffix := range []string{".jpg", ".jpeg", ".png", ".gif", "_source.webp"} {
		if info, err := s.root.Lstat(stem + suffix); err == nil && info.Mode().IsRegular() {
			return true
		}
	}
	return false
}

func (s *FolderSync) readLocal(name string, limit int64) ([]byte, error) {
	info, err := s.root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("通常のファイルを選択してください")
	}
	f, err := s.root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := readBounded(f, limit)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || !os.SameFile(info, after) || info.Size() != after.Size() || info.ModTime() != after.ModTime() {
		return nil, errors.New("書き込み中のファイルです。次の同期で確認します")
	}
	return data, nil
}

func (s *FolderSync) installDownload(item syncRemoteItem, data []byte, occupied map[string]bool) (string, bool, error) {
	dir := path.Join(syncLocalDir(item.Kind), item.Dir)
	if err := s.ensureLocalDir(dir); err != nil {
		return "", false, err
	}
	name := syncDownloadName(item.File)
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 0; ; i++ {
		candidate := name
		if i > 0 {
			candidate = fmt.Sprintf("%s (Drive %s-%d)%s", stem, checksum([]byte(item.ID))[:8], i, ext)
		}
		local := path.Join(dir, candidate)
		if occupied[localSyncKey(local)] {
			continue
		}
		if old, err := s.readLocal(local, int64(len(data))); err == nil && bytes.Equal(old, data) {
			return local, false, nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			if _, statErr := s.root.Lstat(local); statErr != nil {
				return "", false, statErr
			}
			continue
		}
		stage, err := s.stageFile(dir, data, 0644)
		if err != nil {
			return "", false, err
		}
		err = s.root.Link(stage, local)
		_ = s.root.Remove(stage)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return local, err == nil, err
	}
}

func (s *FolderSync) ensureLocalDir(dir string) error {
	current := "."
	for _, name := range strings.Split(dir, "/") {
		current = path.Join(current, name)
		if err := s.root.Mkdir(current, 0755); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		info, err := s.root.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
			return errors.New("同期先のフォルダを利用できません")
		}
	}
	return nil
}

func (s *FolderSync) stageFile(dir string, data []byte, mode fs.FileMode) (string, error) {
	name := path.Join(dir, ".karte-drive-"+rand.Text()+".tmp")
	f, err := s.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return "", err
	}
	_, writeErr := f.Write(data)
	if err = errors.Join(writeErr, f.Sync(), f.Close()); err != nil {
		_ = s.root.Remove(name)
		return "", err
	}
	return name, nil
}

func safeSyncName(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.IsLocal(name) &&
		!strings.ContainsAny(name, "/\\:*?\"<>|\r\n\x00") && strings.TrimRight(name, " .") == name
}

func localSyncKey(name string) string {
	if os.PathSeparator == '\\' {
		return strings.ToLower(name)
	}
	return name
}

func syncLocalDir(kind Kind) string {
	switch kind {
	case Markdown:
		return "content"
	case Photo:
		return "data/image"
	default:
		return "data/csv"
	}
}

func localSyncKind(kind Kind, name string) bool {
	switch kind {
	case Markdown:
		return markdownName(name)
	case Photo:
		return photoMIME(name) != ""
	default:
		return strings.EqualFold(path.Ext(name), ".csv")
	}
}

func syncDownloadName(file File) string {
	name, ext := file.Name, path.Ext(file.Name)
	switch file.Kind {
	case Markdown:
		if strings.EqualFold(ext, ".markdown") {
			return strings.TrimSuffix(name, ext) + ".md"
		}
		if !strings.EqualFold(ext, ".md") {
			return name + ".md"
		}
	case Sheet:
		if !strings.EqualFold(ext, ".csv") {
			return name + ".csv"
		}
	case Photo:
		if photoMIME(name) != file.MIME {
			if file.MIME == "image/jpeg" {
				return name + ".jpg"
			}
			return name + "." + strings.TrimPrefix(file.MIME, "image/")
		}
	}
	return name
}
