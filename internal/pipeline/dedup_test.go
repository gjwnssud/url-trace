package pipeline

import (
	"testing"
	"time"

	"github.com/gjwnssud/url-trace/internal/model"
)

func TestAggregate(t *testing.T) {
	early := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	late := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	records := []model.URLRecord{
		// Same endpoint, differing only by a volatile query param and seen at
		// different times — must collapse into one record spanning both.
		{URL: "https://example.com/a?_=1", Sources: []string{"har"}, FirstSeen: late, LastSeen: late, Count: 1},
		{URL: "https://example.com/a?_=2", Sources: []string{"har"}, FirstSeen: early, LastSeen: early, Count: 1},
		{URL: "https://example.com/b", Sources: []string{"har"}, FirstSeen: early, LastSeen: early, Count: 1},
		{URL: "not a url", Sources: []string{"har"}, Count: 1},
	}

	got, skipped := Aggregate(records)

	if skipped != 1 {
		t.Errorf("skipped = %d, want 1", skipped)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}

	// Results are sorted by URL, so /a precedes /b.
	a := got[0]
	if a.URL != "https://example.com/a" {
		t.Errorf("a.URL = %q, want https://example.com/a", a.URL)
	}
	if a.Count != 2 {
		t.Errorf("a.Count = %d, want 2 (volatile query collapsed both hits)", a.Count)
	}
	if !a.FirstSeen.Equal(early) {
		t.Errorf("a.FirstSeen = %v, want %v", a.FirstSeen, early)
	}
	if !a.LastSeen.Equal(late) {
		t.Errorf("a.LastSeen = %v, want %v", a.LastSeen, late)
	}
}

func TestAggregateUnionsSources(t *testing.T) {
	records := []model.URLRecord{
		{URL: "https://example.com/a", Sources: []string{"har"}, Count: 1},
		{URL: "https://example.com/a", Sources: []string{"browser"}, Count: 1},
		{URL: "https://example.com/a", Sources: []string{"har"}, Count: 1},
	}

	got, _ := Aggregate(records)
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	// Sorted union, no duplicates — independent observation across sources is
	// confidence evidence and must survive the merge.
	want := []string{"browser", "har"}
	if len(got[0].Sources) != 2 || got[0].Sources[0] != want[0] || got[0].Sources[1] != want[1] {
		t.Errorf("Sources = %v, want %v", got[0].Sources, want)
	}
}

func TestAggregateIgnoresZeroTime(t *testing.T) {
	known := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

	records := []model.URLRecord{
		{URL: "https://example.com/a", Sources: []string{"har"}, Count: 1}, // zero time
		{URL: "https://example.com/a", Sources: []string{"har"}, FirstSeen: known, LastSeen: known, Count: 1},
	}

	got, _ := Aggregate(records)
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if !got[0].FirstSeen.Equal(known) {
		t.Errorf("FirstSeen = %v, want %v — a real time must beat the zero time", got[0].FirstSeen, known)
	}
}
