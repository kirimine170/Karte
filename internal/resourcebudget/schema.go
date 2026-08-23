// Package resourcebudget defines the checked-in resource baseline，runtime
// measurement reports，and the strict comparison used by CI.
package resourcebudget

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"sort"
	"strings"
)

const SchemaVersion = 1

var schemaIdentifierPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)

type SamplingPolicy struct {
	Warmup       int    `json:"warmup"`
	Samples      int    `json:"samples"`
	Aggregation  string `json:"aggregation"`
	GOMAXPROCS   int    `json:"gomaxprocs"`
	LatencyClock string `json:"latencyClock"`
}

type Baseline struct {
	SchemaVersion int                  `json:"schemaVersion"`
	Suite         string               `json:"suite"`
	Policy        SamplingPolicy       `json:"policy"`
	Metrics       []BudgetMetric       `json:"metrics"`
	Profiles      []ObserveOnlyProfile `json:"observeOnlyProfiles"`
}

type BudgetMetric struct {
	ID          string  `json:"id"`
	Source      string  `json:"source"`
	Scenario    string  `json:"scenario"`
	Unit        string  `json:"unit"`
	Statistic   string  `json:"statistic"`
	Comparison  string  `json:"comparison"`
	Baseline    float64 `json:"baseline"`
	Limit       float64 `json:"limit"`
	Gate        bool    `json:"gate"`
	Required    bool    `json:"required"`
	Stability   string  `json:"stability"`
	Description string  `json:"description"`
}

type ObserveOnlyProfile struct {
	ID        string   `json:"id"`
	Scenario  string   `json:"scenario"`
	Metrics   []string `json:"metrics"`
	Reason    string   `json:"reason"`
	Procedure string   `json:"procedure"`
}

type Report struct {
	SchemaVersion int               `json:"schemaVersion"`
	Suite         string            `json:"suite"`
	Source        string            `json:"source"`
	Policy        SamplingPolicy    `json:"policy"`
	Environment   map[string]string `json:"environment,omitempty"`
	Measurements  []Measurement     `json:"measurements"`
}

type Measurement struct {
	ID        string    `json:"id"`
	Unit      string    `json:"unit"`
	Statistic string    `json:"statistic"`
	Value     float64   `json:"value"`
	Samples   []float64 `json:"samples"`
	Notes     string    `json:"notes,omitempty"`
}

func LoadBaseline(path string) (Baseline, error) {
	var baseline Baseline
	if err := decodeStrictFile(path, &baseline); err != nil {
		return Baseline{}, err
	}
	if err := ValidateBaseline(baseline); err != nil {
		return Baseline{}, fmt.Errorf("validate baseline %s: %w", path, err)
	}
	return baseline, nil
}

func LoadReport(path string) (Report, error) {
	var report Report
	if err := decodeStrictFile(path, &report); err != nil {
		return Report{}, err
	}
	if err := ValidateReport(report); err != nil {
		return Report{}, fmt.Errorf("validate report %s: %w", path, err)
	}
	return report, nil
}

func WriteReport(path string, report Report) error {
	if err := ValidateReport(report); err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o600)
}

func ValidateBaseline(baseline Baseline) error {
	var problems []string
	if baseline.SchemaVersion != SchemaVersion {
		problems = append(problems, fmt.Sprintf("schemaVersion=%d，want %d", baseline.SchemaVersion, SchemaVersion))
	}
	if !schemaIdentifierPattern.MatchString(baseline.Suite) {
		problems = append(problems, "suite must be a safe lowercase identifier")
	}
	problems = append(problems, validatePolicy(baseline.Policy)...)
	seen := make(map[string]bool, len(baseline.Metrics))
	for index, metric := range baseline.Metrics {
		prefix := fmt.Sprintf("metrics[%d]", index)
		if !schemaIdentifierPattern.MatchString(metric.ID) {
			problems = append(problems, prefix+".id must be a safe lowercase identifier")
		} else if seen[metric.ID] {
			problems = append(problems, "duplicate baseline metric "+metric.ID)
		}
		seen[metric.ID] = true
		if metric.Source != "backend" && metric.Source != "frontend" {
			problems = append(problems, prefix+".source must be backend or frontend")
		}
		if !schemaIdentifierPattern.MatchString(metric.Scenario) || !schemaIdentifierPattern.MatchString(metric.Unit) {
			problems = append(problems, prefix+" scenario and unit must be safe lowercase identifiers")
		}
		if metric.Statistic != "median" {
			problems = append(problems, prefix+".statistic must be median")
		}
		if metric.Comparison != "lte" && metric.Comparison != "gte" && metric.Comparison != "eq" && metric.Comparison != "observe" {
			problems = append(problems, prefix+".comparison must be lte，gte，eq，or observe")
		}
		if metric.Gate && metric.Comparison == "observe" {
			problems = append(problems, prefix+" observe-only metric cannot gate")
		}
		if metric.Gate && !metric.Required {
			problems = append(problems, prefix+" gating metric must be required")
		}
		if !isFinite(metric.Baseline) || !isFinite(metric.Limit) {
			problems = append(problems, prefix+" baseline and limit must be finite")
		}
		if metric.Stability == "" || metric.Description == "" {
			problems = append(problems, prefix+" stability and description are required")
		}
	}
	if len(baseline.Metrics) == 0 {
		problems = append(problems, "at least one metric is required")
	}
	profileIDs := make(map[string]bool, len(baseline.Profiles))
	for index, profile := range baseline.Profiles {
		prefix := fmt.Sprintf("observeOnlyProfiles[%d]", index)
		if !schemaIdentifierPattern.MatchString(profile.ID) || !schemaIdentifierPattern.MatchString(profile.Scenario) || len(profile.Metrics) == 0 || profile.Reason == "" || profile.Procedure == "" {
			problems = append(problems, prefix+" must define id，scenario，metrics，reason，and procedure")
		}
		for metricIndex, metric := range profile.Metrics {
			if !schemaIdentifierPattern.MatchString(metric) {
				problems = append(problems, fmt.Sprintf("%s.metrics[%d] must be a safe lowercase identifier", prefix, metricIndex))
			}
		}
		if profileIDs[profile.ID] {
			problems = append(problems, "duplicate observe-only profile "+profile.ID)
		}
		profileIDs[profile.ID] = true
	}
	return joinProblems(problems)
}

