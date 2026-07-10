// Package patterns proposes wildcard whitelist rules from observed URLs.
//
// Generalization is deliberately conservative: an over-broad whitelist pattern
// is a security hole, so a path segment is only ever wildcarded when it looks
// machine-generated (numeric ID, UUID, hash, token) AND enough distinct values
// were actually observed. Suggestions are additive proposals for a human to
// approve — the observed URL list itself is never modified.
package patterns

import (
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/gjwnssud/url-trace/internal/model"
)

// minDistinctValues is how many different values a segment must take before we
// propose wildcarding it. Two IDs could be coincidence; three establishes the
// position as genuinely variable.
const minDistinctValues = 3

const maxExamples = 3

// idLike matches path segments that are unmistakably machine-generated. Plain
// words never match, so /api/users vs /api/orders can never collapse into /api/*.
var idLike = []*regexp.Regexp{
	regexp.MustCompile(`^[0-9]+$`), // numeric ID
	regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`), // UUID
	regexp.MustCompile(`^[0-9a-fA-F]{16,}$`),                                                            // long hex hash
}

// longToken catches generated tokens that mix letters and digits. The digit
// requirement keeps ordinary long words ("internationalization") from matching.
var longToken = regexp.MustCompile(`^[A-Za-z0-9_-]{20,}$`)

func looksVariable(segment string) bool {
	for _, re := range idLike {
		if re.MatchString(segment) {
			return true
		}
	}
	return longToken.MatchString(segment) && strings.ContainsAny(segment, "0123456789")
}

type group struct {
	distinct map[string]bool
	total    int
	examples []string
}

// Suggest scans aggregated records and returns wildcard rule proposals, sorted
// by pattern. Query strings are ignored for pattern purposes; examples carry
// the original full URLs so a reviewer can see exactly what a pattern covers.
func Suggest(records []model.URLRecord) []model.PatternSuggestion {
	groups := make(map[string]*group)

	for _, r := range records {
		u, err := url.Parse(r.URL)
		if err != nil || u.Path == "" || u.Path == "/" {
			continue
		}
		segments := strings.Split(strings.Trim(u.Path, "/"), "/")
		for i, segment := range segments {
			if !looksVariable(segment) {
				continue
			}
			key := patternKey(u, segments, i)
			g := groups[key]
			if g == nil {
				g = &group{distinct: make(map[string]bool)}
				groups[key] = g
			}
			g.distinct[segment] = true
			g.total += r.Count
			if len(g.examples) < maxExamples {
				g.examples = append(g.examples, r.URL)
			}
		}
	}

	suggestions := make([]model.PatternSuggestion, 0)
	for pattern, g := range groups {
		if len(g.distinct) < minDistinctValues {
			continue
		}
		suggestions = append(suggestions, model.PatternSuggestion{
			Pattern:        pattern,
			DistinctValues: len(g.distinct),
			TotalCount:     g.total,
			Examples:       g.examples,
		})
	}
	sort.Slice(suggestions, func(i, j int) bool {
		return suggestions[i].Pattern < suggestions[j].Pattern
	})
	return suggestions
}

// patternKey renders the candidate rule with position wildcard at index i,
// e.g. https://api.example.com/v1/users/*.
func patternKey(u *url.URL, segments []string, wildcard int) string {
	parts := make([]string, len(segments))
	copy(parts, segments)
	parts[wildcard] = "*"
	return u.Scheme + "://" + u.Host + "/" + strings.Join(parts, "/")
}
