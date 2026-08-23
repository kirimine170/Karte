package resourcebudget

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

type Evaluation struct {
	Pass       bool               `json:"pass"`
	Metrics    []MetricEvaluation `json:"metrics"`
	Violations []string           `json:"violations"`
}

type MetricEvaluation struct {
	ID           string   `json:"id"`
	Source       string   `json:"source"`
	Scenario     string   `json:"scenario"`
	Unit         string   `json:"unit"`
	Comparison   string   `json:"comparison"`
	Baseline     float64  `json:"baseline"`
	Limit        float64  `json:"limit"`
	Value        *float64 `json:"value,omitempty"`
	Delta        *float64 `json:"delta,omitempty"`
	DeltaPercent *float64 `json:"deltaPercent,omitempty"`
	Status       string   `json:"status"`
	Description  string   `json:"description"`
}

func Evaluate(baseline Baseline, reports ...Report) Evaluation {
	evaluation := Evaluation{Pass: true}
	if err := ValidateBaseline(baseline); err != nil {
		evaluation.Pass = false
		evaluation.Violations = append(evaluation.Violations, "invalid baseline: "+err.Error())
		return evaluation
	}

	budgetByID := make(map[string]BudgetMetric, len(baseline.Metrics))
	for _, budget := range baseline.Metrics {
		budgetByID[budget.ID] = budget
	}
	measurementByID := make(map[string]Measurement)
	for index, report := range reports {
		if err := ValidateReport(report); err != nil {
			evaluation.Violations = append(evaluation.Violations, fmt.Sprintf("invalid report %d: %v", index+1, err))
			continue
		}
		if report.Suite != baseline.Suite {
			evaluation.Violations = append(evaluation.Violations, fmt.Sprintf("report source %s suite=%q，want %q", report.Source, report.Suite, baseline.Suite))
		}
		if report.Policy != baseline.Policy {
			evaluation.Violations = append(evaluation.Violations, fmt.Sprintf("report source %s sampling policy differs from baseline", report.Source))
		}
		for _, measurement := range report.Measurements {
			budget, known := budgetByID[measurement.ID]
			if !known {
				evaluation.Violations = append(evaluation.Violations, "unknown metric "+measurement.ID)
				continue
			}
			if budget.Source != report.Source {
				evaluation.Violations = append(evaluation.Violations, fmt.Sprintf("metric %s came from %s，want %s", measurement.ID, report.Source, budget.Source))
			}
			if _, duplicate := measurementByID[measurement.ID]; duplicate {
				evaluation.Violations = append(evaluation.Violations, "duplicate measurement across reports "+measurement.ID)
				continue
			}
			measurementByID[measurement.ID] = measurement
		}
	}

	for _, budget := range baseline.Metrics {
		result := MetricEvaluation{
			ID:          budget.ID,
			Source:      budget.Source,
			Scenario:    budget.Scenario,
			Unit:        budget.Unit,
			Comparison:  budget.Comparison,
			Baseline:    budget.Baseline,
			Limit:       budget.Limit,
			Status:      "not-measured",
			Description: budget.Description,
		}
		measurement, measured := measurementByID[budget.ID]
		if !measured {
			if budget.Required {
				evaluation.Violations = append(evaluation.Violations, "missing required metric "+budget.ID)
				result.Status = "missing"
			}
			evaluation.Metrics = append(evaluation.Metrics, result)
			continue
		}
		value := measurement.Value
		result.Value = &value
		delta := value - budget.Baseline
		result.Delta = &delta
		if budget.Baseline != 0 {
			percent := delta / math.Abs(budget.Baseline) * 100
			result.DeltaPercent = &percent
		}
		validShape := true
		if measurement.Unit != budget.Unit {
			evaluation.Violations = append(evaluation.Violations, fmt.Sprintf("metric %s unit=%s，want %s", budget.ID, measurement.Unit, budget.Unit))
			validShape = false
		}
		if measurement.Statistic != budget.Statistic {
			evaluation.Violations = append(evaluation.Violations, fmt.Sprintf("metric %s statistic=%s，want %s", budget.ID, measurement.Statistic, budget.Statistic))
			validShape = false
		}
		if !validShape {
			result.Status = "invalid"
		} else if !budget.Gate || budget.Comparison == "observe" {
			result.Status = "observe"
		} else if comparisonPasses(budget.Comparison, value, budget.Limit) {
			result.Status = "pass"
		} else {
			result.Status = "fail"
			evaluation.Violations = append(evaluation.Violations, fmt.Sprintf("metric %s value=%s %s limit=%s", budget.ID, formatNumber(value), comparisonFailureWord(budget.Comparison), formatNumber(budget.Limit)))
		}
		evaluation.Metrics = append(evaluation.Metrics, result)
	}

	sort.Slice(evaluation.Metrics, func(i, j int) bool { return evaluation.Metrics[i].ID < evaluation.Metrics[j].ID })
	sort.Strings(evaluation.Violations)
	evaluation.Pass = len(evaluation.Violations) == 0
	return evaluation
}

