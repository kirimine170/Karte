// Package canonical serializes Karte content and policy commits across processes.
package canonical

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var ErrConflict = errors.New("conflict")

type Writer struct {
	root          *os.Root
	BeforeReplace func(string) error
}

func Hash(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func WithWriter(dir string, fn func(*Writer) error) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	w := &Writer{root: root}
	if err := w.MkdirAll(".mdsys", 0700); err != nil {
		return err
	}
	if err := w.check(".mdsys/write.lock"); err != nil {
		return err
	}
	f, err := root.OpenFile(".mdsys/write.lock", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if errors.Is(err, os.ErrExist) || errors.Is(err, os.ErrNotExist) {
		f, err = root.OpenFile(".mdsys/write.lock", os.O_RDWR, 0600)
	}
	if err != nil {
		return err
	}
	defer f.Close()
	if err := lock(f); err != nil {
		return err
	}
	defer unlock(f)
	return fn(w)
}

func (w *Writer) check(name string) error {
	name = filepath.FromSlash(name)
	if name == "." {
		return nil
	}
	if !filepath.IsLocal(name) || filepath.Clean(name) != name {
		return fmt.Errorf("invalid path")
	}
	parts := strings.Split(name, string(filepath.Separator))
	for i := range parts {
		st, err := w.root.Lstat(filepath.Join(parts[:i+1]...))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 || (i < len(parts)-1 && !st.IsDir()) || (!st.IsDir() && !st.Mode().IsRegular()) {
			return fmt.Errorf("unsafe path")
		}
	}
	return nil
}
func (w *Writer) MkdirAll(name string, perm fs.FileMode) error {
	name = filepath.FromSlash(name)
	if err := w.check(name); err != nil {
		return err
	}
	// Sync each parent after creating a directory，including the durable journal tree．
	parts := strings.Split(name, string(filepath.Separator))
	parent := "."
	for i := range parts {
		p := filepath.Join(parts[:i+1]...)
		if err := w.root.Mkdir(p, perm); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		if err := w.check(p); err != nil {
			return err
		}
		if err := syncDirectory(w.root, parent); err != nil {
			return err
		}
		parent = p
	}
	return nil
}
func (w *Writer) Read(name string) ([]byte, error) {
	if err := w.check(name); err != nil {
		return nil, err
	}
	return w.root.ReadFile(name)
}
func (w *Writer) Entries(name string) ([]os.DirEntry, error) {
	if err := w.check(name); err != nil {
		return nil, err
	}
	f, e := w.root.Open(name)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	return f.ReadDir(-1)
}
func (w *Writer) CurrentHash(name string) (*string, error) {
	b, e := w.Read(name)
	if errors.Is(e, os.ErrNotExist) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	h := Hash(b)
	return &h, nil
}
func same(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
func (w *Writer) WriteCAS(name string, expected *string, data []byte, perm fs.FileMode) error {
	if err := w.check(name); err != nil {
		return err
	}
	parent := filepath.Dir(name)
	if err := w.MkdirAll(parent, 0700); err != nil {
		return err
	}
	nonce := make([]byte, 16)
	if _, e := rand.Read(nonce); e != nil {
		return e
	}
	temp := filepath.Join(parent, ".karte-write-"+hex.EncodeToString(nonce))
	f, err := w.root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	defer w.root.Remove(temp)
	if err = preparePermissions(w.root, temp, name, perm); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if w.BeforeReplace != nil {
		if err = w.BeforeReplace(name); err != nil {
			return err
		}
	}
	current, err := w.CurrentHash(name)
	if err != nil {
		return err
	}
	if !same(current, expected) {
		return ErrConflict
	}
	if err = w.root.Rename(temp, name); err != nil {
		return err
	}
	return syncDirectory(w.root, parent)
}
func (w *Writer) Write(name string, data []byte, perm fs.FileMode) error {
	h, e := w.CurrentHash(name)
	if e != nil {
		return e
	}
	return w.WriteCAS(name, h, data, perm)
}
func (w *Writer) Remove(name string) error {
	if e := w.check(name); e != nil {
		return e
	}
	e := w.root.Remove(name)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	return syncDirectory(w.root, filepath.Dir(name))
}

// ReadLimit bounds untrusted mailbox reads before allocating their payload．
func (w *Writer) ReadLimit(name string, limit int64) ([]byte, error) {
	if err := w.check(name); err != nil {
		return nil, err
	}
	f, err := w.root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() > limit {
		return nil, fmt.Errorf("payload_capacity")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("payload_capacity")
	}
	return data, err
}

func (w *Writer) Permissions(name string) (fs.FileMode, error) {
	if err := w.check(name); err != nil {
		return 0, err
	}
	info, err := w.root.Lstat(name)
	if err != nil {
		return 0, err
	}
	return info.Mode().Perm(), nil
}
