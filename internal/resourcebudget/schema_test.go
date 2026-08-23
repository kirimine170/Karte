package resourcebudget

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEvaluateRejectsUnknownDuplicateMissingAndOverBudgetMetrics(t *testing.T) {
	baseline := testBaseline()
	tests := []struct {
		name    string
		reports []Report
		want    string
	}{
		{
			name: "unknown",
			reports: []Report{testReport("backend",
				testMeasurement("backend.idle.goroutines", 4),
				testMeasurement("backend.unknown", 1),
			)},
			want: "unknown metric backend.unknown",
		},
		{
			name: "duplicate",
			reports: []Report{
				testReport("backend", testMeasurement("backend.idle.goroutines", 4)),
				testReport("backend", testMeasurement("backend.idle.goroutines", 4)),
			},
			want: "duplicate measurement across reports backend.idle.goroutines",
		},
		{
			name:    "missing",
			reports: []Report{testReport("backend")},
			want:    "missing required metric backend.idle.goroutines",
		},
		{
			name: "limit",
			reports: []Report{testReport("backend",
				testMeasurement("backend.idle.goroutines", 7),
			)},
			want: "exceeds limit=6",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evaluation := Evaluate(baseline, test.reports...)
			if evaluation.Pass {
				t.Fatalf("Evaluate() passed，want failure: %+v", evaluation)
			}
			if !strings.Contains(strings.Join(evaluation.Violations, "\n"), test.want) {
				t.Fatalf("violations = %v，want %q", evaluation.Violations, test.want)
			}
		})
	}
}

func TestEvaluateRejectsUnitStatisticSourceAndPolicyMismatch(t *testing.T) {
	baseline := testBaseline()
	report := testReport("frontend", testMeasurement("backend.idle.goroutines", 4))
	report.Measurements[0].Unit = "bytes"
	evaluation := Evaluate(baseline, report)
	joined := strings.Join(evaluation.Violations, "\n")
	for _, want := range []string{"came from frontend", "unit=bytes"} {
		if !strings.Contains(joined, want) {
			t.Errorf("violations missing %q: %v", want, evaluation.Violations)
		}
	}

	policyReport := testReport("backend", testMeasurement("backend.idle.goroutines", 4))
	policyReport.Policy.Samples = 7
	policyReport.Measurements[0].Samples = []float64{4, 4, 4, 4, 4, 4, 4}
	evaluation = Evaluate(baseline, policyReport)
	if !strings.Contains(strings.Join(evaluation.Violations, "\n"), "sampling policy differs") {
		t.Errorf("policy mismatch violations = %v", evaluation.Violations)
	}

	invalidStatistic := testReport("backend", testMeasurement("backend.idle.goroutines", 4))
	invalidStatistic.Measurements[0].Statistic = "max"
	if err := ValidateReport(invalidStatistic); err == nil || !strings.Contains(err.Error(), "statistic must be median") {
		t.Fatalf("ValidateReport() error = %v，want statistic rejection", err)
	}
}

func TestValidateRejectsNonFiniteValuesAndDuplicateBaseline(t *testing.T) {
	baseline := testBaseline()
	baseline.Metrics = append(baseline.Metrics, baseline.Metrics[0])
	baseline.Metrics[0].Limit = math.Inf(1)
	err := ValidateBaseline(baseline)
	if err == nil || !strings.Contains(err.Error(), "duplicate baseline metric") || !strings.Contains(err.Error(), "must be finite") {
		t.Fatalf("ValidateBaseline() error = %v", err)
	}

	report := testReport("backend", testMeasurement("backend.idle.goroutines", math.NaN()))
	err = ValidateReport(report)
	if err == nil || !strings.Contains(err.Error(), "must be finite") {
		t.Fatalf("ValidateReport() error = %v", err)
	}
}

