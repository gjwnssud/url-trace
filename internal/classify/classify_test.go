package classify

import (
	"testing"

	"github.com/gjwnssud/url-trace/internal/model"
)

func TestApplyParty(t *testing.T) {
	records := []model.URLRecord{
		{URL: "https://example.com/a"},
		{URL: "https://cdn.example.com/app.js"},   // subdomain → first-party
		{URL: "https://fonts.googleapis.com/css"}, // → third-party
		{URL: "https://evilexample.com/x"},        // suffix trick must NOT match
	}

	Apply(records, []string{"example.com"})

	want := []string{model.PartyFirst, model.PartyFirst, model.PartyThird, model.PartyThird}
	for i, r := range records {
		if r.Party != want[i] {
			t.Errorf("records[%d] (%s) party = %q, want %q", i, r.URL, r.Party, want[i])
		}
	}
}

func TestApplyPartyUnknownWithoutDomains(t *testing.T) {
	records := []model.URLRecord{{URL: "https://example.com/a"}}
	Apply(records, nil)
	if records[0].Party != model.PartyUnknown {
		t.Errorf("party = %q, want %q when no primary domains configured", records[0].Party, model.PartyUnknown)
	}
}

func TestApplyConfidence(t *testing.T) {
	tests := []struct {
		name string
		rec  model.URLRecord
		want string
	}{
		{"single observation", model.URLRecord{Count: 1, Sources: []string{"har"}}, model.ConfidenceLow},
		{"repeated observation", model.URLRecord{Count: 3, Sources: []string{"har"}}, model.ConfidenceMedium},
		{"heavy observation", model.URLRecord{Count: 5, Sources: []string{"har"}}, model.ConfidenceHigh},
		{"corroborated once", model.URLRecord{Count: 2, Sources: []string{"browser", "har"}}, model.ConfidenceHigh},
		{"corroborated single", model.URLRecord{Count: 1, Sources: []string{"browser", "har"}}, model.ConfidenceMedium},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.rec.URL = "https://example.com/a"
			records := []model.URLRecord{tt.rec}
			Apply(records, []string{"example.com"})
			if records[0].Confidence != tt.want {
				t.Errorf("confidence = %q, want %q", records[0].Confidence, tt.want)
			}
		})
	}
}
