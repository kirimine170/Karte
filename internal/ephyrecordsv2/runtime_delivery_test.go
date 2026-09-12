package ephyrecordsv2

import (
	"encoding/json"
	"os"
	"testing"
)

// The Runtime and Karte consume the same synthetic outcome cases．This adds
// evidence for the existing v2 enums without enabling any real producer grant．
func TestRuntimeDeliverySharedContract(t *testing.T) {
	raw, err := os.ReadFile("../../schemas/karte-ephy/v2/fixtures/runtime-delivery.scenario.json")
	must(t, err)
	var fixture struct {
		Cases []struct {
			Name     string `json:"name"`
			Snapshot struct {
				ResponsePlan struct {
					Text string `json:"text"`
				} `json:"response_plan"`
			} `json:"snapshot"`
			Assistant Assistant `json:"expected_assistant"`
		} `json:"cases"`
	}
	must(t, json.Unmarshal(raw, &fixture))
	for _, c := range fixture.Cases {
		t.Run(c.Name, func(t *testing.T) {
			s, _ := newFixture(t)
			p := stableProposal(1)
			p.Events[0].Type = "assistant_result"
			p.Events[0].InputKind = ""
			p.Events[0].ASR = nil
			p.Events[0].Text = c.Snapshot.ResponsePlan.Text
			p.Events[0].Assistant = &c.Assistant
			receipt := apply(t, s, p)
			_, _, record := state(t, s, receipt.Applied.DocID)
			got, _ := json.Marshal(record.Events[0].Assistant)
			want, _ := json.Marshal(c.Assistant)
			if string(got) != string(want) {
				t.Fatal("assistant playback facts changed during adoption")
			}
		})
	}
}
