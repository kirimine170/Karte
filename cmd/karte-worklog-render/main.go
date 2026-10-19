// karte-worklog-render is a bounded read-only bridge from the local worklog
// extension to the official pinned KarteRenderer. It never starts a browser.
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"karte/internal/worklog"
	"os"
	"path/filepath"
	"time"

	renderer "github.com/kirimine170/KarteRenderer"
)

const rendererCommit = "30e5dfbab03f92e2eb7c8a67e17936579075fe88"
const maxInput = 1024
const maxOutput = 2 << 20

type response struct {
	SchemaVersion  int                      `json:"schema_version"`
	OK             bool                     `json:"ok"`
	Error          string                   `json:"error,omitempty"`
	TaskID         string                   `json:"task_id,omitempty"`
	DocID          string                   `json:"doc_id,omitempty"`
	RelativePath   string                   `json:"relative_path,omitempty"`
	SourceSHA256   string                   `json:"source_sha256,omitempty"`
	MarkdownSHA256 string                   `json:"markdown_sha256,omitempty"`
	MarkdownBase64 string                   `json:"markdown_base64,omitempty"`
	RendererCommit string                   `json:"renderer_commit,omitempty"`
	HTML           string                   `json:"html,omitempty"`
	Metadata       *renderer.RenderMetadata `json:"metadata,omitempty"`
}

func failure(code string) response { return response{SchemaVersion: 1, Error: code} }

func execute(args []string, in io.Reader) (result response) {
	if len(args) != 2 || args[0] != "--root" || args[1] == "" {
		return failure("invalid_request")
	}
	raw, err := io.ReadAll(io.LimitReader(in, maxInput+1))
	if err != nil || len(raw) > maxInput {
		return failure("invalid_request")
	}
	// Decode an object first: encoding/json matches struct fields without
	// regard to case, but each protocol permits only its exact keys. Decode
	// also rejects duplicate keys, invalid UTF-8 and trailing input.
	var fields map[string]json.RawMessage
	if worklog.Decode(raw, &fields) != nil || len(fields) != 1 && len(fields) != 3 {
		return failure("invalid_request")
	}
	var taskID string
	if json.Unmarshal(fields["task_id"], &taskID) != nil || taskID == "" {
		return failure("invalid_request")
	}
	schemaVersion := 1
	if len(fields) == 3 {
		var version int
		var operation string
		if json.Unmarshal(fields["schema_version"], &version) != nil || version != 2 || json.Unmarshal(fields["operation"], &operation) != nil || operation != "snapshot" {
			return failure("invalid_request")
		}
		schemaVersion = 2
	}
	// Only a fully validated request selects schema 2. Earlier protocol
	// failures keep the existing schema-1 invalid_request envelope.
	defer func() { result.SchemaVersion = schemaVersion }()
	service, err := worklog.New(args[1], "host_observation")
	if err != nil {
		return failure("permission_denied")
	}
	view, err := service.ReadView(taskID)
	if err != nil {
		switch {
		case errors.Is(err, worklog.ErrDenied):
			return failure("permission_denied")
		case errors.Is(err, worklog.ErrViewMissing):
			return failure("view_missing")
		case errors.Is(err, worklog.ErrViewConflict):
			return failure("view_conflict")
		case errors.Is(err, worklog.ErrViewRecord):
			return failure("invalid_record")
		case errors.Is(err, worklog.ErrViewV2):
			return failure("v2_conflict")
		default:
			return failure("read_failed")
		}
	}
	result = renderView(view)
	if schemaVersion == 2 && result.OK {
		// Use the very same authorized bytes that the renderer consumed. Do
		// not reopen a path, regenerate a document, or attach bytes on error.
		result.MarkdownBase64 = base64.StdEncoding.EncodeToString(view.Markdown)
	}
	return result
}

func renderView(view worklog.ReadViewResult) response {
	root, err := filepath.Abs("karte-render-memory")
	if err != nil {
		return failure("render_failed")
	}
	file := documentFS{path: filepath.Join(root, filepath.FromSlash(view.RelativePath)), content: view.Markdown}
	result, err := renderer.NewRenderer(file).RenderMarkdownResultWithOptions(root, view.RelativePath, false)
	if err != nil || result.Metadata.SchemaVersion != 1 {
		return failure("render_failed")
	}
	// A worklog has no active imports, assets, links or custom layout. Unexpected
	// dependencies fail closed even though the memory FS already denies access.
	if len(result.Metadata.Dependencies) != 1 || result.Metadata.Dependencies[0].Kind != renderer.DependencySource || result.Metadata.Dependencies[0].Path != view.RelativePath || len(result.Metadata.Assets) != 0 || len(result.Metadata.Links) != 0 || result.Metadata.FrontMatter.Marp {
		return failure("render_failed")
	}
	return response{SchemaVersion: 1, OK: true, TaskID: view.TaskID, DocID: view.DocID,
		RelativePath: view.RelativePath, SourceSHA256: view.SourceSHA256, MarkdownSHA256: view.MarkdownSHA256,
		RendererCommit: rendererCommit, HTML: result.HTML, Metadata: &result.Metadata}
}

// Only the verified Markdown is readable. No OSFileSystem is embedded; no
// method delegates to os.Open/ReadFile/Stat, themes, imports, or a fallback root.
type documentFS struct {
	path    string
	content []byte
}

func (f documentFS) ReadFile(name string) ([]byte, error) {
	if name != f.path {
		return nil, fs.ErrNotExist
	}
	return bytes.Clone(f.content), nil
}
func (f documentFS) Open(name string) (io.ReadCloser, error) {
	if name != f.path {
		return nil, fs.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(f.content)), nil
}
func (f documentFS) Stat(name string) (fs.FileInfo, error) {
	if name != f.path {
		return nil, fs.ErrNotExist
	}
	return documentInfo{name: filepath.Base(name), size: int64(len(f.content))}, nil
}

type documentInfo struct {
	name string
	size int64
}

func (i documentInfo) Name() string       { return i.name }
func (i documentInfo) Size() int64        { return i.size }
func (i documentInfo) Mode() fs.FileMode  { return 0400 }
func (i documentInfo) ModTime() time.Time { return time.Time{} }
func (i documentInfo) IsDir() bool        { return false }
func (i documentInfo) Sys() any           { return nil }

func writeResponse(out io.Writer, value response) int {
	raw, err := json.Marshal(value)
	if err != nil || len(raw)+1 > maxOutput {
		limit := failure("output_limit")
		if value.SchemaVersion == 2 {
			limit.SchemaVersion = 2
		}
		raw, _ = json.Marshal(limit)
	}
	raw = append(raw, '\n')
	if _, err := out.Write(raw); err != nil {
		return 1
	}
	return 0
}
func run(args []string, in io.Reader, out io.Writer) int {
	return writeResponse(out, execute(args, in))
}
func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout)) }
