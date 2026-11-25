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
	"strings"
)

const folderSyncProperty = "karteFolderSync"

type syncRemoteItem struct {
	File
	Dir string
}

type syncTree struct {
	files   []syncRemoteItem
	dirs    map[string]string
	skipped int
}

func (s *Service) syncAccess(ctx context.Context, account Account) (string, error) {
	if !account.SameUser(s.account) {
		return "", errors.New("同期対象と同じGoogleアカウントで接続してください")
	}
	return s.accessToken(ctx)
}

func (s *Service) validateSyncFolder(ctx context.Context, account Account, folderID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	access, err := s.syncAccess(ctx, account)
	if err != nil {
		return err
	}
	_, err = s.relationRoot(ctx, access, folderID)
	return err
}

func (s *Service) readSyncTree(ctx context.Context, account Account, folderID string) (syncTree, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	access, err := s.syncAccess(ctx, account)
	if err != nil {
		return syncTree{}, err
	}
	if _, err := s.relationRoot(ctx, access, folderID); err != nil {
		return syncTree{}, err
	}
	type directory struct{ id, name string }
	queue := []directory{{folderID, "."}}
	seen := map[string]bool{folderID: true}
	tree := syncTree{dirs: map[string]string{".": folderID}}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		children, err := s.listFields(ctx, access, "trashed = false and '"+current.id+"' in parents", relationFields+",appProperties")
		if err != nil {
			if current.id != folderID && isInaccessible(err) {
				tree.skipped++
				continue
			}
			return syncTree{}, err
		}
		for _, child := range children {
			if !fileIDPattern.MatchString(child.ID) || seen[child.ID] || child.Trash || child.DriveID != "" || !slices.Contains(child.Parents, current.id) {
				continue
			}
			seen[child.ID] = true
			if myDriveFolder(child) {
				if !safeSyncName(child.Name) {
					tree.skipped++
					continue
				}
				name := path.Join(current.name, child.Name)
				if _, exists := tree.dirs[name]; exists {
					// Both folders can be read. A local upload has no unique destination.
					tree.dirs[name] = ""
				} else {
					tree.dirs[name] = child.ID
				}
				queue = append(queue, directory{child.ID, name})
				continue
			}
			if (child.MIME == "text/csv" || child.MIME == "application/octet-stream") && strings.EqualFold(path.Ext(child.Name), ".csv") {
				child.Kind = Sheet
			} else if !classifyRelationFile(&child) {
				continue
			}
			if !safeSyncName(child.Name) {
				tree.skipped++
				continue
			}
			tree.files = append(tree.files, syncRemoteItem{child, current.name})
		}
	}
	return tree, nil
}

func (s *Service) downloadSyncFile(ctx context.Context, account Account, file syncRemoteItem) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	access, err := s.syncAccess(ctx, account)
	if err != nil {
		return nil, err
	}
	if file.Capabilities.CanDownload != nil && !*file.Capabilities.CanDownload {
		return nil, errors.New("このファイルはダウンロードが許可されていません")
	}
	if file.Kind == Sheet {
		url := s.driveURL + "/files/" + file.ID + "?alt=media"
		if file.MIME == sheetMIME {
			url = s.driveURL + "/files/" + file.ID + "/export?mimeType=text%2Fcsv"
		}
		data, err := s.request(ctx, access, http.MethodGet, url, "", nil, maxText)
		if err != nil {
			return nil, err
		}
		if _, err := parseCSV(string(data)); err != nil {
			return nil, err
		}
		return data, nil
	}
	limit := int64(maxText)
	if file.Kind == Photo {
		limit = maxPhoto
	}
	data, err := s.request(ctx, access, http.MethodGet, s.driveURL+"/files/"+file.ID+"?alt=media", "", nil, limit)
	if err != nil {
		return nil, err
	}
	if file.Kind == Photo && http.DetectContentType(data) != file.MIME {
		return nil, errors.New("Drive上のファイルを対応する画像形式で読み取れません")
	}
	if file.Kind == Markdown && validateMarkdown(string(data)) != nil {
		return nil, errors.New("Drive上のファイルをUTF-8 Markdownで読み取れません")
	}
	return data, nil
}

func (s *Service) createSyncDirectory(ctx context.Context, account Account, parent, name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	access, err := s.syncAccess(ctx, account)
	if err != nil {
		return "", err
	}
	query := url.Values{"fields": {"id,parents,mimeType"}}
	data, err := json.Marshal(map[string]any{"name": name, "parents": []string{parent}, "mimeType": folderMIME})
	if err != nil {
		return "", err
	}
	data, err = s.request(ctx, access, http.MethodPost, s.driveURL+"/files?"+query.Encode(), "application/json", bytes.NewReader(data), 32<<10)
	if err != nil {
		return "", err
	}
	var folder File
	if json.Unmarshal(data, &folder) != nil || !myDriveFolder(folder) || !slices.Contains(folder.Parents, parent) {
		return "", errors.New("同期用のDriveフォルダを確認できません")
	}
	return folder.ID, nil
}

func (s *Service) uploadSyncFile(ctx context.Context, account Account, parent, name, mimeType, mediaType, description, operation string, media []byte) (File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	access, err := s.syncAccess(ctx, account)
	if err != nil {
		return File{}, err
	}
	return s.createFile(ctx, access, parent, name, mimeType, mediaType, description, operation, media)
}
