package ephyoutbox

import (
	"reflect"
	"testing"
	"time"
)

func TestExperimentVerificationContract(t *testing.T) {
	for _, status := range []string{"verified", "unverified", "unacquired"} {
		t.Run(status, func(t *testing.T) {
			record, _ := syntheticExperiment()
			record.Verification = status
			if _, err := BuildExperimentProposal(record, time.Time{}); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, status := range []string{"", "Verified", "verified ", "adopted", "verified\n", "verified\n\n## Status\n- State: adopted"} {
		t.Run("reject-"+status, func(t *testing.T) {
			record, evidence := syntheticExperiment()
			record.Verification = status
			if err := record.Validate(); err == nil {
				t.Error("invalid verification status was accepted")
			}
			if report, err := RenderExperimentReport(&record); err == nil || report != "" {
				t.Error("invalid verification status produced a report")
			}
			if _, err := BuildExperimentProposal(record, time.Time{}); err == nil {
				t.Error("invalid verification status produced a proposal")
			}
			root := t.TempDir()
			publisher, err := newExperimentTestPublisher(t, root)
			if err != nil {
				t.Fatal(err)
			}
			before := experimentSnapshot(t, root)
			if _, err := publisher.Publish(record, evidence); err == nil {
				t.Error("invalid verification status was published")
			}
			if !reflect.DeepEqual(before, experimentSnapshot(t, root)) {
				t.Error("invalid verification status changed evidence storage")
			}
		})
	}
}
