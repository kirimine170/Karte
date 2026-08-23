package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"karte/internal/compliance"
)

func main() {
	root := flag.String("root", ".", "repository root")
	artifactRoot := flag.String("artifact-root", "", "unpacked platform artifact root (.app on macOS)")
	platform := flag.String("platform", "", "artifact platform: darwin，windows，or linux")
	flag.Parse()
	if flag.NArg() != 1 {
		fatal("usage: go run ./cmd/licensegate [-root PATH] [-artifact-root PATH -platform OS] generate|verify|audit|artifact-audit")
	}
	absoluteRoot, err := filepath.Abs(*root)
	if err != nil {
		fatal(err.Error())
	}
	ctx := context.Background()
	switch flag.Arg(0) {
	case "generate":
		generated, result, err := compliance.GenerateRepositoryFiles(ctx, absoluteRoot)
		if err != nil {
			fatal(err.Error())
		}
		if err := compliance.WriteGeneratedFiles(absoluteRoot, generated); err != nil {
			fatal(err.Error())
		}
		fmt.Printf("generated deterministic compliance inventory for %d components\n", len(result.Components))
		if len(result.Problems) > 0 {
			fmt.Printf("generation completed with %d fail-closed audit problem(s); run audit for details\n", len(result.Problems))
		}
	case "verify":
		generated, result, err := compliance.GenerateRepositoryFiles(ctx, absoluteRoot)
		if err != nil {
			fatal(err.Error())
		}
		if err := compliance.VerifyGeneratedFiles(absoluteRoot, generated); err != nil {
			fatal(err.Error())
		}
		fmt.Printf("verified deterministic compliance output for %d components\n", len(result.Components))
	case "audit":
		if err := compliance.AuditRepository(ctx, absoluteRoot, time.Now().UTC()); err != nil {
			fatal(err.Error())
		}
		fmt.Println("license and distribution audit passed")
	case "artifact-audit":
		if *artifactRoot == "" || *platform == "" {
			fatal("artifact-audit requires -artifact-root and -platform")
		}
		absoluteArtifactRoot, err := filepath.Abs(*artifactRoot)
		if err != nil {
			fatal(err.Error())
		}
		if err := compliance.AuditArtifact(absoluteRoot, absoluteArtifactRoot, *platform, time.Now().UTC()); err != nil {
			fatal(err.Error())
		}
		fmt.Println("artifact dependency，license，and file coverage audit passed")
	default:
		fatal("unknown command " + flag.Arg(0))
	}
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
