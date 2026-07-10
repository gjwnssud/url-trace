// Package model defines the core data types shared across url-trace.
package model

import "time"

// Party classifies whether a URL belongs to the application's own domains or to
// an external service. This drives whitelist review: first-party entries are
// near-automatic approvals while third-party ones need scrutiny.
const (
	PartyFirst   = "first-party"
	PartyThird   = "third-party"
	PartyUnknown = "unknown" // no primary domains configured to compare against
)

// Confidence expresses how much observed evidence backs a record, so a reviewer
// can tell a constantly used endpoint from a single stray observation.
const (
	ConfidenceLow    = "low"
	ConfidenceMedium = "medium"
	ConfidenceHigh   = "high"
)

// ConfidenceLevel orders confidence values for threshold comparisons
// (low < medium < high). Unknown strings rank lowest.
func ConfidenceLevel(c string) int {
	switch c {
	case ConfidenceHigh:
		return 2
	case ConfidenceMedium:
		return 1
	default:
		return 0
	}
}

// URLRecord is a single observed URL together with the audit metadata a
// whitelist policy review depends on: where it came from, how often/when it was
// seen, whose domain it is, and how trustworthy the observation is. Aggregation
// collapses duplicates into one record; Sources keeps every collector that saw
// it because independent observation across sources is itself evidence.
type URLRecord struct {
	URL        string    `json:"url"`
	Sources    []string  `json:"sources"`
	FirstSeen  time.Time `json:"firstSeen"`
	LastSeen   time.Time `json:"lastSeen"`
	Count      int       `json:"count"`
	Party      string    `json:"party,omitempty"`
	Confidence string    `json:"confidence,omitempty"`
}

// PatternSuggestion proposes collapsing many observed URLs into one wildcard
// whitelist rule (e.g. /api/users/123, /api/users/456 → /api/users/*). It is
// only ever a proposal for a human to approve — over-generalizing a whitelist
// is a security hole, so url-trace never applies patterns on its own.
type PatternSuggestion struct {
	Pattern        string   `json:"pattern"`
	DistinctValues int      `json:"distinctValues"` // distinct values seen at the wildcard position
	TotalCount     int      `json:"totalCount"`     // observations across all matching URLs
	Examples       []string `json:"examples"`
}

// Result is the complete output of an extract run.
type Result struct {
	URLs               []URLRecord         `json:"urls"`
	PatternSuggestions []PatternSuggestion `json:"patternSuggestions"`
}
