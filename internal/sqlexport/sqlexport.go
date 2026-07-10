// Package sqlexport renders a whitelist policy as SQL INSERT statements using a
// user-supplied table and column mapping. The target schema lives entirely in
// the config file, never in this codebase, so the tool stays generic while any
// concrete system's table stays private to its operator.
package sqlexport

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gjwnssud/url-trace/internal/policy"
)

// Column maps one table column to a value template. Value is a literal string
// in which {placeholder} tokens are substituted per rule; a plain literal (no
// tokens) becomes a fixed value for every row.
type Column struct {
	Name string `json:"name"`
	// Value template. Available placeholders:
	//   {pattern}      the rule pattern (URL, possibly with * segments)
	//   {host} {path}  parsed from the pattern
	//   {id}           sha256 hex of the pattern — deterministic row key, so
	//                  re-exporting the same policy yields the same IDs
	//   {party} {confidence} {count} {sources}   rule evidence
	//   {nowMs} {firstSeenMs} {lastSeenMs}       epoch-millis timestamps
	Value string `json:"value"`
	// Type "number" emits the substituted value unquoted; default is a quoted,
	// escaped string.
	Type string `json:"type"`
	// MaxLength truncates longer values (0 = unlimited). Truncation is reported
	// as a warning except for pure "{id}" columns, where a hash prefix is still
	// a valid deterministic key.
	MaxLength int `json:"maxLength"`
}

// Config is the full table mapping.
type Config struct {
	Table   string   `json:"table"`
	Columns []Column `json:"columns"`
}

var placeholderPattern = regexp.MustCompile(`\{([a-zA-Z]+)\}`)

var knownPlaceholders = map[string]bool{
	"pattern": true, "host": true, "path": true, "id": true,
	"party": true, "confidence": true, "count": true, "sources": true,
	"nowMs": true, "firstSeenMs": true, "lastSeenMs": true,
}

// LoadConfig parses and validates a mapping config. Unknown placeholders are
// rejected here rather than at write time, so a typo fails fast instead of
// silently emitting the literal token into every row.
func LoadConfig(r io.Reader) (Config, error) {
	var cfg Config
	if err := json.NewDecoder(r).Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse sql export config: %w", err)
	}
	if cfg.Table == "" {
		return Config{}, fmt.Errorf("sql export config: table is required")
	}
	if len(cfg.Columns) == 0 {
		return Config{}, fmt.Errorf("sql export config: at least one column is required")
	}
	for i, col := range cfg.Columns {
		if col.Name == "" {
			return Config{}, fmt.Errorf("sql export config: columns[%d].name is required", i)
		}
		if col.Type != "" && col.Type != "string" && col.Type != "number" {
			return Config{}, fmt.Errorf("sql export config: column %s: type must be string or number, got %q", col.Name, col.Type)
		}
		for _, m := range placeholderPattern.FindAllStringSubmatch(col.Value, -1) {
			if !knownPlaceholders[m[1]] {
				return Config{}, fmt.Errorf("sql export config: column %s: unknown placeholder {%s} (known: %s)",
					col.Name, m[1], strings.Join(placeholderNames(), ", "))
			}
		}
	}
	return cfg, nil
}

func placeholderNames() []string {
	names := make([]string, 0, len(knownPlaceholders))
	for name := range knownPlaceholders {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// WriteSQL emits one INSERT statement per policy rule using the configured
// mapping. Values exceeding a column's MaxLength are truncated and reported in
// warnings — a silently truncated whitelist URL would match nothing or, worse,
// something else.
func WriteSQL(w io.Writer, p policy.Policy, cfg Config, now time.Time) ([]string, error) {
	var warnings []string

	names := make([]string, len(cfg.Columns))
	for i, col := range cfg.Columns {
		names[i] = col.Name
	}
	columnList := strings.Join(names, ", ")

	for _, rule := range p.Rules {
		subs := substitutions(rule, now)
		values := make([]string, len(cfg.Columns))
		for i, col := range cfg.Columns {
			rendered, warn, err := renderColumn(col, subs)
			if err != nil {
				return warnings, err
			}
			warnings = append(warnings, warn...)
			values[i] = rendered
		}
		stmt := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s);\n", cfg.Table, columnList, strings.Join(values, ", "))
		if _, err := io.WriteString(w, stmt); err != nil {
			return warnings, err
		}
	}
	return warnings, nil
}

func renderColumn(col Column, subs map[string]string) (string, []string, error) {
	value := placeholderPattern.ReplaceAllStringFunc(col.Value, func(token string) string {
		return subs[strings.Trim(token, "{}")]
	})

	var warnings []string
	if col.MaxLength > 0 && len(value) > col.MaxLength {
		// A hash prefix is still a valid deterministic key; anything else
		// losing characters deserves a warning.
		if col.Value != "{id}" {
			warnings = append(warnings, fmt.Sprintf("%s truncated to %d chars: %s", col.Name, col.MaxLength, value))
		}
		value = value[:col.MaxLength]
	}

	if col.Type == "number" {
		if !numericValue.MatchString(value) {
			return "", warnings, fmt.Errorf("column %s: type is number but value is %q", col.Name, value)
		}
		return value, warnings, nil
	}
	return "'" + escape(value) + "'", warnings, nil
}

var numericValue = regexp.MustCompile(`^-?[0-9]+$`)

func substitutions(rule policy.Rule, now time.Time) map[string]string {
	host, path := hostPath(rule.Pattern)
	return map[string]string{
		"pattern":     rule.Pattern,
		"host":        host,
		"path":        path,
		"id":          recordID(rule.Pattern),
		"party":       rule.Party,
		"confidence":  rule.Confidence,
		"count":       strconv.Itoa(rule.Count),
		"sources":     strings.Join(rule.Sources, ";"),
		"nowMs":       strconv.FormatInt(now.UnixMilli(), 10),
		"firstSeenMs": epochMillis(rule.FirstSeen),
		"lastSeenMs":  epochMillis(rule.LastSeen),
	}
}

// recordID derives a deterministic row key from the pattern; truncate it via
// MaxLength to fit the target column.
func recordID(pattern string) string {
	sum := sha256.Sum256([]byte(pattern))
	return hex.EncodeToString(sum[:])
}

func hostPath(pattern string) (host, path string) {
	u, err := url.Parse(pattern)
	if err != nil {
		return "", "/"
	}
	path = u.Path
	if path == "" {
		path = "/"
	}
	return u.Host, path
}

// epochMillis renders a timestamp, or "0" for an unknown (zero) time.
func epochMillis(t time.Time) string {
	if t.IsZero() {
		return "0"
	}
	return strconv.FormatInt(t.UnixMilli(), 10)
}

// escape neutralizes the two characters MySQL treats specially inside a
// single-quoted string literal.
func escape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `'`, `''`)
}
