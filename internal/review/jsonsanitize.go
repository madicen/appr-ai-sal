package review

import (
	"encoding/json"
	"regexp"
	"strings"
)

// stripMarkdownCodeFence removes a leading ```json / ``` wrapper some models emit.
func stripMarkdownCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	rest := strings.TrimPrefix(s, "```")
	rest = strings.TrimLeft(rest, "\r\n")
	if len(rest) > 0 && (rest[0] == '{' || rest[0] == '[') {
		if i := strings.LastIndex(rest, "```"); i >= 0 {
			rest = rest[:i]
		}
		return strings.TrimSpace(rest)
	}
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[i+1:]
	}
	if i := strings.LastIndex(rest, "```"); i >= 0 {
		rest = rest[:i]
	}
	return strings.TrimSpace(rest)
}

// stripJSONCStyleComments removes // and /* */ outside JSON strings so encoding/json
// can parse models that emit JSON5-style comments (invalid in strict JSON).
func stripJSONCStyleComments(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inStr := false
	esc := false
	for i := 0; i < len(s); {
		c := s[i]
		if inStr {
			if esc {
				b.WriteByte(c)
				esc = false
				i++
				continue
			}
			if c == '\\' {
				b.WriteByte(c)
				esc = true
				i++
				continue
			}
			if c == '"' {
				inStr = false
			}
			b.WriteByte(c)
			i++
			continue
		}
		if c == '"' {
			inStr = true
			b.WriteByte(c)
			i++
			continue
		}
		// Line comment
		if c == '/' && i+1 < len(s) && s[i+1] == '/' {
			i += 2
			for i < len(s) && s[i] != '\n' && s[i] != '\r' {
				i++
			}
			if i < len(s) {
				b.WriteByte('\n')
				i++
			}
			continue
		}
		// Block comment
		if c == '/' && i+1 < len(s) && s[i+1] == '*' {
			i += 2
			for i+1 < len(s) && !(s[i] == '*' && s[i+1] == '/') {
				i++
			}
			if i+1 < len(s) {
				i += 2
			}
			b.WriteByte(' ')
			continue
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

// specialistJSONVariants returns distinct parse attempts for noisy model output.
func specialistJSONVariants(s string) []string {
	s = strings.TrimSpace(s)
	base := []string{s, stripMarkdownCodeFence(s)}
	seen := map[string]struct{}{}
	var out []string
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		if _, ok := seen[v]; ok {
			return
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	for _, c := range base {
		add(c)
		add(stripJSONCStyleComments(c))
		tq := sanitizeTripleQuotedStringValues(c)
		add(tq)
		add(sanitizeTripleQuotedStringValues(stripJSONCStyleComments(c)))
		add(stripJSONCStyleComments(tq))
	}
	return out
}

// tripleQuotedJSONField matches a few object keys whose values are sometimes
// emitted as Python-style """...""" instead of a JSON string (invalid JSON).
var tripleQuotedJSONField = regexp.MustCompile(
	`(?is)("(?:suggestion|comment|prompt|title|summary|path)"\s*:\s*)\s*"""(.*?)"""`,
)

// sanitizeTripleQuotedStringValues rewrites invalid triple-quoted string values
// into JSON strings so encoding/json can parse them.
func sanitizeTripleQuotedStringValues(s string) string {
	if !strings.Contains(s, `"""`) {
		return s
	}
	return tripleQuotedJSONField.ReplaceAllStringFunc(s, func(match string) string {
		sub := tripleQuotedJSONField.FindStringSubmatch(match)
		if len(sub) < 3 {
			return match
		}
		quoted, err := json.Marshal(sub[2])
		if err != nil {
			return match
		}
		return sub[1] + string(quoted)
	})
}
