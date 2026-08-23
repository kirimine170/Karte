package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"karte/internal/resourcebudget"
)

type reportPaths []string

func (paths *reportPaths) String() string {
	return fmt.Sprint([]string(*paths))
}

func (paths *reportPaths) Set(value string) error {
	if value == "" {
		return fmt.Errorf("report path is empty")
	}
	*paths = append(*paths, value)
	return nil
}

func main() {
	var baselinePath string
	var reports reportPaths
	var markdownPath string
	var evaluationPath string
	flag.StringVar(&baselinePath, "baseline", "resource-budget/baseline.json", "checked-in baseline JSON")
	flag.Var(&reports, "report", "measurement report JSON，repeat for each source")
	flag.StringVar(&markdownPath, "markdown-out", "", "optional Markdown summary path")
	flag.StringVar(&evaluationPath, "json-out", "", "optional merged evaluation JSON path")
	flag.Parse()

	if len(reports) == 0 {
		fail("at least one --report is required")
	}
	baseline, err := resourcebudget.LoadBaseline(baselinePath)
	if err != nil {
		fail(err.Error())
	}
	loaded := make([]resourcebudget.Report, 0, len(reports))
	for _, path := range reports {
		report, err := resourcebudget.LoadReport(path)
		if err != nil {
			fail(err.Error())
		}
		loaded = append(loaded, report)
	}
	evaluation := resourcebudget.Evaluate(baseline, loaded...)
	summary := resourcebudget.MarkdownSummary(baseline, evaluation)
	if markdownPath != "" {
		if err := os.WriteFile(markdownPath, []byte(summary), 0o644); err != nil {
			fail(fmt.Sprintf("write Markdown summary: %v", err))
		}
	}
	if evaluationPath != "" {
		encoded, err := json.MarshalIndent(evaluation, "", "  ")
		if err != nil {
			fail(fmt.Sprintf("encode evaluation: %v", err))
		}
		encoded = append(encoded, '\n')
		if err := os.WriteFile(evaluationPath, encoded, 0o644); err != nil {
			fail(fmt.Sprintf("write evaluation JSON: %v", err))
		}
	}
	fmt.Print(summary)
	if !evaluation.Pass {
		os.Exit(1)
	}
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, "resourcegate:", message)
	os.Exit(2)
}
