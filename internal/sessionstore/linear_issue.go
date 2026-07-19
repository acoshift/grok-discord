package sessionstore

import (
	"fmt"
	"regexp"
	"strings"
)

// Linear provider constant for TrackedIssue.Provider.
const ProviderLinear = "linear"

// ProviderGitHub is the default when Provider is empty (legacy sessions).
const ProviderGitHub = "github"

var (
	// ENG-123 style identifiers (team key + number).
	linearIdentifierRE = regexp.MustCompile(`(?i)\b([A-Z][A-Z0-9]+)-(\d+)\b`)
	// https://linear.app/workspace/issue/ENG-123/... (optional Discord <…> wraps stripped by caller text).
	linearIssueURLRE = regexp.MustCompile(`(?i)https?://linear\.app/[^/\s>]+/issue/([A-Z][A-Z0-9]+-\d+)`)
)

// IsLinear reports whether this tracked issue is a Linear ticket.
func (iss TrackedIssue) IsLinear() bool {
	return strings.EqualFold(strings.TrimSpace(iss.Provider), ProviderLinear)
}

// NormalizeLinearIdentifier uppercases the team key portion: eng-123 → ENG-123.
func NormalizeLinearIdentifier(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	parts := strings.SplitN(id, "-", 2)
	if len(parts) != 2 {
		return strings.ToUpper(id)
	}
	return strings.ToUpper(parts[0]) + "-" + parts[1]
}

// ParseLinearIssueRefs extracts Linear issue identifiers and URLs from free text.
// Only call when the project has Linear enabled.
func ParseLinearIssueRefs(text string) []TrackedIssue {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	// Strip Discord link wrappers <https://...> so URL regex matches.
	text = strings.ReplaceAll(text, "<", " ")
	text = strings.ReplaceAll(text, ">", " ")

	type hit struct {
		iss   TrackedIssue
		start int
	}
	var hits []hit
	seen := map[string]struct{}{}

	add := func(ident string, url string, start int) {
		ident = NormalizeLinearIdentifier(ident)
		if ident == "" {
			return
		}
		key := "linear:" + strings.ToLower(ident)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		iss := TrackedIssue{
			Provider:   ProviderLinear,
			Identifier: ident,
			URL:        strings.TrimSpace(url),
			Keyword:    keywordBefore(text, start),
		}
		if team, _, ok := splitLinearIdentifier(ident); ok {
			iss.TeamKey = team
		}
		hits = append(hits, hit{iss: iss, start: start})
	}

	for _, m := range linearIssueURLRE.FindAllStringSubmatchIndex(text, -1) {
		if len(m) < 4 {
			continue
		}
		full := text[m[0]:m[1]]
		ident := text[m[2]:m[3]]
		// Canonical URL without trailing path slug noise: keep match prefix through identifier.
		url := full
		if idx := strings.Index(strings.ToLower(full), strings.ToLower(ident)); idx >= 0 {
			// keep through identifier; drop optional /slug
			end := idx + len(ident)
			url = full[:end]
		}
		add(ident, url, m[0])
	}

	for _, m := range linearIdentifierRE.FindAllStringSubmatchIndex(text, -1) {
		if len(m) < 2 {
			continue
		}
		ident := text[m[0]:m[1]]
		add(ident, "", m[0])
	}

	if len(hits) == 0 {
		return nil
	}
	out := make([]TrackedIssue, len(hits))
	for i, h := range hits {
		out[i] = h.iss
	}
	if len(out) > maxTrackedIssues {
		out = out[:maxTrackedIssues]
	}
	return out
}

func splitLinearIdentifier(ident string) (teamKey, number string, ok bool) {
	ident = NormalizeLinearIdentifier(ident)
	parts := strings.SplitN(ident, "-", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// linearIssueKey is the stable session key for a Linear ticket.
func linearIssueKey(ident string) string {
	ident = NormalizeLinearIdentifier(ident)
	if ident == "" {
		return ""
	}
	return "linear:" + strings.ToLower(ident)
}

// formatLinearDisplay is ENG-123 (preferred).
func formatLinearDisplay(iss TrackedIssue) string {
	if id := NormalizeLinearIdentifier(iss.Identifier); id != "" {
		return id
	}
	if u := strings.TrimSpace(iss.URL); u != "" {
		return u
	}
	return ""
}

// parseLinearQuery tries to interpret a /link or /unlink query as Linear.
func parseLinearQuery(query string) (TrackedIssue, bool) {
	query = strings.TrimSpace(query)
	if query == "" {
		return TrackedIssue{}, false
	}
	// Strip Discord wraps.
	query = strings.TrimPrefix(query, "<")
	query = strings.TrimSuffix(query, ">")
	parsed := ParseLinearIssueRefs(query)
	if len(parsed) == 0 {
		// Bare identifier only.
		if linearIdentifierRE.MatchString(query) {
			id := NormalizeLinearIdentifier(query)
			iss := TrackedIssue{Provider: ProviderLinear, Identifier: id}
			if team, _, ok := splitLinearIdentifier(id); ok {
				iss.TeamKey = team
			}
			return iss, true
		}
		return TrackedIssue{}, false
	}
	return parsed[0], true
}

// linearPRBodyLine returns "Fixes ENG-123" style for Linear's GitHub integration.
func linearPRBodyLine(iss TrackedIssue) string {
	kw := iss.EffectiveKeyword()
	id := NormalizeLinearIdentifier(iss.Identifier)
	if id == "" {
		if u := strings.TrimSpace(iss.URL); u != "" {
			return kw + " " + u
		}
		return ""
	}
	return fmt.Sprintf("%s %s", kw, id)
}
