// Package pipeline transforms raw collected URLs into canonical, deduplicated
// records ready for whitelist policy review.
package pipeline

import (
	"net/url"
	"sort"
	"strings"
)

// volatileQueryKeys are query parameters that vary per request — cache busters,
// session tokens, timestamps. They must be dropped so one endpoint collapses to
// a single whitelist entry instead of thousands of near-identical ones.
var volatileQueryKeys = map[string]bool{
	"_":            true,
	"t":            true,
	"ts":           true,
	"timestamp":    true,
	"cachebust":    true,
	"cb":           true,
	"token":        true,
	"access_token": true,
	"sessionid":    true,
	"sid":          true,
	"nonce":        true,
}

// Normalize canonicalizes a raw URL so equivalent requests dedupe together: it
// lowercases scheme and host, drops the default port and fragment, removes
// volatile query parameters, and sorts the rest. It returns ok=false for input
// that is not a usable absolute HTTP(S) URL.
func Normalize(raw string) (normalized string, ok bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", false
	}
	if u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", false
	}

	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = canonicalHost(u.Scheme, strings.ToLower(u.Host))
	u.Fragment = ""
	u.RawQuery = cleanQuery(u.Query())

	return u.String(), true
}

// canonicalHost drops the scheme's default port so http://x:80 and http://x —
// or the https equivalents — collapse to the same host.
func canonicalHost(scheme, host string) string {
	switch scheme {
	case "http":
		return strings.TrimSuffix(host, ":80")
	case "https":
		return strings.TrimSuffix(host, ":443")
	default:
		return host
	}
}

// cleanQuery removes volatile parameters and returns the remainder as a stably
// ordered query string, so dedup is deterministic regardless of input order.
func cleanQuery(values url.Values) string {
	for key := range values {
		if volatileQueryKeys[strings.ToLower(key)] {
			delete(values, key)
		}
	}
	if len(values) == 0 {
		return ""
	}
	// Encode already sorts by key; sort each key's values for full determinism.
	for _, v := range values {
		sort.Strings(v)
	}
	return values.Encode()
}
