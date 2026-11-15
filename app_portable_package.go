package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"karte/internal/canonical"
	"karte/internal/contextcore"
	fm "karte/internal/frontmatter"
	"karte/internal/portablepackage"
)

var (
	portablePackageSaveFilters = []runtime.FileFilter{{DisplayName: "Karte Package", Pattern: "*" + portablepackage.Extension}}
	portablePackageOpenFilters = []runtime.FileFilter{{DisplayName: "Karte Package", Pattern: "*" + portablepackage.Extension + ";*" + portablepackage.LegacyExtension}}
)

// ExportPortablePackage shares an authorized, saved snapshot for the local
// user to move to another workspace under their control.
func (a *App) ExportPortablePackage(relativePath string) (bool, error) {
	if a.ctx == nil {
		return false, fmt.Errorf("アプリの準備が完了していません")
	}
	snapshot, err := a.portablePackageSnapshot(relativePath)
	if err != nil {
		return false, err
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "自分の別端末用にパッケージを保存",
		DefaultFilename: strings.TrimSuffix(filepath.Base(relativePath), filepath.Ext(relativePath)) + portablepackage.Extension,
		Filters:         portablePackageSaveFilters,
	})
	if err != nil || path == "" {
		return false, err
	}
	// Follow the exact native-dialog destination and its overwrite confirmation.
	if !strings.EqualFold(filepath.Ext(path), portablepackage.Extension) {
		return false, fmt.Errorf("拡張子を %s にしてください", portablepackage.Extension)
	}
	latest, err := a.portablePackageSnapshot(relativePath)
	if err != nil {
		return false, err
	}
	if latest.Manifest != snapshot.Manifest {
		return false, fmt.Errorf("文書が変更されました。もう一度書き出してください")
	}
	if err := portablepackage.Write(path, snapshot); err != nil {
		return false, fmt.Errorf("パッケージを書き出せませんでした")
	}
	return true, nil
}

func (a *App) portablePackageSnapshot(relativePath string) (portablepackage.Snapshot, error) {
	unavailable := errors.New("この文書はパッケージとして書き出せません")
	if a.dataDir == "" || !strings.EqualFold(filepath.Ext(relativePath), ".md") ||
		strings.HasSuffix(strings.ToLower(relativePath), ".board.md") {
		return portablepackage.Snapshot{}, unavailable
	}
	absPath, ok := a.resolveContentPath(relativePath)
	if !ok {
		return portablepackage.Snapshot{}, unavailable
	}
	resource, err := a.authorizedExportResource(relativePath)
	if err != nil {
		return portablepackage.Snapshot{}, unavailable
	}
	relative, err := a.canonicalContentRelative(absPath)
	if err != nil || filepath.ToSlash(relative) != relativePath {
		return portablepackage.Snapshot{}, unavailable
	}
	var data []byte
	err = canonical.WithWriter(a.dataDir, func(w *canonical.Writer) error {
		var err error
		data, err = w.ReadLimit(relative, portablepackage.MaxMarkdownBytes)
		return err
	})
	if err != nil || canonical.Hash(data) != resource.SHA256 {
		return portablepackage.Snapshot{}, unavailable
	}
	snapshot, err := portablepackage.PersonalProjection(data, strings.TrimSuffix(filepath.Base(relativePath), filepath.Ext(relativePath)), resource)
	if errors.Is(err, portablepackage.ErrNotStandalone) {
		return portablepackage.Snapshot{}, fmt.Errorf("パッケージには、画像・外部リンク・埋め込みのない Markdown を選んでください")
	}
	return snapshot, err
}

