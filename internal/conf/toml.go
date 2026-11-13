// Package conf parses the small TOML subset fleet uses for its data files:
// [section] headers (dotted names allowed), key = "string", key = ["a", "b"],
// key = { K = "v" }, numbers/bools as raw strings, and # comments.
package conf

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Value is a string, []string or map[string]string.
type Value any

// Doc maps section name -> key -> value. Top-level keys use section "".
type Doc map[string]map[string]Value

// ParseFile reads and parses a file. A missing file returns an empty Doc.
func ParseFile(path string) (Doc, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Doc{}, nil
	}
	if err != nil {
		return nil, err
	}
	return Parse(string(b))
}

// Parse parses TOML-subset text.
func Parse(s string) (Doc, error) {
	d := Doc{"": {}}
	sec := ""
	for n, raw := range strings.Split(s, "\n") {
		line := strings.TrimSpace(stripComment(raw))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			sec = strings.Trim(strings.TrimSpace(line[1:len(line)-1]), `"`)
			if d[sec] == nil {
				d[sec] = map[string]Value{}
			}
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected key = value", n+1)
		}
		val, err := parseValue(strings.TrimSpace(v))
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n+1, err)
		}
		d[sec][strings.Trim(strings.TrimSpace(k), `"`)] = val
	}
	return d, nil
}

func stripComment(s string) string {
	in, lit := false, false // inside a "basic" or a 'literal' string
	for i, r := range s {
		switch {
		case r == '"' && !lit && !escaped(s, i):
			in = !in
		case r == '\'' && !in:
			lit = !lit
		case r == '#' && !in && !lit:
			return s[:i]
		}
	}
	return s
}

// escaped says whether s[i] follows an odd run of backslashes, so a
// closing quote after an escaped backslash ("C:\\") still closes.
func escaped(s string, i int) bool {
	n := 0
	for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
		n++
	}
	return n%2 == 1
}

func parseValue(v string) (Value, error) {
	switch {
	case strings.HasPrefix(v, "["):
		if !strings.HasSuffix(v, "]") {
			return nil, fmt.Errorf("unterminated array")
		}
		items, err := splitList(v[1 : len(v)-1])
		if err != nil {
			return nil, err
		}
		out := []string{}
		for _, it := range items {
			out = append(out, unquote(it))
		}
		return out, nil
	case strings.HasPrefix(v, "{"):
		if !strings.HasSuffix(v, "}") {
			return nil, fmt.Errorf("unterminated inline table")
		}
		items, err := splitList(v[1 : len(v)-1])
		if err != nil {
			return nil, err
		}
		m := map[string]string{}
		for _, it := range items {
			k, val, ok := strings.Cut(it, "=")
			if !ok {
				return nil, fmt.Errorf("bad inline table entry %q", it)
			}
			m[strings.Trim(strings.TrimSpace(k), `"`)] = unquote(strings.TrimSpace(val))
		}
		return m, nil
	}
	return unquote(v), nil
}

// splitList splits on commas outside quotes.
func splitList(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	var q rune
	for i, r := range s {
		switch {
		case q != 0:
			if r == q && (q == '\'' || !escaped(s, i)) {
				q = 0
			}
			cur.WriteRune(r)
		case r == '"' || r == '\'':
			q = r
			cur.WriteRune(r)
		case r == ',':
			if t := strings.TrimSpace(cur.String()); t != "" {
				out = append(out, t)
			}
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if q != 0 {
		return nil, fmt.Errorf("unterminated string")
	}
	if t := strings.TrimSpace(cur.String()); t != "" {
		out = append(out, t)
	}
	return out, nil
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return s[1 : len(s)-1]
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		r := strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\n`, "\n", `\t`, "\t")
		return r.Replace(s[1 : len(s)-1])
	}
	return s
}

// Str returns a string field or "".
func Str(m map[string]Value, k string) string { s, _ := m[k].(string); return s }

// List returns a list field or nil.
func List(m map[string]Value, k string) []string { l, _ := m[k].([]string); return l }

// Map returns an inline-table field or nil.
func Map(m map[string]Value, k string) map[string]string { t, _ := m[k].(map[string]string); return t }

// Bool returns a bool field. ok is false when the key is absent or not a bool
// ("true"/"false", any case).
func Bool(m map[string]Value, k string) (val, ok bool) {
	s, isStr := m[k].(string)
	if !isStr {
		return false, false
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	return false, false
}

// Dur returns a duration field parsed with time.ParseDuration. ok is false
// when the key is absent or unparseable.
func Dur(m map[string]Value, k string) (d time.Duration, ok bool) {
	s, isStr := m[k].(string)
	if !isStr || strings.TrimSpace(s) == "" {
		return 0, false
	}
	d, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil {
		return 0, false
	}
	return d, true
}
