package pipeline

import (
	"sort"
	"time"

	"github.com/gjwnssud/url-trace/internal/model"
)

// Aggregate normalizes every record's URL and merges duplicates into one record
// per canonical URL, summing observation counts and widening the first/last-seen
// span. Records whose URL fails normalization are dropped and counted in skipped
// so the caller can report what was left out — silence would read as full
// coverage when it is not. The result is sorted by URL for deterministic output.
func Aggregate(records []model.URLRecord) (aggregated []model.URLRecord, skipped int) {
	byURL := make(map[string]*model.URLRecord)

	for _, r := range records {
		normalized, ok := Normalize(r.URL)
		if !ok {
			skipped++
			continue
		}
		merge(byURL, normalized, r)
	}

	aggregated = make([]model.URLRecord, 0, len(byURL))
	for _, r := range byURL {
		aggregated = append(aggregated, *r)
	}
	sort.Slice(aggregated, func(i, j int) bool {
		return aggregated[i].URL < aggregated[j].URL
	})
	return aggregated, skipped
}

// merge folds one record into the accumulator keyed by its canonical URL.
func merge(byURL map[string]*model.URLRecord, normalized string, r model.URLRecord) {
	existing, found := byURL[normalized]
	if !found {
		r.URL = normalized
		stored := r
		byURL[normalized] = &stored
		return
	}
	existing.Count += r.Count
	existing.Sources = unionSources(existing.Sources, r.Sources)
	existing.FirstSeen = earlier(existing.FirstSeen, r.FirstSeen)
	existing.LastSeen = later(existing.LastSeen, r.LastSeen)
}

// unionSources merges two source lists without duplicates, sorted for stable
// output. Preserving every source matters: a URL seen independently by both the
// traffic capture and the browser is stronger whitelist evidence than either
// alone, and confidence scoring relies on that.
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

// earlier returns the earlier of two times, treating the zero time as "unknown"
// so a missing timestamp never wins over a real one.
func earlier(a, b time.Time) time.Time {
	switch {
	case a.IsZero():
		return b
	case b.IsZero():
		return a
	case b.Before(a):
		return b
	default:
		return a
	}
}

// later returns the later of two times, treating the zero time as "unknown".
func later(a, b time.Time) time.Time {
	switch {
	case a.IsZero():
		return b
	case b.IsZero():
		return a
	case b.After(a):
		return b
	default:
		return a
	}
}
