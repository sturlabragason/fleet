package jsonx

import (
	"encoding/json"
	"strings"
	"testing"
)

type inner struct {
	A     string `json:"a"`
	Skip  string `json:"-"`
	Plain int
	extra Extra
}

type innerJSON inner

func (v *inner) UnmarshalJSON(b []byte) error {
	var base innerJSON
	e, err := Unmarshal(b, &base)
	*v = inner(base)
	v.extra = e
	return err
}

func (v inner) MarshalJSON() ([]byte, error) { return Marshal(innerJSON(v), v.extra) }

// A newer binary's key, at any depth, survives a round trip through a type
// that has no field for it; known keys still decode and win over a stale copy.
func TestRoundTripKeepsUnknownKeys(t *testing.T) {
	in := `{"a":"x","Plain":3,"future":{"deep":[1,2]},"plain2":true}`
	var v inner
	if err := json.Unmarshal([]byte(in), &v); err != nil {
		t.Fatal(err)
	}
	if v.A != "x" || v.Plain != 3 {
		t.Fatalf("known fields: %+v", v)
	}
	v.A = "y"
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.Unmarshal(out, &got)
	if got["a"] != "y" || got["plain2"] != true || got["future"] == nil || got["Plain"] != float64(3) {
		t.Fatalf("round trip lost or kept stale keys: %s", out)
	}
	if strings.Contains(string(out), "Skip") {
		t.Fatalf("a json:\"-\" field was written: %s", out)
	}
}

// Field names match case-insensitively, as encoding/json does, so a key the
// struct decoded is never also written back as an unknown one.
func TestKnownKeysAreCaseInsensitive(t *testing.T) {
	var v inner
	json.Unmarshal([]byte(`{"A":"x","plain":2}`), &v)
	if len(v.extra) != 0 {
		t.Fatalf("decoded keys kept as unknown: %v", v.extra)
	}
}

// Nothing unknown: the output is exactly what the plain struct encodes.
func TestNoExtraIsPlainMarshal(t *testing.T) {
	v := inner{A: "x"}
	got, _ := json.Marshal(v)
	want, _ := json.Marshal(innerJSON(v))
	if string(got) != string(want) {
		t.Fatalf("got %s want %s", got, want)
	}
}

// The key scan that skips the second decode must agree with it: unknown keys
// are kept wherever they sit, and strings that look like JSON structure do
// not throw it off.
func TestUnknownKeysFoundByScan(t *testing.T) {
	tests := []struct {
		name, in string
		extra    []string
	}{
		{"known only", `{"a":"x","Plain":1}`, nil},
		{"spaced", " {\n \"a\" : \"x\" ,\t\"plain\":2 }\n", nil},
		{"unknown last", `{"a":"x","new":1}`, []string{"new"}},
		{"unknown after nested", `{"a":"x","Plain":1,"deep":{"a":[1,{"b":"}"}]},"z":null}`, []string{"deep", "z"}},
		{"braces and quotes in strings", `{"a":"{\"}[\\","Plain":0}`, nil},
		{"escaped known key", `{"\u0061":"x"}`, nil},
		{"escaped unknown key", `{"\u0062":"x"}`, []string{"b"}},
		{"empty", `{}`, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var v inner
			if err := json.Unmarshal([]byte(tc.in), &v); err != nil {
				t.Fatal(err)
			}
			if len(v.extra) != len(tc.extra) {
				t.Fatalf("extra = %v, want keys %v", v.extra, tc.extra)
			}
			for _, k := range tc.extra {
				if _, ok := v.extra[k]; !ok {
					t.Fatalf("extra = %v, want key %q", v.extra, k)
				}
			}
		})
	}
}
