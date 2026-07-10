// Package classify enriches aggregated URL records with the judgments a
// whitelist reviewer needs: whose domain each URL belongs to (first- vs
// third-party) and how much observed evidence backs it (confidence).
package classify

import (
	"net/url"
	"strings"

	"github.com/gjwnssud/url-trace/internal/model"
)

// Apply sets Party and Confidence on every record in place. primaryDomains are
// the application's own domains; a host equal to one of them or any subdomain
// counts as first-party. With no primary domains configured, party is marked
// unknown rather than guessed.
func Apply(records []model.URLRecord, primaryDomains []string) {
	for i := range records {
		records[i].Party = party(records[i].URL, primaryDomains)
		records[i].Confidence = confidence(records[i])
	}
}

func party(rawURL string, primaryDomains []string) string {
	if len(primaryDomains) == 0 {
		return model.PartyUnknown
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return model.PartyUnknown
	}
	host := u.Hostname()
	for _, domain := range primaryDomains {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return model.PartyFirst
		}
	}
	return model.PartyThird
}

func confidence(r model.URLRecord) string {
	return Confidence(r.Count, len(r.Sources))
}

// Confidence scores observed evidence. The scale is deliberately simple enough
// to explain in a policy review: repeated observation raises it, and being seen
// independently by two different sources (e.g. traffic capture AND browser)
// raises it one more level, because independent corroboration is stronger than
// repetition within one source. Exported so policy export can rescore rules
// that aggregate several observed URLs.
func Confidence(count, sourceCount int) string {
	level := 0
	switch {
	case count >= 5:
		level = 2
	case count >= 2:
		level = 1
	}
	if sourceCount >= 2 && level < 2 {
		level++
	}
	return [...]string{model.ConfidenceLow, model.ConfidenceMedium, model.ConfidenceHigh}[level]
}
