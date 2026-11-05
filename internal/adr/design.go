package adr

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// entryRe matches a §14 ADR entry. They are bold paragraphs, not headings, and
// a reference inside a line ("Amends ADR-29") must not count as an entry.
var entryRe = regexp.MustCompile(`(?m)^\*\*ADR-([0-9]+)\b`)

// Numbers returns the ADR numbers DESIGN.md text declares.
func Numbers(text string) map[int]bool {
	out := map[int]bool{}
	for _, m := range entryRe.FindAllStringSubmatch(text, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil {
			out[n] = true
		}
	}
	return out
}

// mainRefs are tried in order: a local main first, so an agent sees what its
// own merges landed before anyone pushed.
var mainRefs = []string{"main", "origin/main"}

// OnMain returns the ADR numbers in DESIGN.md on main, read from the repo
// containing dir (worktrees share the ref, so a fleet worktree works).
func OnMain(dir string) (map[int]bool, error) {
	var last error
	for _, ref := range mainRefs {
		out, err := exec.Command("git", "-C", dir, "show", ref+":DESIGN.md").Output()
		if err == nil {
			return Numbers(string(out)), nil
		}
		last = err
	}
	return nil, fmt.Errorf("read DESIGN.md from %s in %s: %w", strings.Join(mainRefs, " or "), dir, last)
}
