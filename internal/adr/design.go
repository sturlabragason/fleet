package adr

import (
	"fmt"
	"os/exec"
	"regexp"
	"sort"
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

// numbersAt returns the ADR numbers DESIGN.md declares at rev; a rev without a
// DESIGN.md declares none.
func numbersAt(dir, rev string) map[int]bool {
	out, err := exec.Command("git", "-C", dir, "show", rev+":DESIGN.md").Output()
	if err != nil {
		return map[int]bool{}
	}
	return Numbers(string(out))
}

// BranchAdds returns the ADR entries HEAD's DESIGN.md declares that the merge
// base with main does not: the numbers this branch introduces, lowest first.
// An amendment to an existing ADR adds nothing.
func BranchAdds(dir string) ([]int, error) {
	var last error
	for _, ref := range mainRefs {
		base, err := exec.Command("git", "-C", dir, "merge-base", "HEAD", ref).Output()
		if err != nil {
			last = err
			continue
		}
		before := numbersAt(dir, strings.TrimSpace(string(base)))
		var adds []int
		for n := range numbersAt(dir, "HEAD") {
			if !before[n] {
				adds = append(adds, n)
			}
		}
		sort.Ints(adds)
		return adds, nil
	}
	return nil, fmt.Errorf("no merge base with %s in %s: %w", strings.Join(mainRefs, " or "), dir, last)
}

// Collision is an ADR number a branch adds that someone else already holds.
type Collision struct {
	N      int
	Holder string // "main", or the agent whose live claim it is
}

func (c Collision) String() string {
	if c.Holder == "main" {
		return fmt.Sprintf("ADR-%d is taken (already on main)", c.N)
	}
	return fmt.Sprintf("ADR-%d is taken (claimed by %s)", c.N, c.Holder)
}

// Taken returns the numbers in adds that are on main or held by another
// agent's live claim. A claim for one of me, or for nobody, is not a
// collision: it may be this agent's own (fleet#40).
func Taken(adds []int, onMain map[int]bool, claims []Claim, me ...string) []Collision {
	mine := map[string]bool{"": true}
	for _, m := range me {
		mine[m] = true
	}
	var out []Collision
	for _, n := range adds {
		if onMain[n] {
			out = append(out, Collision{N: n, Holder: "main"})
			continue
		}
		for _, c := range claims {
			if c.N == n && !mine[c.For] {
				out = append(out, Collision{N: n, Holder: c.For})
				break
			}
		}
	}
	return out
}