func MarkdownSummary(baseline Baseline, evaluation Evaluation) string {
	var output strings.Builder
	output.WriteString("## Karte resource budget\n\n")
	if evaluation.Pass {
		output.WriteString("Result: PASS．\n\n")
	} else {
		output.WriteString("Result: FAIL．\n\n")
	}
	output.WriteString(fmt.Sprintf("Sampling: %d warmup，%d measured samples，median，GOMAXPROCS=%d．\n\n", baseline.Policy.Warmup, baseline.Policy.Samples, baseline.Policy.GOMAXPROCS))
	output.WriteString("| Metric | Scenario | Value | Baseline | Limit | Delta | Status |\n")
	output.WriteString("| --- | --- | ---: | ---: | ---: | ---: | --- |\n")
	for _, metric := range evaluation.Metrics {
		value := "—"
		delta := "—"
		if metric.Value != nil {
			value = formatNumber(*metric.Value) + " " + metric.Unit
		}
		if metric.Delta != nil {
			delta = signedNumber(*metric.Delta)
			if metric.DeltaPercent != nil {
				delta += " (" + signedPercent(*metric.DeltaPercent) + "%)"
			}
		}
		limit := "observe"
		if metric.Comparison != "observe" {
			limit = metric.Comparison + " " + formatNumber(metric.Limit)
		}
		output.WriteString(fmt.Sprintf("| `%s` | %s | %s | %s | %s | %s | %s |\n",
			metric.ID,
			metric.Scenario,
			value,
			formatNumber(metric.Baseline),
			limit,
			delta,
			metric.Status,
		))
	}
	if len(evaluation.Violations) > 0 {
		output.WriteString("\nViolations:\n\n")
		for _, violation := range evaluation.Violations {
			output.WriteString("- " + violation + "．\n")
		}
	}
	if len(baseline.Profiles) > 0 {
		output.WriteString("\nObserve-only profiles are intentionally excluded from the PR gate: ")
		ids := make([]string, len(baseline.Profiles))
		for index, profile := range baseline.Profiles {
			ids[index] = profile.ID
		}
		output.WriteString(strings.Join(ids, "，") + "．\n")
	}
	return output.String()
}

func comparisonPasses(comparison string, value, limit float64) bool {
	switch comparison {
	case "lte":
		return value <= limit
	case "gte":
		return value >= limit
	case "eq":
		return value == limit
	default:
		return true
	}
}

func comparisonFailureWord(comparison string) string {
	switch comparison {
	case "lte":
		return "exceeds"
	case "gte":
		return "is below"
	case "eq":
		return "differs from"
	default:
		return "violates"
	}
}

func formatNumber(value float64) string {
	formatted := strconv.FormatFloat(value, 'f', 6, 64)
	formatted = strings.TrimRight(formatted, "0")
	formatted = strings.TrimRight(formatted, ".")
	if formatted == "-0" || formatted == "" {
		return "0"
	}
	return formatted
}

func signedNumber(value float64) string {
	if value > 0 {
		return "+" + formatNumber(value)
	}
	return formatNumber(value)
}

func signedPercent(value float64) string {
	formatted := strconv.FormatFloat(value, 'f', 2, 64)
	formatted = strings.TrimRight(strings.TrimRight(formatted, "0"), ".")
	if formatted == "-0" || formatted == "" {
		return "0"
	}
	if value > 0 {
		return "+" + formatted
	}
	return formatted
}
