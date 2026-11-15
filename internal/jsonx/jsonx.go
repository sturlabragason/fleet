// Package jsonx keeps JSON keys a type does not know across a read-modify-
// write (ADR-58, ADR-62). Several fleet binaries of different vintages write
// the same files — a resident watcher, the overview, every MCP server — so
// any of them must write back what a newer (or older) one put there.
//
// A type opts in with an unexported Extra field and two short methods that
// decode and encode through a method-less alias of itself:
//
//	type teamJSON Team
//	func (t *Team) UnmarshalJSON(b []byte) error {
//		var base teamJSON
//		e, err := jsonx.Unmarshal(b, &base)
//		*t = Team(base)
//		t.extra = e
//		return err
//	}
//	func (t Team) MarshalJSON() ([]byte, error) { return jsonx.Marshal(teamJSON(t), t.extra) }
package jsonx

import (
	"encoding/json"
	"reflect"
	"strings"
	"sync"
)

// Extra holds the keys of a JSON object that its Go type has no field for.
type Extra map[string]json.RawMessage

// Unmarshal decodes data into v (a pointer to a struct type without its own
// UnmarshalJSON) and returns the object's keys v's type does not know.
func Unmarshal[T any](data []byte, v *T) (Extra, error) {
	if err := json.Unmarshal(data, v); err != nil {
		return nil, err
	}
	known := knownKeys(reflect.TypeFor[T]())
	// Nearly every object has only known keys: a scan of its top-level
	// keys settles that without decoding every value a second time, which
	// cost the watcher a whole board.json parse a pass (ADR-138).
	if onlyKnown(data, known) {
		return nil, nil
	}
	var all map[string]json.RawMessage
	if json.Unmarshal(data, &all) != nil {
		return nil, nil //nolint:nilerr // v decoded; without an object there are no extra keys to keep
	}
	for k := range all {
		if known[strings.ToLower(k)] {
			delete(all, k)
		}
	}
	if len(all) == 0 {
		return nil, nil
	}
	return all, nil
}

// Marshal encodes v (a struct without its own MarshalJSON) plus the extra
// keys a read kept. A key v now writes itself wins over a stale extra copy.
func Marshal(v any, extra Extra) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil || len(extra) == 0 {
		return b, err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(b, &all); err != nil {
		return b, nil //nolint:nilerr // b is valid JSON; without an object there is nowhere to put extra keys
	}
	known := knownKeys(reflect.TypeOf(v))
	for k, raw := range extra {
		if _, dup := all[k]; dup || known[strings.ToLower(k)] {
			continue
		}
		all[k] = raw
	}
	return json.Marshal(all)
}

var cache sync.Map // reflect.Type -> map[string]bool

// knownKeys is the lower-cased json names of t's fields, embedded structs'
// fields included. encoding/json matches names case-insensitively, so this
// does too: a key the struct decoded is never also kept as unknown.
func knownKeys(t reflect.Type) map[string]bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if m, ok := cache.Load(t); ok {
		return m.(map[string]bool)
	}
	m := map[string]bool{}
	if t.Kind() == reflect.Struct {
		for f := range t.Fields() {
			tag := f.Tag.Get("json")
			name, _, _ := strings.Cut(tag, ",")
			if name == "-" && tag == "-" {
				continue
			}
			if f.Anonymous && name == "" {
				for k := range knownKeys(f.Type) {
					m[k] = true
				}
				continue
			}
			if !f.IsExported() {
				continue
			}
			if name == "" {
				name = f.Name
			}
			m[strings.ToLower(name)] = true
		}
	}
	cache.Store(t, m)
	return m
}

// onlyKnown reports whether data is a JSON object whose top-level keys are
// all in known (lower-cased). data has already decoded, so it is valid; any
// shape this scan does not expect answers false and leaves the decision to
// the full decode.
func onlyKnown(data []byte, known map[string]bool) bool {
	i := skipSpace(data, 0)
	if i >= len(data) || data[i] != '{' {
		return false
	}
	for i++; ; {
		i = skipSpace(data, i)
		if i >= len(data) {
			return false
		}
		switch data[i] {
		case '}':
			return true
		case ',':
			i++
			continue
		case '"':
		default:
			return false
		}
		end := endString(data, i)
		if end < 0 {
			return false
		}
		key := string(data[i+1 : end-1])
		if strings.IndexByte(key, '\\') >= 0 && json.Unmarshal(data[i:end], &key) != nil {
			return false
		}
		if !known[strings.ToLower(key)] {
			return false
		}
		i = skipSpace(data, end)
		if i >= len(data) || data[i] != ':' {
			return false
		}
		if i = skipValue(data, skipSpace(data, i+1)); i < 0 {
			return false
		}
	}
}

func skipSpace(data []byte, i int) int {
	for i < len(data) && (data[i] == ' ' || data[i] == '\t' || data[i] == '\n' || data[i] == '\r') {
		i++
	}
	return i
}

// endString is the index just past the string that opens at data[i], -1 if
// it does not close.
func endString(data []byte, i int) int {
	for j := i + 1; j < len(data); j++ {
		switch data[j] {
		case '\\':
			j++
		case '"':
			return j + 1
		}
	}
	return -1
}

// skipValue is the index just past the value that starts at data[i], -1 on
// a shape it does not know.
func skipValue(data []byte, i int) int {
	if i >= len(data) {
		return -1
	}
	switch data[i] {
	case '"':
		return endString(data, i)
	case '{', '[':
		depth := 0
		for ; i < len(data); i++ {
			switch data[i] {
			case '"':
				if i = endString(data, i); i < 0 {
					return -1
				}
				i--
			case '{', '[':
				depth++
			case '}', ']':
				if depth--; depth == 0 {
					return i + 1
				}
			}
		}
		return -1
	}
	for i < len(data) && !strings.ContainsRune(",}] \t\n\r", rune(data[i])) {
		i++
	}
	return i
}
