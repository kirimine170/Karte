// karte-experiment is a synthetic, proposal-only Worker bridge.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"karte/internal/ephyoutbox"
	"os"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, output io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: karte-experiment prepare|publish|status -data-root DIR [options]")
	}
	operation := args[0]
	if operation != "prepare" && operation != "publish" && operation != "status" {
		return fmt.Errorf("unsupported producer operation: %s", operation)
	}
	f := flag.NewFlagSet("karte-experiment "+operation, flag.ContinueOnError)
	dataRoot := f.String("data-root", "", "Existing synthetic Karte data directory")
	bundle := f.String("worker-bundle", "", "Read-only Worker v1 bundle with evidence-manifest.json")
	metadata := f.String("metadata", "", "Versioned synthetic experiment metadata JSON")
	id := f.String("candidate-id", "", "Prepared candidate identity")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 || *dataRoot == "" {
		return fmt.Errorf("data-root is required and positional arguments are unsupported")
	}
	if operation == "prepare" {
		if *bundle == "" || *metadata == "" || *id != "" {
			return fmt.Errorf("prepare requires worker-bundle and metadata, with candidate_id supplied only by metadata")
		}
	} else if *id == "" || *bundle != "" || *metadata != "" {
		return fmt.Errorf("publish/status require candidate-id and use the immutable prepared payload")
	}
	producer, err := ephyoutbox.NewExperimentProducer(*dataRoot)
	if err != nil {
		return err
	}
	defer producer.Close()
	var status ephyoutbox.ExperimentProducerStatus
	switch operation {
	case "prepare":
		status, err = producer.Prepare(*bundle, *metadata)
	case "publish":
		status, err = producer.Publish(*id)
	case "status":
		status, err = producer.Status(*id)
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(status)
}
