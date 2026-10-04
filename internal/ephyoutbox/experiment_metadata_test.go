package ephyoutbox

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func experimentMetadataField(record *ExperimentRecord, field string) *string {
	switch field {
	case "experiment_id":
		return &record.ExperimentID
	case "run_id":
		return &record.RunID
	case "attempt_id":
		return &record.AttemptID
	case "environment":
		return &record.Environment
	case "model":
		return &record.Model
	case "checker":
		return &record.Checker
	default:
		panic("unknown synthetic metadata field")
	}
}

func TestExperimentMetadataRejectsMalformedValues(t *testing.T) {
	for _, field := range []string{"experiment_id", "run_id", "attempt_id", "environment", "model", "checker"} {
		invalid := map[string]string{
			"empty": "", "newline": "fixture\n", "carriage-return": "fixture\r",
			"status-injection": "fixture\n\n## Status\n- State: adopted",
		}
		if strings.HasSuffix(field, "_id") {
			invalid["leading-punctuation"] = ".fixture"
			invalid["space"] = "fixture one"
			invalid["non-ascii"] = "fixture-日本語"
			invalid["over-length"] = strings.Repeat("a", 129)
		} else {
			invalid["over-length"] = strings.Repeat("a", 257)
		}
		for scenario, value := range invalid {
			t.Run(field+"/"+scenario, func(t *testing.T) {
				record, evidence := syntheticExperiment()
				record.Verification = "unverified"
				*experimentMetadataField(&record, field) = value
				if err := record.Validate(); err == nil {
					t.Error("malformed metadata was accepted")
				}
				if report, err := RenderExperimentReport(&record); err == nil || report != "" {
					t.Error("malformed metadata produced a report")
				}
				if _, err := BuildExperimentProposal(record, time.Time{}); err == nil {
					t.Error("malformed metadata produced a proposal")
				}
				root := t.TempDir()
				publisher, err := newExperimentTestPublisher(t, root)
				if err != nil {
					t.Fatal(err)
				}
				before := experimentSnapshot(t, root)
				if _, err := publisher.Publish(record, evidence); err == nil {
					t.Error("malformed metadata was published")
				}
				if !reflect.DeepEqual(before, experimentSnapshot(t, root)) {
					t.Error("malformed metadata changed storage")
				}
			})
		}
	}
}

func TestExperimentMetadataContractBoundaries(t *testing.T) {
	for _, field := range []string{"experiment_id", "run_id", "attempt_id", "environment", "model", "checker"} {
		values := []string{"unacquired", "runner"}
		if strings.HasSuffix(field, "_id") {
			values = append(values, "A.b_c:/-09", strings.Repeat("a", 128))
		} else {
			values = append(values, "Unicode 日本語: model/v1", strings.Repeat("あ", 256))
		}
		for index, value := range values {
			t.Run(field+"/"+string(rune('0'+index)), func(t *testing.T) {
				record, _ := syntheticExperiment()
				*experimentMetadataField(&record, field) = value
				if _, err := BuildExperimentProposal(record, time.Time{}); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestExperimentFreeTextRetainsNewlines(t *testing.T) {
	record, _ := syntheticExperiment()
	record.Observations = []string{"fact\nsecond line"}
	record.Interpretation = "interpretation\nsecond line"
	record.HaltReason = "reason\nsecond line"
	report, err := RenderExperimentReport(&record)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{record.Observations[0], record.Interpretation, record.HaltReason} {
		if !strings.Contains(report, text) {
			t.Errorf("free text changed: %q", text)
		}
	}
}
