package pages

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeAsk points the companion at a script for `artifact ask`: list prints
// dir/asks, take exits 3 for id "taken0000000", reply appends its args to
// dir/replies.
func fakeAsk(t *testing.T, list string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "asks"), []byte(list), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "artifact")
	script := "#!/bin/sh\nd=" + dir + "\n[ \"$1\" = ask ] || exit 1\n" +
		`case "$2" in list) cat "$d/asks" ;; take) [ "$4" = taken0000000 ] && exit 3; echo '{}' ;; reply) echo "$3 $4 $5" >> "$d/replies" ;; *) exit 1 ;; esac` + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLEET_ARTIFACT", bin)
	t.Setenv("FLEET_CONFIG", filepath.Join(dir, "none.toml"))
	return dir
}

func TestAsks(t *testing.T) {
	dir := fakeAsk(t, `{"page":"floor-a","url":"https://x.invalid/floor-a/","id":"aaaaaaaaaaaa","t":1,"question":"why?","ctx":"work","taken":null}
{"page":"floor-b","id":"bbbbbbbbbbbb","t":2,"question":"done already","taken":3}
`)
	as, err := OpenAsks(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(as) != 1 || as[0].Page != "floor-a" || as[0].Ctx != "work" || as[0].Question != "why?" {
		t.Fatalf("open asks = %+v, want only floor-a's", as)
	}
	for _, tc := range []struct {
		id   string
		want bool
	}{{"aaaaaaaaaaaa", true}, {"taken0000000", false}} {
		got, err := TakeAsk(t.Context(), "floor-a", tc.id)
		if err != nil || got != tc.want {
			t.Errorf("TakeAsk(%s) = %v, %v; want %v", tc.id, got, err, tc.want)
		}
	}
	if err := ReplyAsk(t.Context(), "floor-a", "aaaaaaaaaaaa", "it is a box"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "replies")); strings.TrimSpace(string(b)) != "floor-a aaaaaaaaaaaa it is a box" {
		t.Errorf("reply = %q", b)
	}
}
