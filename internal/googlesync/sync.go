package googlesync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"karte/internal/canonical"
	"karte/internal/decisionlog"
)

const (
	syncDir     = ".mdsys/decision-sync"
	linkPath    = syncDir + "/link.json"
	pendingPath = syncDir + "/pending.json"
)

var ErrSyncDisabled = errors.New("決定ログの同期が無効です")
var syncNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

type SyncStatus struct {
	Enabled   bool   `json:"enabled"`
	Name      string `json:"name,omitempty"`
	LastSync  string `json:"last_sync,omitempty"`
	Conflicts int    `json:"conflicts"`
}

type workspaceLink struct {
	SyncStatus
	Account     string `json:"account"`
	Base        string `json:"base"`
	BaseContent string `json:"base_content"`
}

type pendingApplication struct {
	ID     string               `json:"id"`
	Base   string               `json:"base"`
	Before decisionlog.Snapshot `json:"before"`
	After  decisionlog.Snapshot `json:"after"`
}

func WorkspaceSyncStatus(root string) (SyncStatus, error) {
	var link workspaceLink
	err := canonical.WithWriter(root, func(w *canonical.Writer) error {
		var err error
		link, err = readLink(w)
		return err
	})
	return link.SyncStatus, err
}

func (s *Session) SetWorkspaceSync(root, name string, enabled bool) (SyncStatus, error) {
	name = strings.TrimSpace(name)
	account := ""
	if enabled {
		if !syncNamePattern.MatchString(name) {
			return SyncStatus{}, fmt.Errorf("同期IDには半角英数字、ハイフン、アンダースコアを使ってください")
		}
		s.mu.Lock()
		value, err := s.read()
		s.mu.Unlock()
		if err != nil {
			return SyncStatus{}, err
		}
		account = accountHash(value)
	}
	var status SyncStatus
	err := canonical.WithWriter(root, func(w *canonical.Writer) error {
		link, err := readLink(w)
		if err != nil {
			return err
		}
		if enabled {
			if link.Name != "" && (link.Name != name || link.Account != account) {
				return fmt.Errorf("このワークスペースには別の同期IDまたはアカウントが設定されています")
			}
			link.Name, link.Account = name, account
		}
		link.Enabled = enabled
		status = link.SyncStatus
		return saveLink(w, link)
	})
	return status, err
}

func (s *Session) SyncWorkspace(ctx context.Context, root string) (SyncStatus, error) {
	var link workspaceLink
	if err := canonical.WithWriter(root, func(w *canonical.Writer) error {
		var err error
		link, err = readLink(w)
		return err
	}); err != nil {
		return SyncStatus{}, err
	}
	if !link.Enabled {
		return SyncStatus{}, ErrSyncDisabled
	}
	value, err := s.authorization(ctx)
	if err != nil {
		return link.SyncStatus, err
	}
	if accountHash(value) != link.Account {
		return link.SyncStatus, fmt.Errorf("このワークスペースを設定したGoogleアカウントでログインしてください")
	}
	store := driveStore{
		client: s.client, token: value.AccessToken, prefix: "karte-decisions-v1-" + canonical.Hash([]byte(link.Name)) + "-",
		filesURL: driveFilesURL, uploadURL: driveUploadURL,
	}
	return syncWithStore(ctx, root, link, store)
}

func accountHash(value credential) string {
	return canonical.Hash([]byte(value.ClientID + "\n" + value.Subject))
}

func readLink(w *canonical.Writer) (workspaceLink, error) {
	raw, err := w.ReadLimit(linkPath, 64<<10)
	if errors.Is(err, os.ErrNotExist) {
		return workspaceLink{}, nil
	}
	if err != nil {
		return workspaceLink{}, err
	}
	var link workspaceLink
	if json.Unmarshal(raw, &link) != nil || (link.Base != "" && (!revisionIDPattern.MatchString(link.Base) ||
		!revisionIDPattern.MatchString(link.BaseContent))) ||
		(link.Enabled && (!syncNamePattern.MatchString(link.Name) || !revisionIDPattern.MatchString(link.Account))) {
		return workspaceLink{}, fmt.Errorf("決定ログの同期設定が不正です")
	}
	return link, nil
}

func saveLink(w *canonical.Writer, link workspaceLink) error {
	return writeJSON(w, linkPath, link)
}

func writeJSON(w *canonical.Writer, path string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return w.Write(path, raw, 0600)
}

