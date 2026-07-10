package pipeline

import "testing"

func TestNormalize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{"lowercases scheme and host", "HTTP://Example.COM/Path", "http://example.com/Path", true},
		{"drops default http port", "http://example.com:80/a", "http://example.com/a", true},
		{"drops default https port", "https://example.com:443/a", "https://example.com/a", true},
		{"keeps non-default port", "http://example.com:8080/a", "http://example.com:8080/a", true},
		{"strips fragment", "https://example.com/a#section", "https://example.com/a", true},
		{"removes volatile query", "https://example.com/a?_=123&q=x", "https://example.com/a?q=x", true},
		{"sorts query keys", "https://example.com/a?b=2&a=1", "https://example.com/a?a=1&b=2", true},
		{"rejects relative url", "/just/a/path", "", false},
		{"rejects non-http scheme", "ftp://example.com/a", "", false},
		{"rejects empty input", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Normalize(tt.in)
			if ok != tt.ok {
				t.Fatalf("Normalize(%q) ok = %v, want %v", tt.in, ok, tt.ok)
			}
			if got != tt.want {
				t.Errorf("Normalize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