func ValidateReport(report Report) error {
	var problems []string
	if report.SchemaVersion != SchemaVersion {
		problems = append(problems, fmt.Sprintf("schemaVersion=%d，want %d", report.SchemaVersion, SchemaVersion))
	}
	if !schemaIdentifierPattern.MatchString(report.Suite) {
		problems = append(problems, "suite must be a safe lowercase identifier")
	}
	if report.Source != "backend" && report.Source != "frontend" {
		problems = append(problems, "source must be backend or frontend")
	}
	problems = append(problems, validatePolicy(report.Policy)...)
	seen := make(map[string]bool, len(report.Measurements))
	for index, measurement := range report.Measurements {
		prefix := fmt.Sprintf("measurements[%d]", index)
		if !schemaIdentifierPattern.MatchString(measurement.ID) || !schemaIdentifierPattern.MatchString(measurement.Unit) {
			problems = append(problems, prefix+" id and unit must be safe lowercase identifiers")
		}
		if measurement.Statistic != "median" {
			problems = append(problems, prefix+".statistic must be median")
		}
		if seen[measurement.ID] {
			problems = append(problems, "duplicate report metric "+measurement.ID)
		}
		seen[measurement.ID] = true
		if !isFinite(measurement.Value) {
			problems = append(problems, prefix+".value must be finite")
		}
		if len(measurement.Samples) != report.Policy.Samples {
			problems = append(problems, fmt.Sprintf("%s.samples has %d values，want policy.samples=%d", prefix, len(measurement.Samples), report.Policy.Samples))
		}
		for sampleIndex, sample := range measurement.Samples {
			if !isFinite(sample) {
				problems = append(problems, fmt.Sprintf("%s.samples[%d] must be finite", prefix, sampleIndex))
			}
		}
		if len(measurement.Samples) > 0 && isFinite(measurement.Value) && measurement.Value != Median(measurement.Samples) {
			problems = append(problems, prefix+".value must equal the median of samples")
		}
	}
	if len(report.Measurements) == 0 {
		problems = append(problems, "at least one measurement is required")
	}
	return joinProblems(problems)
}

func Median(samples []float64) float64 {
	if len(samples) == 0 {
		return 0
	}
	ordered := append([]float64(nil), samples...)
	sort.Float64s(ordered)
	middle := len(ordered) / 2
	if len(ordered)%2 == 1 {
		return ordered[middle]
	}
	return (ordered[middle-1] + ordered[middle]) / 2
}

func validatePolicy(policy SamplingPolicy) []string {
	var problems []string
	if policy.Warmup < 1 {
		problems = append(problems, "policy.warmup must be at least 1")
	}
	if policy.Samples < 3 || policy.Samples%2 == 0 {
		problems = append(problems, "policy.samples must be an odd number of at least 3")
	}
	if policy.Aggregation != "median" {
		problems = append(problems, "policy.aggregation must be median")
	}
	if policy.GOMAXPROCS < 1 {
		problems = append(problems, "policy.gomaxprocs must be at least 1")
	}
	if policy.LatencyClock != "monotonic" {
		problems = append(problems, "policy.latencyClock must be monotonic")
	}
	return problems
}

func decodeStrictFile(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("decode %s: trailing JSON value", path)
		}
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func joinProblems(problems []string) error {
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return errors.New(strings.Join(problems, "; "))
}
