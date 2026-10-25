// postlab-assets refreshes the browser cache from the pinned KarteRenderer module．
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/kirimine170/KarteRenderer/instagram"
)

func main() {
	output := flag.String("output", "frontend/public/postlab-renderer", "generated asset directory")
	check := flag.Bool("check", false, "verify generated assets without writing")
	flag.Parse()
	hashes := map[string]string{}
	files := map[string][]byte{}
	for _, name := range []string{"renderer.mjs", "preview.css"} {
		data, err := fs.ReadFile(instagram.WebAssets(), name)
		if err != nil {
			panic(err)
		}
		sum := sha256.Sum256(data)
		hashes[name] = hex.EncodeToString(sum[:])
		files[name] = data
	}
	manifest, _ := json.MarshalIndent(struct {
		Source string            `json:"source"`
		SHA256 map[string]string `json:"sha256"`
	}{"github.com/kirimine170/KarteRenderer/instagram.WebAssets", hashes}, "", "  ")
	files["manifest.json"] = append(manifest, '\n')
	if !*check {
		if err := os.MkdirAll(*output, 0o755); err != nil {
			panic(err)
		}
	}
	for name, data := range files {
		path := filepath.Join(*output, name)
		if *check {
			actual, err := os.ReadFile(path)
			if err != nil || string(actual) != string(data) {
				fmt.Fprintln(os.Stderr, "stale renderer asset:", path)
				os.Exit(1)
			}
		} else if err := os.WriteFile(path, data, 0o644); err != nil {
			panic(err)
		}
	}
}
