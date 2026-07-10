package source

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestWaitForSignal(t *testing.T) {
	t.Run("returns on newline", func(t *testing.T) {
		if err := waitForSignal(context.Background(), strings.NewReader("\n")); err != nil {
			t.Errorf("got %v, want nil", err)
		}
	})
	t.Run("returns on EOF (piped/empty stdin)", func(t *testing.T) {
		if err := waitForSignal(context.Background(), strings.NewReader("")); err != nil {
			t.Errorf("got %v, want nil", err)
		}
	})
	t.Run("returns on context cancel", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		// blockingReader never yields, so only ctx cancellation can unblock.
		if err := waitForSignal(ctx, blockingReader{}); err == nil {
			t.Error("want context error, got nil")
		}
	})
}

// blockingReader blocks forever, simulating a stdin with no input.
type blockingReader struct{}

func (blockingReader) Read([]byte) (int, error) { select {} }

func TestParseCookies(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []cookie
	}{
		{
			name: "single cookie",
			in:   []string{"SESSION=abc"},
			want: []cookie{{"SESSION", "abc"}},
		},
		{
			name: "comma-separated in one flag",
			in:   []string{"SESSION=NDAwZDI0OTIt,C_PVCC=-7dcef955b60fc0750cc9fe20ae641350_1783661758862_-6ba3d0a6c79f4f72"},
			want: []cookie{
				{"SESSION", "NDAwZDI0OTIt"},
				{"C_PVCC", "-7dcef955b60fc0750cc9fe20ae641350_1783661758862_-6ba3d0a6c79f4f72"},
			},
		},
		{
			name: "semicolon-separated (copied Cookie header)",
			in:   []string{"a=1; b=2"},
			want: []cookie{{"a", "1"}, {"b", "2"}},
		},
		{
			name: "repeated flags",
			in:   []string{"a=1", "b=2"},
			want: []cookie{{"a", "1"}, {"b", "2"}},
		},
		{
			name: "value keeps '=' padding",
			in:   []string{"t=YWJjZA=="},
			want: []cookie{{"t", "YWJjZA=="}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCookies(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d cookies, want %d: %+v", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("cookie[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestParseCookiesInvalid(t *testing.T) {
	if _, err := parseCookies([]string{"noequalsign"}); err == nil {
		t.Fatal("a cookie without '=' must error")
	}
}

func TestNormalizeLink(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"https://a.com/x", "https://a.com/x"},
		{"https://a.com/x#/users", "https://a.com/x#/users"},   // hash route kept
		{"https://a.com/#!/orders", "https://a.com/#!/orders"}, // hashbang route kept
		{"https://a.com/x#section", "https://a.com/x"},         // plain anchor dropped
		{"mailto:me@a.com", ""},                                // non-http dropped
		{"javascript:void(0)", ""},
	}
	for _, tt := range tests {
		if got := normalizeLink(tt.in); got != tt.want {
			t.Errorf("normalizeLink(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
