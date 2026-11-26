package fleet

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sturlabragason/fleet/internal/board"
)

// A spawn in a repo without its own check gets the repo's (ADR-202).
func TestRepoCheck(t *testing.T) {
	withToml := func(toml string) string {
		dir := repo(t)
		if err := os.MkdirAll(filepath.Join(dir, ".fleet"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".fleet", "repo.toml"), []byte(toml), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	done := withToml("merge_check = \"make ci\"\ndone_check = \"make build\"\n")
	merge := withToml("merge_check = \"make ci\"\n")
	sub := filepath.Join(done, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	optOut := withToml("merge_check = \"make ci\"\ndone_check = \"none\"\n")
	cases := []struct {
		name string
		dir  string
		in   *board.Card
		want *board.Card // nil: no card
	}{
		{"outside git, no card", t.TempDir(), nil, nil},
		{"repo without file", repo(t), &board.Card{Goal: "g", Result: DefaultResult}, &board.Card{Goal: "g", Result: DefaultResult}},
		{"done_check fills a missing card", done, nil, &board.Card{Check: "make build", Result: DefaultResult}},
		{"done_check fills a card", done, &board.Card{Goal: "g", Result: "r.md"}, &board.Card{Goal: "g", Check: "make build", Result: "r.md"}},
		{"merge_check is the fallback", merge, nil, &board.Card{Check: "make ci", Result: DefaultResult}},
		{"explicit check wins", done, &board.Card{Check: "true", Result: DefaultResult}, &board.Card{Check: "true", Result: DefaultResult}},
		{"none alone drops the card", done, &board.Card{Check: NoCheck, Result: DefaultResult}, nil},
		{"none keeps the rest", done, &board.Card{Goal: "g", Check: NoCheck, Result: DefaultResult}, &board.Card{Goal: "g", Result: DefaultResult}},
		{"repo opts out", optOut, nil, nil},
		{"subfolder keeps the card", sub, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RepoCheck(tc.in, tc.dir)
			if (got == nil) != (tc.want == nil) || got != nil &&
				(got.Goal != tc.want.Goal || got.Check != tc.want.Check || got.Result != tc.want.Result) {
				t.Fatalf("RepoCheck = %+v, want %+v", got, tc.want)
			}
		})
	}
}
