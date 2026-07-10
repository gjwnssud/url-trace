package source

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/gjwnssud/url-trace/internal/model"
)

// HARSource extracts request URLs from an HTTP Archive (HAR) file — a capture of
// the traffic an application actually made. This is the most reliable whitelist
// input because it reflects real usage rather than a crawler's guess.
type HARSource struct {
	Path string
}

// NewHARSource creates a source that reads URLs from the given HAR file.
func NewHARSource(path string) *HARSource {
	return &HARSource{Path: path}
}

// Name identifies this source in audit metadata.
func (s *HARSource) Name() string { return "har" }

// harFile mirrors only the subset of the HAR schema we consume.
type harFile struct {
	Log struct {
		Entries []struct {
			StartedDateTime string `json:"startedDateTime"`
			Request         struct {
				URL string `json:"url"`
			} `json:"request"`
		} `json:"entries"`
	} `json:"log"`
}

// Fetch reads and parses the HAR file, emitting one record per request entry.
func (s *HARSource) Fetch(ctx context.Context, out chan<- model.URLRecord) error {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return fmt.Errorf("read har file: %w", err)
	}

	var har harFile
	if err := json.Unmarshal(data, &har); err != nil {
		return fmt.Errorf("parse har file: %w", err)
	}

	for _, entry := range har.Log.Entries {
		if entry.Request.URL == "" {
			continue
		}
		seen := parseHARTime(entry.StartedDateTime)
		record := model.URLRecord{
			URL:       entry.Request.URL,
			Sources:   []string{s.Name()},
			FirstSeen: seen,
			LastSeen:  seen,
			Count:     1,
		}
		select {
		case out <- record:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// parseHARTime tolerates a missing or malformed timestamp by returning the zero
// time, so a URL is never dropped just because its capture time is unusable.
func parseHARTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
