package runner

import (
    "embed"
    "io/fs"
    "os"
    "path/filepath"
)

//go:embed skeleton/**
var skeletonFS embed.FS

func InitProject(root string) error {
    paths := []string{".mdsys", "content", "data", filepath.Join("themes", "default")}
    for _, p := range paths {
        if err := os.MkdirAll(filepath.Join(root, p), 0o755); err != nil { return err }
    }
    // copy skeleton files into root
    if err := copyDir(skeletonFS, "skeleton", root); err != nil { return err }
    return nil
}

func copyDir(e embed.FS, src, dst string) error {
    return fs.WalkDir(e, src, func(path string, d fs.DirEntry, err error) error {
        if err != nil { return err }
        rel, _ := filepath.Rel(src, path)
        if rel == "." { return nil }
        out := filepath.Join(dst, rel)
        if d.IsDir() { return os.MkdirAll(out, 0o755) }
        b, readErr := e.ReadFile(path)
        if readErr != nil { return readErr }
        if err := os.WriteFile(out, b, 0o644); err != nil { return err }
        return nil
    })
}
