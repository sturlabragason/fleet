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
	var all map[string]json.RawMessage
	if json.Unmarshal(data, &all) != nil {
		return nil, nil
	}
	known := knownKeys(reflect.TypeOf(v).Elem())
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
		return b, nil
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
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
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
