// karte-mcp is the STDIO entrypoint for the read-only Karte context MCP
// facade. It is designed to be registered in a Codex config as:
//
//	{
//	  "mcpServers": {
//	    "karte": {
//	      "command": "karte-mcp",
//	      "env": { "KARTE_DATA_DIR": "/absolute/path/to/codex-dedicated-root" }
//	    }
//	  }
//	}
//
// The process refuses to start unless the environment variable points at a
// directory that carries both .mdsys/context/v1/policy.json and
// .mdsys/context/v1/mcp-scope.json, and the policy grants the actor named in
// the scope marker both search and read. This keeps the adapter from
// silently binding to a shared local-only root.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"karte/internal/contextcore/mcp"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("karte-mcp", flag.ContinueOnError)
	flags.SetOutput(stderr)
	check := flags.Bool("check", false, "Validate the environment and exit without serving.")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	dataDir := os.Getenv("KARTE_DATA_DIR")
	server, err := mcp.NewServer(dataDir)
	if err != nil {
		fmt.Fprintf(stderr, "karte-mcp: %v\n", err)
		return 1
	}
	if *check {
		fmt.Fprintln(stdout, server.Summary())
		return 0
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.Run(ctx, stdin, stdout); err != nil && ctx.Err() == nil {
		fmt.Fprintf(stderr, "karte-mcp: %v\n", err)
		return 1
	}
	return 0
}
