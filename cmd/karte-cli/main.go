package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"karte/internal/kartecore"
)

const (
	exitOK           = 0
	exitInvalidInput = 2
	exitNotFound     = 3
	exitConflict     = 4
	exitInternal     = 10
)

// Override with -ldflags "-X main.CLIVersion=<version>" when packaging releases.
var CLIVersion = "dev"

type outputEnvelope struct {
	OK     bool         `json:"ok"`
	Result any          `json:"result,omitempty"`
	Error  *errorDetail `json:"error,omitempty"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		writeError(stdout, stderr, true, "invalid_input", "subcommand is required", "")
		return exitInvalidInput
	}

	subcommand := args[0]
	subArgs := args[1:]

	common := flag.NewFlagSet(subcommand, flag.ContinueOnError)
	common.SetOutput(io.Discard)
	rootFlag := common.String("root", ".", "project root containing karte_data")
	jsonFlag := common.Bool("json", false, "emit JSON output")

	svcFactory := func(root string) *kartecore.Service {
		absRoot, err := filepath.Abs(root)
		if err != nil {
			absRoot = root
		}
		return kartecore.New(absRoot, nil)
	}

	switch subcommand {
	case "init":
		if err := common.Parse(subArgs); err != nil {
			writeError(stdout, stderr, true, "invalid_input", err.Error(), "")
			return exitInvalidInput
		}
		svc := svcFactory(*rootFlag)
		if err := svc.Init(); err != nil {
			return handleError(stdout, stderr, *jsonFlag, err)
		}
		return writeSuccess(stdout, stderr, *jsonFlag, map[string]string{"dataDir": svc.DataDir})
	case "list":
		if err := common.Parse(subArgs); err != nil {
			writeError(stdout, stderr, true, "invalid_input", err.Error(), "")
			return exitInvalidInput
		}
		svc := svcFactory(*rootFlag)
		items, err := svc.ListFiles()
		if err != nil {
			return handleError(stdout, stderr, *jsonFlag, err)
		}
		return writeSuccess(stdout, stderr, *jsonFlag, items)
	case "read":
		pathFlag := common.String("path", "", "document path (.md), content/ prefix optional")
		if err := common.Parse(subArgs); err != nil {
			writeError(stdout, stderr, true, "invalid_input", err.Error(), "")
			return exitInvalidInput
		}
		svc := svcFactory(*rootFlag)
		normalizedPath := normalizeCLIPath(*pathFlag)
		content, err := svc.Read(normalizedPath)
		if err != nil {
			return handleError(stdout, stderr, *jsonFlag, err)
		}
		return writeSuccess(stdout, stderr, *jsonFlag, map[string]string{"path": normalizedPath, "content": content})
	case "create":
		pathFlag := common.String("path", "", "document path (.md), content/ prefix optional")
		titleFlag := common.String("title", "", "title in frontmatter")
		if err := common.Parse(subArgs); err != nil {
			writeError(stdout, stderr, true, "invalid_input", err.Error(), "")
			return exitInvalidInput
		}
		svc := svcFactory(*rootFlag)
		createdPath, err := svc.Create(normalizeCLIPath(*pathFlag), *titleFlag)
		if err != nil {
			return handleError(stdout, stderr, *jsonFlag, err)
		}
		return writeSuccess(stdout, stderr, *jsonFlag, map[string]string{"path": createdPath})
	case "write":
		pathFlag := common.String("path", "", "document path (.md), content/ prefix optional")
		contentFile := common.String("content-file", "", "path to markdown content file")
		createFlag := common.Bool("create", false, "create file if it does not exist")
		if err := common.Parse(subArgs); err != nil {
			writeError(stdout, stderr, true, "invalid_input", err.Error(), "")
			return exitInvalidInput
		}
		if strings.TrimSpace(*contentFile) == "" {
			writeError(stdout, stderr, *jsonFlag, "invalid_input", "--content-file is required", "")
			return exitInvalidInput
		}
		payload, err := os.ReadFile(*contentFile)
		if err != nil {
			writeError(stdout, stderr, *jsonFlag, "invalid_input", "failed to read --content-file", err.Error())
			return exitInvalidInput
		}
		svc := svcFactory(*rootFlag)
		normalizedPath := normalizeCLIPath(*pathFlag)
		if err := svc.Write(normalizedPath, string(payload), *createFlag); err != nil {
			return handleError(stdout, stderr, *jsonFlag, err)
		}
		return writeSuccess(stdout, stderr, *jsonFlag, map[string]any{"path": normalizedPath, "bytes": len(payload)})
	case "build":
		if err := common.Parse(subArgs); err != nil {
			writeError(stdout, stderr, true, "invalid_input", err.Error(), "")
			return exitInvalidInput
		}
		svc := svcFactory(*rootFlag)
		if err := svc.Build(); err != nil {
			return handleError(stdout, stderr, *jsonFlag, err)
		}
		return writeSuccess(stdout, stderr, *jsonFlag, map[string]string{"publicDir": filepath.Join(svc.DataDir, "public")})
	case "preview":
		pathFlag := common.String("path", "", "document path (.md), content/ prefix optional")
		if err := common.Parse(subArgs); err != nil {
			writeError(stdout, stderr, true, "invalid_input", err.Error(), "")
			return exitInvalidInput
		}
		svc := svcFactory(*rootFlag)
		normalizedPath := normalizeCLIPath(*pathFlag)
		html, err := svc.Preview(normalizedPath)
		if err != nil {
			return handleError(stdout, stderr, *jsonFlag, err)
		}
		return writeSuccess(stdout, stderr, *jsonFlag, map[string]string{"path": normalizedPath, "html": html})
	case "graph":
		if err := common.Parse(subArgs); err != nil {
			writeError(stdout, stderr, true, "invalid_input", err.Error(), "")
			return exitInvalidInput
		}
		svc := svcFactory(*rootFlag)
		graph, err := svc.Graph()
		if err != nil {
			return handleError(stdout, stderr, *jsonFlag, err)
		}
		return writeSuccess(stdout, stderr, *jsonFlag, graph)
	default:
		writeError(stdout, stderr, true, "invalid_input", fmt.Sprintf("unknown subcommand: %s", subcommand), "")
		return exitInvalidInput
	}
}

func normalizeCLIPath(path string) string {
	trimmed := strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(path)), "/")
	if trimmed == "" || strings.HasPrefix(trimmed, "content/") {
		return trimmed
	}
	return "content/" + trimmed
}

func handleError(stdout io.Writer, stderr io.Writer, jsonOut bool, err error) int {
	var coreErr *kartecore.Error
	if errors.As(err, &coreErr) {
		code := mapErrorCode(coreErr.Code)
		details := ""
		if coreErr.Err != nil {
			details = coreErr.Err.Error()
		}
		writeError(stdout, stderr, jsonOut, string(coreErr.Code), coreErr.Message, details)
		return code
	}
	writeError(stdout, stderr, jsonOut, string(kartecore.ErrCodeInternal), "unexpected error", err.Error())
	return exitInternal
}

func mapErrorCode(code kartecore.ErrorCode) int {
	switch code {
	case kartecore.ErrCodeInvalidInput:
		return exitInvalidInput
	case kartecore.ErrCodeNotFound:
		return exitNotFound
	case kartecore.ErrCodeConflict:
		return exitConflict
	default:
		return exitInternal
	}
}

func writeSuccess(stdout io.Writer, stderr io.Writer, jsonOut bool, result any) int {
	if jsonOut {
		_ = json.NewEncoder(stdout).Encode(outputEnvelope{OK: true, Result: result})
		return exitOK
	}
	body, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, "failed to marshal result")
		return exitInternal
	}
	fmt.Fprintln(stdout, string(body))
	return exitOK
}

func writeError(stdout io.Writer, stderr io.Writer, jsonOut bool, code, message, details string) {
	if jsonOut {
		_ = json.NewEncoder(stdout).Encode(outputEnvelope{
			OK: false,
			Error: &errorDetail{
				Code:    code,
				Message: message,
				Details: details,
			},
		})
		return
	}
	if details != "" {
		fmt.Fprintf(stderr, "%s: %s\n", message, details)
		return
	}
	fmt.Fprintln(stderr, message)
}
