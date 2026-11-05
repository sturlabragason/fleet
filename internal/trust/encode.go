package trust

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// obj is one JSON object kept as raw members, so every key fleet does not
// understand survives a rewrite untouched. Key order is not preserved: Go
// marshals a map in sorted order, and these files are machine-managed.
type obj map[string]json.RawMessage

// parseObj reads a JSON object; empty input is an empty object.
func parseObj(b []byte) (obj, error) {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return obj{}, nil
	}
	var o obj
	if err := json.Unmarshal(b, &o); err != nil {
		return nil, err
	}
	if o == nil {
		o = obj{}
	}
	return o, nil
}

// sub returns the nested object at key, empty when absent or null.
func (o obj) sub(key string) (obj, error) {
	raw, ok := o[key]
	if !ok {
		return obj{}, nil
	}
	return parseObj(raw)
}

// set stores v at key.
func (o obj) set(key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	o[key] = b
	return nil
}

// isTrue reports whether key holds JSON true.
func (o obj) isTrue(key string) bool {
	var b bool
	return json.Unmarshal(o[key], &b) == nil && b
}

// strings returns the string array at key. ok is false when the key holds
// something else, which is a config fleet must not silently overwrite; an
// absent key is an empty list and ok.
func (o obj) strings(key string) (l []string, ok bool) {
	raw, there := o[key]
	if !there {
		return nil, true
	}
	if err := json.Unmarshal(raw, &l); err != nil {
		return nil, false
	}
	return l, true
}

// render writes the object the way these tools do: two-space indent.
func render(o obj) ([]byte, error) {
	b, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// splitComments separates a leading // comment block from the JSON under it.
// Copilot's config.json opens with "// This file is managed automatically.",
// which is not JSON and must survive the rewrite.
func splitComments(b []byte) (head, body []byte) {
	rest := b
	for {
		line, after, found := bytes.Cut(rest, []byte("\n"))
		t := bytes.TrimSpace(line)
		if !bytes.HasPrefix(t, []byte("//")) && len(t) != 0 {
			break
		}
		if !found {
			break
		}
		rest = after
	}
	return b[:len(b)-len(rest)], rest
}

// errParse wraps a store fleet could not read. A hand-broken config file is
// the tool's problem to report, not a reason to fail the spawn.
func errParse(path string, err error) error { return fmt.Errorf("%s: %w", path, err) }

// contains reports whether l has s.
func contains(l []string, s string) bool {
	for _, v := range l {
		if v == s {
			return true
		}
	}
	return false
}

// without returns l with every s removed.
func without(l []string, s string) []string {
	out := l[:0:0]
	for _, v := range l {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}

// tomlStrings renders a TOML array of strings over one line per entry, the
// shape tomli_w writes for vibe's trusted_folders.toml.
func tomlStrings(key string, l []string) string {
	if len(l) == 0 {
		return key + " = []\n"
	}
	var b strings.Builder
	b.WriteString(key + " = [\n")
	for _, v := range l {
		fmt.Fprintf(&b, "    %s,\n", tomlQuote(v))
	}
	b.WriteString("]\n")
	return b.String()
}

// tomlQuote renders a TOML basic string.
func tomlQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`, "\r", `\r`)
	return `"` + r.Replace(s) + `"`
}
