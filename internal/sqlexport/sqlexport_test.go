package sqlexport

import (
	"strings"
	"testing"
	"time"

	"github.com/gjwnssud/url-trace/internal/policy"
)

const validConfig = `{
  "table": "URL_ALLOWLIST",
  "columns": [
    {"name": "ID", "value": "{id}", "maxLength": 24},
    {"name": "URL_PATTERN", "value": "{pattern}", "maxLength": 1000},
    {"name": "LABEL", "value": "{host}{path}", "maxLength": 100},
    {"name": "GROUP_CODE", "value": "GRP-01"},
    {"name": "HIT_COUNT", "value": "{count}", "type": "number"},
    {"name": "REGISTERED_MS", "value": "{nowMs}", "type": "number"}
  ]
}`

func loadValid(t *testing.T) Config {
	t.Helper()
	cfg, err := LoadConfig(strings.NewReader(validConfig))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestLoadConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr string
	}{
		{"missing table", `{"columns":[{"name":"A","value":"x"}]}`, "table is required"},
		{"no columns", `{"table":"T"}`, "at least one column"},
		{"missing column name", `{"table":"T","columns":[{"value":"x"}]}`, "name is required"},
		{"bad type", `{"table":"T","columns":[{"name":"A","value":"x","type":"float"}]}`, "must be string or number"},
		{"unknown placeholder", `{"table":"T","columns":[{"name":"A","value":"{nope}"}]}`, "unknown placeholder {nope}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadConfig(strings.NewReader(tt.json))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestWriteSQL(t *testing.T) {
	cfg := loadValid(t)
	p := policy.Policy{Version: 1, Rules: []policy.Rule{
		{Pattern: "https://api.example.com/v1/users/*", Count: 4},
		{Pattern: "https://a.com/it's?q=a\\b", Count: 1}, // quote + backslash escaping
	}}

	var sb strings.Builder
	warnings, err := WriteSQL(&sb, p, cfg, time.UnixMilli(1751900000000))
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}

	lines := strings.Split(strings.TrimSpace(sb.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d statements, want 2:\n%s", len(lines), sb.String())
	}
	first := lines[0]
	if !strings.HasPrefix(first, "INSERT INTO URL_ALLOWLIST (ID, URL_PATTERN, LABEL, GROUP_CODE, HIT_COUNT, REGISTERED_MS) VALUES (") {
		t.Errorf("unexpected statement prefix: %s", first)
	}
	for _, want := range []string{
		"'https://api.example.com/v1/users/*'",
		"'api.example.com/v1/users/*'", // {host}{path}
		"'GRP-01'",                     // static literal
		" 4, 1751900000000);",          // numbers unquoted
	} {
		if !strings.Contains(first, want) {
			t.Errorf("statement missing %q: %s", want, first)
		}
	}
	if !strings.Contains(lines[1], `it''s`) || !strings.Contains(lines[1], `a\\b`) {
		t.Errorf("quote/backslash not escaped: %s", lines[1])
	}
}

func TestWriteSQLDeterministicTruncatedID(t *testing.T) {
	cfg := loadValid(t)
	p := policy.Policy{Version: 1, Rules: []policy.Rule{{Pattern: "https://a.com/x"}}}

	var first, second strings.Builder
	if _, err := WriteSQL(&first, p, cfg, time.UnixMilli(0)); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteSQL(&second, p, cfg, time.UnixMilli(0)); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() {
		t.Error("re-export must be deterministic")
	}
	// {id} truncation to maxLength is silent — a hash prefix is still a key.
	id := strings.SplitN(strings.SplitN(first.String(), "('", 2)[1], "'", 2)[0]
	if len(id) != 24 {
		t.Errorf("id length = %d, want 24 (maxLength)", len(id))
	}
}

func TestWriteSQLTruncationWarns(t *testing.T) {
	cfg := loadValid(t)
	long := "https://a.com/" + strings.Repeat("x", 1100)
	p := policy.Policy{Version: 1, Rules: []policy.Rule{{Pattern: long}}}

	var sb strings.Builder
	warnings, err := WriteSQL(&sb, p, cfg, time.UnixMilli(0))
	if err != nil {
		t.Fatal(err)
	}
	// URL_PATTERN(1000) and LABEL(100) overflow and warn; ID truncates silently.
	if len(warnings) != 2 {
		t.Errorf("warnings = %d, want 2: %v", len(warnings), warnings)
	}
}

func TestWriteSQLRejectsNonNumeric(t *testing.T) {
	cfg, err := LoadConfig(strings.NewReader(
		`{"table":"T","columns":[{"name":"N","value":"{party}","type":"number"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	p := policy.Policy{Version: 1, Rules: []policy.Rule{{Pattern: "https://a.com/x", Party: "first-party"}}}
	var sb strings.Builder
	if _, err := WriteSQL(&sb, p, cfg, time.UnixMilli(0)); err == nil {
		t.Fatal("non-numeric value in a number column must error")
	}
}
