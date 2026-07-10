// Package output writes extraction results in the formats a policy review
// consumes.
package output

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/gjwnssud/url-trace/internal/model"
)

// Format enumerates the supported serialization formats.
type Format string

const (
	FormatJSON Format = "json"
	FormatCSV  Format = "csv"
)

// Write serializes the result to w in the requested format. CSV is flat and
// carries only the URL records; pattern suggestions appear in JSON output only.
func Write(w io.Writer, result model.Result, format Format) error {
	switch format {
	case FormatJSON:
		return writeJSON(w, result)
	case FormatCSV:
		return writeCSV(w, result.URLs)
	default:
		return fmt.Errorf("unsupported output format: %q", format)
	}
}

func writeJSON(w io.Writer, result model.Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

func writeCSV(w io.Writer, records []model.URLRecord) error {
	cw := csv.NewWriter(w)
	header := []string{"url", "sources", "party", "confidence", "first_seen", "last_seen", "count"}
	if err := cw.Write(header); err != nil {
		return err
	}
	for _, r := range records {
		row := []string{
			r.URL,
			strings.Join(r.Sources, ";"),
			r.Party,
			r.Confidence,
			formatTime(r.FirstSeen),
			formatTime(r.LastSeen),
			strconv.Itoa(r.Count),
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// formatTime renders a timestamp as RFC3339, or empty for an unknown (zero) time.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}
