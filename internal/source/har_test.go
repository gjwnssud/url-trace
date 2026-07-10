package source

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gjwnssud/url-trace/internal/model"
)

func TestHARSourceFetch(t *testing.T) {
	const harJSON = `{
	  "log": {
	    "entries": [
	      {"startedDateTime": "2026-01-01T10:00:00Z", "request": {"url": "https://api.example.com/v1/users"}},
	      {"startedDateTime": "2026-01-01T10:00:01Z", "request": {"url": "https://cdn.example.com/app.js"}},
	      {"startedDateTime": "", "request": {"url": ""}}
	    ]
	  }
	}`

	path := filepath.Join(t.TempDir(), "test.har")
	if err := os.WriteFile(path, []byte(harJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	records := drain(t, NewHARSource(path))

	if len(records) != 2 {
		t.Fatalf("got %d records, want 2 (the empty-URL entry is skipped)", len(records))
	}
	if records[0].URL != "https://api.example.com/v1/users" {
		t.Errorf("records[0].URL = %q", records[0].URL)
	}
	if len(records[0].Sources) != 1 || records[0].Sources[0] != "har" {
		t.Errorf("records[0].Sources = %v, want [har]", records[0].Sources)
	}
	if records[0].FirstSeen.IsZero() {
		t.Error("records[0].FirstSeen should be parsed from startedDateTime, got zero")
	}
}

func TestHARSourceMissingFile(t *testing.T) {
	out := make(chan model.URLRecord)
	go func() {
		for range out { // drain so Fetch never blocks on send
		}
	}()
	err := NewHARSource("/no/such/file.har").Fetch(context.Background(), out)
	close(out)
	if err == nil {
		t.Fatal("Fetch on a missing file should return an error")
	}
}

// drain runs a source to completion and returns everything it emits.
func drain(t *testing.T, s Source) []model.URLRecord {
	t.Helper()
	out := make(chan model.URLRecord)
	errc := make(chan error, 1)
	go func() {
		errc <- s.Fetch(context.Background(), out)
		close(out)
	}()

	var records []model.URLRecord
	for r := range out {
		records = append(records, r)
	}
	if err := <-errc; err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	return records
}