func TestValidateBaselineRejectsMarkdownControlCharacters(t *testing.T) {
	baseline := testBaseline()
	baseline.Metrics[0].Scenario = "idle|injected\nrow"
	baseline.Profiles = []ObserveOnlyProfile{{
		ID:        "native",
		Scenario:  "idle",
		Metrics:   []string{"rss|bytes"},
		Reason:    "fixture",
		Procedure: "fixture",
	}}
	err := ValidateBaseline(baseline)
	if err == nil || !strings.Contains(err.Error(), "scenario and unit must be safe") || !strings.Contains(err.Error(), "metrics[0] must be a safe") {
		t.Fatalf("ValidateBaseline() error = %v，want Markdown control rejection", err)
	}
}

func TestValidateReportBindsValueToExactSampleMedianAndCount(t *testing.T) {
	report := testReport("backend", testMeasurement("backend.idle.goroutines", 4))
	report.Measurements[0].Value = 5
	if err := ValidateReport(report); err == nil || !strings.Contains(err.Error(), "must equal the median") {
		t.Fatalf("ValidateReport() error = %v，want median mismatch", err)
	}

	report = testReport("backend", testMeasurement("backend.idle.goroutines", 4))
	report.Measurements[0].Samples = []float64{4}
	if err := ValidateReport(report); err == nil || !strings.Contains(err.Error(), "want policy.samples=3") {
		t.Fatalf("ValidateReport() error = %v，want sample count mismatch", err)
	}
}

func TestLoadBaselineUsesStrictVersionedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.json")
	source := `{
  "schemaVersion": 1,
  "suite": "test",
  "policy": {"warmup": 1, "samples": 3, "aggregation": "median", "gomaxprocs": 2, "latencyClock": "monotonic"},
  "metrics": [{
    "id": "backend.idle.goroutines", "source": "backend", "scenario": "idle", "unit": "count",
    "statistic": "median", "comparison": "lte", "baseline": 4, "limit": 6,
    "gate": true, "required": true, "stability": "deterministic", "description": "fixture"
  }],
  "observeOnlyProfiles": [],
  "unexpected": true
}`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBaseline(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("LoadBaseline() error = %v，want unknown field", err)
	}
}

func TestEvaluatePassAndMarkdownDiff(t *testing.T) {
	baseline := testBaseline()
	report := testReport("backend", testMeasurement("backend.idle.goroutines", 5))
	evaluation := Evaluate(baseline, report)
	if !evaluation.Pass {
		t.Fatalf("Evaluate() = %+v，want pass", evaluation)
	}
	summary := MarkdownSummary(baseline, evaluation)
	for _, want := range []string{"Result: PASS", "backend.idle.goroutines", "+1 (+25%)", "lte 6"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary missing %q:\n%s", want, summary)
		}
	}
}

func TestMedianUsesStableCopy(t *testing.T) {
	samples := []float64{9, 1, 5, 3, 7}
	if got := Median(samples); got != 5 {
		t.Fatalf("Median() = %v，want 5", got)
	}
	if samples[0] != 9 {
		t.Fatalf("Median mutated input: %v", samples)
	}
}

func testBaseline() Baseline {
	return Baseline{
		SchemaVersion: SchemaVersion,
		Suite:         "test",
		Policy: SamplingPolicy{
			Warmup:       1,
			Samples:      3,
			Aggregation:  "median",
			GOMAXPROCS:   2,
			LatencyClock: "monotonic",
		},
		Metrics: []BudgetMetric{{
			ID:          "backend.idle.goroutines",
			Source:      "backend",
			Scenario:    "idle",
			Unit:        "count",
			Statistic:   "median",
			Comparison:  "lte",
			Baseline:    4,
			Limit:       6,
			Gate:        true,
			Required:    true,
			Stability:   "deterministic",
			Description: "fixture",
		}},
	}
}

func testReport(source string, measurements ...Measurement) Report {
	return Report{
		SchemaVersion: SchemaVersion,
		Suite:         "test",
		Source:        source,
		Policy:        testBaseline().Policy,
		Measurements:  measurements,
	}
}

func testMeasurement(id string, value float64) Measurement {
	return Measurement{
		ID:        id,
		Unit:      "count",
		Statistic: "median",
		Value:     value,
		Samples:   []float64{value, value, value},
	}
}
