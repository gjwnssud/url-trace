package patterns

import (
	"testing"

	"github.com/gjwnssud/url-trace/internal/model"
)

func rec(url string, count int) model.URLRecord {
	return model.URLRecord{URL: url, Count: count}
}

func TestSuggestNumericIDs(t *testing.T) {
	records := []model.URLRecord{
		rec("https://api.example.com/v1/users/101", 2),
		rec("https://api.example.com/v1/users/102", 1),
		rec("https://api.example.com/v1/users/103", 1),
	}

	got := Suggest(records)
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1: %+v", len(got), got)
	}
	s := got[0]
	if s.Pattern != "https://api.example.com/v1/users/*" {
		t.Errorf("Pattern = %q", s.Pattern)
	}
	if s.DistinctValues != 3 {
		t.Errorf("DistinctValues = %d, want 3", s.DistinctValues)
	}
	if s.TotalCount != 4 {
		t.Errorf("TotalCount = %d, want 4", s.TotalCount)
	}
}

func TestSuggestRequiresMinDistinct(t *testing.T) {
	records := []model.URLRecord{
		rec("https://api.example.com/v1/users/101", 5),
		rec("https://api.example.com/v1/users/102", 5),
	}
	if got := Suggest(records); len(got) != 0 {
		t.Errorf("two distinct IDs must not yet produce a suggestion, got %+v", got)
	}
}

func TestSuggestNeverGeneralizesWords(t *testing.T) {
	// Different word segments must never collapse into a wildcard — that would
	// turn the whitelist into a security hole.
	records := []model.URLRecord{
		rec("https://api.example.com/v1/users", 1),
		rec("https://api.example.com/v1/orders", 1),
		rec("https://api.example.com/v1/items", 1),
		// long word without digits must not count as a token either
		rec("https://api.example.com/v1/internationalization", 1),
	}
	if got := Suggest(records); len(got) != 0 {
		t.Errorf("word segments must never generalize, got %+v", got)
	}
}

func TestSuggestUUIDs(t *testing.T) {
	records := []model.URLRecord{
		rec("https://api.example.com/files/0f8fad5b-d9cb-469f-a165-70867728950e", 1),
		rec("https://api.example.com/files/7c9e6679-7425-40de-944b-e07fc1f90ae7", 1),
		rec("https://api.example.com/files/936da01f-9abd-4d9d-80c7-02af85c822a8", 1),
	}
	got := Suggest(records)
	if len(got) != 1 || got[0].Pattern != "https://api.example.com/files/*" {
		t.Fatalf("got %+v, want single files/* suggestion", got)
	}
	if len(got[0].Examples) != 3 {
		t.Errorf("len(Examples) = %d, want 3", len(got[0].Examples))
	}
}