// ImportPortablePackage previews a verified, inert snapshot before an
// explicit user action creates a document in the destination workspace.
func (a *App) ImportPortablePackage() (bool, error) {
	if a.ctx == nil {
		return false, fmt.Errorf("アプリの準備が完了していません")
	}
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Karte パッケージを選択", Filters: portablePackageOpenFilters,
	})
	if err != nil || path == "" {
		return false, err
	}
	snapshot, err := portablepackage.Read(path)
	if errors.Is(err, portablepackage.ErrUnsupported) {
		return false, fmt.Errorf("このパッケージ形式のバージョンには対応していません")
	}
	if err != nil {
		return false, fmt.Errorf("パッケージを検証できませんでした")
	}
	resource, err := snapshot.Resource()
	if err != nil {
		return false, err
	}
	if err := a.authorizePortableImport(resource); err != nil {
		return false, err
	}
	answer, err := runtime.MessageDialog(a.ctx, runtime.MessageDialogOptions{
		Type:    runtime.QuestionDialog,
		Title:   "文書を取り込む",
		Message: fmt.Sprintf("「%s」を取り込みますか？\n\nプロジェクト %s ／ 機密区分 %s\n自分で書き出したファイルを取り込んでください。", snapshot.Title(), resource.Project, resource.Sensitivity),
		Buttons: []string{"Yes", "No"}, DefaultButton: "No", CancelButton: "No",
	})
	if err != nil || answer != "Yes" {
		return false, err
	}
	importedPath, err := a.installPortableSnapshot(snapshot)
	if err != nil {
		return false, err
	}
	a.emitEvent("file-changed", importedPath)
	return true, nil
}

func (a *App) authorizePortableImport(resource contextcore.Resource) error {
	policy, err := contextcore.LoadPolicy(a.dataDir)
	if err != nil {
		return fmt.Errorf("このワークスペースのポリシーでは取り込めません")
	}
	correlation := "portable-import:" + resource.SHA256 + ":" + time.Now().UTC().Format(time.RFC3339Nano)
	decision, err := a.authorizeAndAudit(policy, correlation, localHumanActor, contextcore.CapabilityReview, resource)
	if err == nil && decision.Allowed {
		return nil
	}
	return fmt.Errorf("このワークスペースのポリシーでは取り込めません")
}

func (a *App) installPortableSnapshot(snapshot portablepackage.Snapshot) (string, error) {
	if a.dataDir == "" {
		return "", fmt.Errorf("ワークスペースの準備が完了していません")
	}
	path := "content/portable/" + canonical.Hash([]byte(snapshot.Manifest.RootResourceID)) + ".md"
	resource, err := snapshot.Resource()
	if err != nil {
		return "", err
	}
	err = canonical.WithWriter(a.dataDir, func(w *canonical.Writer) error {
		if err := a.authorizePortableImport(resource); err != nil {
			return err
		}
		root, err := os.OpenRoot(a.dataDir)
		if err != nil {
			return err
		}
		defer root.Close()
		existing, err := findPortableImportID(root, w, snapshot.Manifest.RootResourceID)
		if err != nil {
			return fmt.Errorf("既存の文書 ID を確認できませんでした")
		}
		if existing != "" {
			current, err := w.ReadLimit(existing, portablepackage.MaxMarkdownBytes)
			if err == nil && bytes.Equal(current, snapshot.Markdown) {
				path = existing
				return nil
			}
			return fmt.Errorf("同じ文書 ID の別の版がすでにあります")
		}
		if err := w.WriteCAS(filepath.FromSlash(path), nil, snapshot.Markdown, 0600); err != nil {
			return fmt.Errorf("パッケージを保存できませんでした: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return path, nil
}

// Check the destination while holding its canonical writer lock. Keep
// ambiguous IDs out of the Context index, even after a document was renamed.
func findPortableImportID(root *os.Root, w *canonical.Writer, docID string) (string, error) {
	var found string
	err := fs.WalkDir(root.FS(), "content", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() || !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		// Oversize documents are outside the Context index's ID space.
		if info.Size() > portablepackage.MaxMarkdownBytes {
			return nil
		}
		data, err := w.ReadLimit(filepath.FromSlash(path), portablepackage.MaxMarkdownBytes)
		if err != nil {
			return err
		}
		if strings.TrimSpace(fm.ExtractDocID(string(data))) == docID {
			if found != "" {
				return fmt.Errorf("duplicate document ID")
			}
			found = path
		}
		return nil
	})
	return found, err
}
