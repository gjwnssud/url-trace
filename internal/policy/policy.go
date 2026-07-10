// Package policy turns extraction results into durable whitelist policies and
// compares later observations against them.
//
// The review workflow it supports: extract produces observed URLs plus wildcard
// suggestions; a human approves specific patterns; Build collapses the URLs
// those patterns cover into single rules and keeps everything else as exact
// rules; Diff later reports which newly observed URLs the policy does not cover
// (candidates to add) and which rules matched nothing (candidates to retire).
package policy

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gjwnssud/url-trace/internal/classify"
	"github.com/gjwnssud/url-trace/internal/model"
)

// CurrentVersion is the policy file schema version this build writes.
const CurrentVersion = 1

// Rule is one whitelist entry. Pattern is either an exact URL or contains "*"
// path segments, each matching exactly one segment — never more, so an approved
// wildcard cannot silently widen. The remaining fields carry the observation
// evidence forward so a later review can still see why the rule exists.
type Rule struct {
	Pattern    string    `json:"pattern"`
	Party      string    `json:"party,omitempty"`
	Confidence string    `json:"confidence,omitempty"`
	Sources    []string  `json:"sources,omitempty"`
	Count      int       `json:"count,omitempty"`
	FirstSeen  time.Time `json:"firstSeen,omitzero"`
	LastSeen   time.Time `json:"lastSeen,omitzero"`
}

// Policy is a versioned set of whitelist rules.
type Policy struct {
	Version int    `json:"version"`
	Rules   []Rule `json:"rules"`
}

// Matches reports whether rawURL is covered by this rule. Exact rules compare
// scheme://host/path, ignoring the query — query strings are request detail,
// not whitelist identity — unless the pattern itself pins one with "?".
// Wildcard rules additionally require equal segment counts.
func (r Rule) Matches(rawURL string) bool {
	if !strings.Contains(r.Pattern, "*") {
		return matchesExact(r.Pattern, rawURL)
	}
	return matchesWildcard(r.Pattern, rawURL)
}

