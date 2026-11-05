package trust

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// markVibe adds dir to `trusted` in vibe's trusted_folders.toml and takes it
// off `untrusted`, which is what "Trust this folder" writes. vibe decides by
// the closest decided ancestor, so an explicit untrust has to go.
//
// Only those two assignments are rewritten; anything else in the file is
// left exactly as it was.
func markVibe(home string, env Env, dir string) (Result, error) {
	path := filepath.Join(vibeHome(home, env), "trusted_folders.toml")
	r := Result{Tool: "vibe", File: path}
	changed, err := edit(path, func(old []byte) ([]byte, bool, error) {
		text := string(old)
		trusted, err := tomlArray(text, "trusted")
		if err != nil {
			return nil, false, errParse(path, err)
		}
		untrusted, err := tomlArray(text, "untrusted")
		if err != nil {
			return nil, false, errParse(path, err)
		}
		if contains(trusted, dir) && !contains(untrusted, dir) {
			return nil, false, nil
		}
		if !contains(trusted, dir) {
			trusted = append(trusted, dir)
		}
		untrusted = without(untrusted, dir)
		if text, err = setTomlArray(text, "trusted", trusted); err != nil {
			return nil, false, errParse(path, err)
		}
		if text, err = setTomlArray(text, "untrusted", untrusted); err != nil {
			return nil, false, errParse(path, err)
		}
		return []byte(text), true, nil
	})
	r.Changed = changed
	return r, err
}

// vibeHome is $VIBE_HOME or ~/.vibe.
func vibeHome(home string, env Env) string {
	if v := strings.TrimSpace(env("VIBE_HOME")); v != "" {
		return absDir(home, v)
	}
	return filepath.Join(home, ".vibe")
}

var (
	arrayStartRe = regexp.MustCompile(`(?m)^[ \t]*(?:"([^"]*)"|([A-Za-z0-9_-]+))[ \t]*=[ \t]*\[`)
	tomlStrRe    = regexp.MustCompile(`"(?:[^"\\]|\\.)*"`)
)

// tomlArray reads the array of strings at top-level key, which vibe writes
// over several lines, so the line-based conf parser cannot read it. A key
// that is not there is an empty array; an unterminated one is an error, so a
// half-written file is never taken for an empty trust list.
func tomlArray(text, key string) ([]string, error) {
	vals, _, _, err := tomlArraySpan(text, key)
	return vals, err
}

// tomlArraySpan also returns the half-open byte range the whole assignment
// covers, so an array can be written back without touching anything else in
// the file. start is -1 when the key is not there.
func tomlArraySpan(text, key string) (vals []string, start, end int, err error) {
	for _, m := range arrayStartRe.FindAllStringSubmatchIndex(text, -1) {
		name := group(text, m, 1)
		if name == "" {
			name = group(text, m, 2)
		}
		if name != key {
			continue
		}
		body := text[m[1]:]
		quoted := tomlStrRe.FindAllStringIndex(body, -1)
		stop := closeBracket(body, quoted)
		if stop < 0 {
			return nil, -1, 0, fmt.Errorf("%s: unterminated array", key)
		}
		for _, q := range quoted {
			if q[0] > stop {
				break
			}
			v, err := strconv.Unquote(body[q[0]:q[1]])
			if err != nil {
				return nil, -1, 0, fmt.Errorf("%s: %w", key, err)
			}
			vals = append(vals, v)
		}
		return vals, m[0], m[1] + stop + 1, nil
	}
	return nil, -1, 0, nil
}

// closeBracket finds the ] that ends an array body, skipping any that falls
// inside one of the quoted spans: a trusted path may contain a bracket.
func closeBracket(body string, quoted [][]int) int {
	for i := 0; i < len(body); i++ {
		if body[i] != ']' {
			continue
		}
		inside := false
		for _, q := range quoted {
			if q[0] <= i && i < q[1] {
				inside = true
				break
			}
		}
		if !inside {
			return i
		}
	}
	return -1
}

// setTomlArray writes key = [...] back into text, replacing the assignment
// already there and keeping every other key and comment in the file, so a
// key a later vibe release adds survives fleet's rewrite. A key that is not
// there yet is appended.
func setTomlArray(text, key string, vals []string) (string, error) {
	_, start, end, err := tomlArraySpan(text, key)
	if err != nil {
		return "", err
	}
	rendered := strings.TrimRight(tomlStrings(key, vals), "\n")
	if start < 0 {
		if len(vals) == 0 {
			return text, nil // nothing to say, and no empty key to add
		}
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		return text + rendered + "\n", nil
	}
	return text[:start] + rendered + text[end:], nil
}

// group returns submatch n of a FindAllSubmatchIndex entry ("" when unset).
func group(text string, m []int, n int) string {
	if len(m) <= 2*n+1 || m[2*n] < 0 {
		return ""
	}
	return text[m[2*n]:m[2*n+1]]
}
