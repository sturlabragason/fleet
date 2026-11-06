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