func matchesExact(pattern, rawURL string) bool {
	if strings.Contains(pattern, "?") {
		return pattern == rawURL
	}
	pu, err := url.Parse(pattern)
	if err != nil {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return schemeHostPath(pu) == schemeHostPath(u)
}

func matchesWildcard(pattern, rawURL string) bool {
	pu, err := url.Parse(pattern)
	if err != nil {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if pu.Scheme != u.Scheme || pu.Host != u.Host {
		return false
	}
	patternSegs := splitPath(pu.Path)
	urlSegs := splitPath(u.Path)
	if len(patternSegs) != len(urlSegs) {
		return false
	}
	for i := range patternSegs {
		if patternSegs[i] != "*" && patternSegs[i] != urlSegs[i] {
			return false
		}
	}
	return true
}

// schemeHostPath canonicalizes for exact comparison so a missing trailing slash
// on the root path never causes a false mismatch.
func schemeHostPath(u *url.URL) string {
	path := u.Path
	if path == "" {
		path = "/"
	}
	return u.Scheme + "://" + u.Host + path
}

func splitPath(p string) []string {
	return strings.Split(strings.Trim(p, "/"), "/")
}

// BuildOptions filters and shapes the generated policy.
type BuildOptions struct {
	// MinConfidence drops records below this confidence ("" or "low" keeps all).
	MinConfidence string
	// Party keeps only records of this party ("" keeps all).
	Party string
	// AcceptPatterns are human-approved wildcard patterns; URLs they cover are
	// collapsed into one rule each.
	AcceptPatterns []string
}

// Build converts an extraction result into a policy. Approval stays explicit:
// only patterns listed in opts.AcceptPatterns become wildcard rules; suggestions
// in the result are never applied on their own. An accepted pattern that covers
// no observed URL is still emitted (the human asked for it) but reported in
// warnings so a typo does not silently become a dead rule.
func Build(result model.Result, opts BuildOptions) (Policy, []string) {
	minLevel := model.ConfidenceLevel(opts.MinConfidence)
	var filtered []model.URLRecord
	for _, r := range result.URLs {
		if model.ConfidenceLevel(r.Confidence) < minLevel {
			continue
		}
		if opts.Party != "" && r.Party != opts.Party {
			continue
		}
		filtered = append(filtered, r)
	}

	var warnings []string
	rules := make([]Rule, 0, len(filtered))
	covered := make([]bool, len(filtered))

	for _, pattern := range opts.AcceptPatterns {
		rule := Rule{Pattern: pattern}
		matched := false
		for i, r := range filtered {
			if covered[i] || !rule.Matches(r.URL) {
				continue
			}
			covered[i] = true
			matched = true
			foldRecord(&rule, r)
		}
		if !matched {
			warnings = append(warnings, fmt.Sprintf("accepted pattern %q matches no observed URL", pattern))
		}
		rule.Confidence = classify.Confidence(rule.Count, len(rule.Sources))
		rules = append(rules, rule)
	}

	for i, r := range filtered {
		if covered[i] {
			continue
		}
		rules = append(rules, Rule{
			Pattern:    r.URL,
			Party:      r.Party,
			Confidence: r.Confidence,
			Sources:    r.Sources,
			Count:      r.Count,
			FirstSeen:  r.FirstSeen,
			LastSeen:   r.LastSeen,
		})
	}

	sort.Slice(rules, func(i, j int) bool { return rules[i].Pattern < rules[j].Pattern })
	return Policy{Version: CurrentVersion, Rules: rules}, warnings
}

// foldRecord accumulates one covered URL's evidence into a wildcard rule. All
// covered URLs share the rule's host, so party carries over directly.
func foldRecord(rule *Rule, r model.URLRecord) {
	rule.Party = r.Party
	rule.Count += r.Count
	rule.Sources = unionSources(rule.Sources, r.Sources)
	if rule.FirstSeen.IsZero() || (!r.FirstSeen.IsZero() && r.FirstSeen.Before(rule.FirstSeen)) {
		rule.FirstSeen = r.FirstSeen
	}
	if r.LastSeen.After(rule.LastSeen) {
		rule.LastSeen = r.LastSeen
	}
}

func unionSources(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	var merged []string
	for _, s := range append(append([]string{}, a...), b...) {
		if !seen[s] {
			seen[s] = true
			merged = append(merged, s)
		}
	}
	sort.Strings(merged)
	return merged
}

// Report is the outcome of checking an extraction result against a policy.
type Report struct {
	// NewURLs were observed but no rule covers them — the actionable additions.
	NewURLs []model.URLRecord `json:"newUrls"`
	// UnusedRules matched nothing in this run — candidates for retirement,
	// though absence in one capture is not proof the endpoint is gone.
	UnusedRules []Rule `json:"unusedRules"`
	Checked     int    `json:"checked"`
	Covered     int    `json:"covered"`
}

// Diff checks every observed URL against the policy. A URL may be covered by
// several rules; all of them count as used.
func Diff(p Policy, result model.Result) Report {
	used := make([]bool, len(p.Rules))
	report := Report{
		NewURLs:     make([]model.URLRecord, 0),
		UnusedRules: make([]Rule, 0),
	}

	for _, rec := range result.URLs {
		report.Checked++
		matched := false
		for i, rule := range p.Rules {
			if rule.Matches(rec.URL) {
				used[i] = true
				matched = true
			}
		}
		if matched {
			report.Covered++
		} else {
			report.NewURLs = append(report.NewURLs, rec)
		}
	}

	for i, rule := range p.Rules {
		if !used[i] {
			report.UnusedRules = append(report.UnusedRules, rule)
		}
	}
	return report
}

// Load reads and validates a policy file.
func Load(r io.Reader) (Policy, error) {
	var p Policy
	if err := json.NewDecoder(r).Decode(&p); err != nil {
		return Policy{}, fmt.Errorf("parse policy: %w", err)
	}
	if p.Version != CurrentVersion {
		return Policy{}, fmt.Errorf("unsupported policy version %d (this build supports %d)", p.Version, CurrentVersion)
	}
	return p, nil
}
