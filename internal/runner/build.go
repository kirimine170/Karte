package runner

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"

	"karte/internal/site"
)

type IndexEntry struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}
type Index struct {
	Items []IndexEntry `json:"items"`
}

func Build(root string) error {
	// ensure root is absolute
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	root = absRoot

	pub := filepath.Join(root, "public")
	tmp := filepath.Join(root, ".mdsys", "_public_tmp")
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}

	idx := Index{}

	contentDir := filepath.Join(root, "content")
	fs.WalkDir(os.DirFS(contentDir), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(p) != ".md" {
			return nil
		}
		src := filepath.Join(contentDir, p)
		dst := filepath.Join(tmp, p[:len(p)-3]+".html")
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		html, _, err := site.RenderMarkdown(root, src)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, []byte(html), 0o644); err != nil {
			return err
		}
		id := filepath.ToSlash(p)
		idx.Items = append(idx.Items, IndexEntry{ID: id, Path: id})
		return nil
	})
	b, _ := json.MarshalIndent(idx, "", "  ")
	_ = os.WriteFile(filepath.Join(root, ".mdsys", "index.json"), b, 0o644)
	// ここで原子的入れ替え（旧publicを消してからrename）
	_ = os.RemoveAll(pub)
	if err := os.Rename(tmp, pub); err != nil {
		// 失敗時は tmp を消しておく（次回ビルドに影響しないように）
		_ = os.RemoveAll(tmp)
		return err
	}
	return nil
}