func syncWithStore(ctx context.Context, root string, link workspaceLink, store revisionStore) (SyncStatus, error) {
	var local decisionlog.Snapshot
	err := canonical.WithWriter(root, func(w *canonical.Writer) error {
		current, err := readLink(w)
		if err != nil {
			return err
		}
		if current != link {
			return decisionlog.ErrSnapshotConflict
		}
		if err := resumeApplication(w, &link); err != nil {
			return err
		}
		local, err = decisionlog.ReadSnapshot(w)
		return err
	})
	if err != nil {
		return link.SyncStatus, err
	}
	revisions, err := store.list(ctx)
	if err != nil {
		return link.SyncStatus, err
	}
	if link.Base != "" && revisions[link.Base].ID == "" {
		return link.SyncStatus, fmt.Errorf("前回の同期履歴がGoogle Driveに見つかりません")
	}
	if _, err := revisionTips(revisions); err != nil {
		return link.SyncStatus, err
	}

	previous := link
	content := snapshotHash(local)
	changed := content != link.BaseContent && (link.Base != "" || local.Settings != nil || len(local.Records) != 0)
	if changed {
		if err := local.Validate(); err != nil {
			return link.SyncStatus, err
		}
		next := revision{Parent: link.Base, Snapshot: local}
		raw, err := json.Marshal(next)
		if err != nil || len(raw) > maxRevisionBytes {
			return link.SyncStatus, fmt.Errorf("決定ログが同期ファイルの上限を超えています")
		}
		id := canonical.Hash(raw)
		if _, exists := revisions[id]; !exists {
			if err := store.create(ctx, id, next, raw); err != nil {
				return link.SyncStatus, err
			}
			revisions[id] = driveRevision{ID: id, Parent: next.Parent}
		}
		link.Base, link.BaseContent = id, content
	}

	tips, err := revisionTips(revisions)
	if err != nil {
		return link.SyncStatus, err
	}
	conflicts := make(map[string]revision)
	var incoming *pendingApplication
	for _, tip := range tips {
		if tip.ID == link.Base {
			continue
		}
		remote, err := store.read(ctx, tip)
		if err != nil {
			return link.SyncStatus, err
		}
		if len(tips) > 1 {
			conflicts[tip.ID] = remote
		} else {
			incoming = &pendingApplication{ID: tip.ID, Base: previous.Base, Before: local, After: remote.Snapshot}
		}
	}

	err = canonical.WithWriter(root, func(w *canonical.Writer) error {
		current, err := readLink(w)
		if err != nil {
			return err
		}
		if current != previous {
			return decisionlog.ErrSnapshotConflict
		}
		if incoming != nil {
			if link.Base != current.Base {
				if err := saveLink(w, link); err != nil {
					return err
				}
			}
			incoming.Base = link.Base
			if err := writeJSON(w, pendingPath, incoming); err != nil {
				return err
			}
			if err := resumeApplication(w, &link); err != nil {
				return err
			}
		}
		for id, item := range conflicts {
			if err := saveConflict(w, id, item); err != nil {
				return err
			}
		}
		link.Conflicts = len(conflicts)
		link.LastSync = time.Now().UTC().Format(time.RFC3339)
		return saveLink(w, link)
	})
	return link.SyncStatus, err
}

func snapshotHash(snapshot decisionlog.Snapshot) string {
	data, _ := json.Marshal(snapshot)
	return canonical.Hash(data)
}

func resumeApplication(w *canonical.Writer, link *workspaceLink) error {
	raw, err := w.ReadLimit(pendingPath, 2*maxRevisionBytes+4096)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var pending pendingApplication
	if json.Unmarshal(raw, &pending) != nil || !revisionIDPattern.MatchString(pending.ID) {
		return fmt.Errorf("決定ログの保留中の同期情報が不正です")
	}
	if pending.ID == link.Base {
		return w.Remove(pendingPath)
	}
	if pending.Base != link.Base {
		return decisionlog.ErrSnapshotConflict
	}
	err = decisionlog.ApplySnapshot(w, pending.Before, pending.After)
	if errors.Is(err, decisionlog.ErrSnapshotConflict) {
		if err := saveConflict(w, pending.ID, revision{Parent: pending.Base, Snapshot: pending.After}); err != nil {
			return err
		}
		if err := w.Remove(pendingPath); err != nil {
			return err
		}
		return decisionlog.ErrSnapshotConflict
	}
	if err != nil {
		return err
	}
	link.Base, link.BaseContent = pending.ID, snapshotHash(pending.After)
	if err := saveLink(w, *link); err != nil {
		return err
	}
	return w.Remove(pendingPath)
}

func saveConflict(w *canonical.Writer, id string, item revision) error {
	return writeJSON(w, syncDir+"/conflicts/"+id+".json", item)
}

func revisionTips(revisions map[string]driveRevision) ([]driveRevision, error) {
	parents := make(map[string]bool, len(revisions))
	for _, item := range revisions {
		if item.Parent != "" {
			if _, exists := revisions[item.Parent]; !exists {
				return nil, fmt.Errorf("Google Driveの決定ログ同期履歴が揃っていません")
			}
			parents[item.Parent] = true
		}
	}
	var tips []driveRevision
	for id, item := range revisions {
		if !parents[id] {
			tips = append(tips, item)
		}
	}
	visited := make(map[string]bool, len(revisions))
	for _, tip := range tips {
		for id := tip.ID; id != "" && !visited[id]; id = revisions[id].Parent {
			visited[id] = true
		}
	}
	if len(visited) != len(revisions) {
		return nil, fmt.Errorf("Google Driveの決定ログ同期履歴に循環があります")
	}
	return tips, nil
}
