package policy

import (
	"strings"
	"testing"

	"github.com/gjwnssud/url-trace/internal/model"
)

func TestRuleMatchesExact(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		url     string
		want    bool
	}{
		{"identical", "https://a.com/x", "https://a.com/x", true},
		{"query ignored", "https://a.com/x", "https://a.com/x?q=1", true},
		{"root trailing slash", "https://a.com/", "https://a.com", true},
		{"different path", "https://a.com/x", "https://a.com/y", false},
		{"different host", "https://a.com/x", "https://b.com/x", false},
		{"pattern pins query", "https://a.com/x?q=1", "https://a.com/x?q=1", true},
		{"pinned query mismatch", "https://a.com/x?q=1", "https://a.com/x?q=2", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Rule{Pattern: tt.pattern}).Matches(tt.url); got != tt.want {
				t.Errorf("Matches(%q, %q) = %v, want %v", tt.pattern, tt.url, got, tt.want)
			}
		})
	}
}

func TestRuleMatchesWildcard(t *testing.T) {
	rule := Rule{Pattern: "https://api.a.com/v1/users/*"}
	tests := []struct {
		url  string
		want bool
	}{
		{"https://api.a.com/v1/users/123", true},
		{"https://api.a.com/v1/users/abc", true},
		// One segment only — a wildcard must never widen to deeper paths.
		{"https://api.a.com/v1/users/123/orders", false},
		{"https://api.a.com/v1/users", false},
		{"https://other.a.com/v1/users/123", false},
		{"http://api.a.com/v1/users/123", false},
	}
	for _, tt := range tests {
		if got := rule.Matches(tt.url); got != tt.want {
			t.Errorf("Matches(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func result(records ...model.URLRecord) model.Result {
	return model.Result{URLs: records}
}

func TestBuildFilters(t *testing.T) {
	res := result(
		model.URLRecord{URL: "https://a.com/keep", Party: model.PartyFirst, Confidence: model.ConfidenceHigh, Count: 5},
		model.URLRecord{URL: "https://a.com/low", Party: model.PartyFirst, Confidence: model.ConfidenceLow, Count: 1},
		model.URLRecord{URL: "https://cdn.other.com/x", Party: model.PartyThird, Confidence: model.ConfidenceHigh, Count: 5},
	)

	p, warnings := Build(res, BuildOptions{MinConfidence: model.ConfidenceMedium, Party: model.PartyFirst})
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
	if len(p.Rules) != 1 || p.Rules[0].Pattern != "https://a.com/keep" {
		t.Fatalf("rules = %+v, want only https://a.com/keep", p.Rules)
	}
	if p.Version != CurrentVersion {
		t.Errorf("version = %d, want %d", p.Version, CurrentVersion)
	}
}

func TestBuildAcceptPattern(t *testing.T) {
	res := result(
		model.URLRecord{URL: "https://a.com/users/1", Party: model.PartyFirst, Confidence: model.ConfidenceLow, Count: 2, Sources: []string{"har"}},
		model.URLRecord{URL: "https://a.com/users/2", Party: model.PartyFirst, Confidence: model.ConfidenceLow, Count: 3, Sources: []string{"browser"}},
		model.URLRecord{URL: "https://a.com/login", Party: model.PartyFirst, Confidence: model.ConfidenceLow, Count: 1, Sources: []string{"har"}},
	)

	p, warnings := Build(res, BuildOptions{AcceptPatterns: []string{"https://a.com/users/*"}})
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
	if len(p.Rules) != 2 {
		t.Fatalf("rules = %+v, want pattern rule + login", p.Rules)
	}

	// Sorted by pattern: /login < /users/*
	pattern := p.Rules[1]
	if pattern.Pattern != "https://a.com/users/*" {
		t.Fatalf("rules[1].Pattern = %q", pattern.Pattern)
	}
	if pattern.Count != 5 {
		t.Errorf("pattern.Count = %d, want 5 (folded evidence)", pattern.Count)
	}
	if len(pattern.Sources) != 2 {
		t.Errorf("pattern.Sources = %v, want union of har+browser", pattern.Sources)
	}
	// 5 observations + 2 sources → rescored high, not inherited from members.
	if pattern.Confidence != model.ConfidenceHigh {
		t.Errorf("pattern.Confidence = %q, want high", pattern.Confidence)
	}
}

func TestBuildWarnsOnDeadPattern(t *testing.T) {
	res := result(model.URLRecord{URL: "https://a.com/login", Confidence: model.ConfidenceLow, Count: 1})
	p, warnings := Build(res, BuildOptions{AcceptPatterns: []string{"https://a.com/ghosts/*"}})
	if len(warnings) != 1 || !strings.Contains(warnings[0], "ghosts") {
		t.Errorf("warnings = %v, want one mentioning the dead pattern", warnings)
	}
	// The human asked for it, so it is still emitted — just flagged.
	if len(p.Rules) != 2 {
		t.Errorf("rules = %+v, want dead pattern + login", p.Rules)
	}
}

func TestDiff(t *testing.T) {
	p := Policy{Version: CurrentVersion, Rules: []Rule{
		{Pattern: "https://a.com/users/*"},
		{Pattern: "https://a.com/login"},
		{Pattern: "https://a.com/retired"},
	}}
	res := result(
		model.URLRecord{URL: "https://a.com/users/42", Count: 1},
		model.URLRecord{URL: "https://a.com/login", Count: 1},
		model.URLRecord{URL: "https://a.com/brand-new", Count: 1},
	)

	report := Diff(p, res)

	if report.Checked != 3 || report.Covered != 2 {
		t.Errorf("checked/covered = %d/%d, want 3/2", report.Checked, report.Covered)
	}
	if len(report.NewURLs) != 1 || report.NewURLs[0].URL != "https://a.com/brand-new" {
		t.Errorf("NewURLs = %+v, want only brand-new", report.NewURLs)
	}
	if len(report.UnusedRules) != 1 || report.UnusedRules[0].Pattern != "https://a.com/retired" {
		t.Errorf("UnusedRules = %+v, want only retired", report.UnusedRules)
	}
}

func TestLoadRejectsWrongVersion(t *testing.T) {
	_, err := Load(strings.NewReader(`{"version": 99, "rules": []}`))
	if err == nil {
		t.Fatal("Load must reject an unsupported version")
	}
}
