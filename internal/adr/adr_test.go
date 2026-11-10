package adr

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const design = `# DESIGN

## 14. Decisions

**ADR-41: a readability floor** (R4). *Amends ADR-15.*
Decision: something. Rejected: ADR-99 was never an entry.

**ADR-42: fixtures come from snap** (R2)
Why: because. See ADR-36.
`

func TestNumbersReadsBoldEntriesNotReferences(t *testing.T) {
	got := Numbers(design)
	for _, n := range []int{41, 42} {
		if !got[n] {
			t.Errorf("ADR-%d missing from %v", n, got)
		}
	}
	for _, n := range []int{15, 36, 99} {
		if got[n] {
			t.Errorf("ADR-%d is a reference inside a line, not an entry", n)
		}
	}
}

// The seeded claims are what stops a fresh ~/.fleet from re-issuing a number an
// agent is already writing.
func TestClaimStartsAboveTheSeedAndMain(t *testing.T) {
	s := NewStore(t.TempDir())
	n, err := s.Claim("a1", Numbers(design)) // ADR-42 is the fleet fingerprint
	if err != nil {
		t.Fatal(err)
	}
	if want := 51; n != want { // seed tops out at ADR-50
		t.Fatalf("first claim %d, want %d", n, want)
	}
}

// In another repo the hand-outs mean nothing: they would be claims that repo
// can never release.
func TestClaimDoesNotSeedOutsideFleet(t *testing.T) {
	s := NewStore(t.TempDir())
	n, err := s.Claim("a1", map[int]bool{3: true})
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("first claim %d, want 4 (no seed outside fleet)", n)
	}
	cs, err := s.List(map[int]bool{3: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 {
		t.Fatalf("live claims %v, want only the one just made", cs)
	}
}

// A claim in another repo seeds nothing, so it must not mark the hand-outs
// recorded: the next claim in fleet would then hand out ADR-43 again.
func TestAClaimElsewhereDoesNotDisableTheSeed(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, err := s.Claim("other", map[int]bool{3: true}); err != nil {
		t.Fatal(err)
	}
	n, err := s.Claim("a1", Numbers(design))
	if err != nil {
		t.Fatal(err)
	}
	if n != 51 {
		t.Fatalf("claim in fleet %d, want 51 (the hand-outs still hold 43-50)", n)
	}
}

// ls must show the hand-outs before anyone has claimed: it is what a reviewer
// reads to check a number is free.
func TestListShowsTheSeedBeforeTheFirstClaim(t *testing.T) {
	cs, err := NewStore(t.TempDir()).List(Numbers(design))
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != len(handedOut) {
		t.Fatalf("live claims %v, want the %d hand-outs", cs, len(handedOut))
	}
}

// The store is shared by every repo, so another repo's DESIGN.md must never
// delete a claim: filtering happens on read, deleting only in Release.
func TestAnotherRepoDoesNotDeleteLiveClaims(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, err := s.Claim("a1", Numbers(design)); err != nil {
		t.Fatal(err)
	}
	// A second repo whose own DESIGN.md happens to contain ADR-47.
	if _, err := s.Claim("other", map[int]bool{47: true}); err != nil {
		t.Fatal(err)
	}
	cs, err := s.List(Numbers(design))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cs {
		if c.N == 47 {
			return
		}
	}
	t.Fatalf("ADR-47 was dropped by a claim in another repo: %v", cs)
}

// A read error is not a missing file: guessing "empty" there would hand out a
// number someone already holds, and then overwrite the file that proved it.
func TestClaimFailsOnAnUnreadableStore(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := os.Mkdir(filepath.Join(dir, "adr-claims.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim("a1", nil); err == nil {
		t.Error("Claim should fail when adr-claims.json cannot be read")
	}
}

// Broken JSON is moved aside under the lock, by the writer, never by a reader.
func TestBrokenJSONIsMovedAsideByTheWriterOnly(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	path := filepath.Join(dir, "adr-claims.json")
	write(t, path, "{not json")
	if _, err := s.List(nil); err == nil {
		t.Error("List should report broken JSON, not pretend the store is empty")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("List moved the file aside; only update may do that")
	}
	if _, err := s.Claim("a1", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".broken"); err != nil {
		t.Error("Claim should have moved the broken file aside")
	}
}

// A number already on main is released, and no longer holds the counter up.
func TestClaimReleasesNumbersThatLandedOnMain(t *testing.T) {
	s := NewStore(t.TempDir())
	onMain := map[int]bool{}
	for _, h := range handedOut {
		onMain[h.N] = true
	}
	onMain[30] = true
	n, err := s.Claim("a1", onMain)
	if err != nil {
		t.Fatal(err)
	}
	if n != 51 {
		t.Fatalf("claim %d, want 51 (max on main is 50)", n)
	}
	cs, err := s.List(onMain)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].N != 51 {
		t.Fatalf("live claims %v, want only ADR-51", cs)
	}
}

func TestReleaseFreesTheNumberForTheNextClaim(t *testing.T) {
	s := NewStore(t.TempDir())
	n, err := s.Claim("a1", nil) // no fingerprint, so no seed: this is ADR-1
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Release(n); err != nil {
		t.Fatal(err)
	}
	cs, err := s.List(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 0 {
		t.Fatalf("live claims %v, want none", cs)
	}
	if err := s.Release(n); err != nil {
		t.Fatalf("releasing twice: %v", err)
	}
	if err := s.Release(0); err == nil {
		t.Error("Release(0) should be an error")
	}
}

// The point of the lock: concurrent claimers never get the same number.
func TestConcurrentClaimsGetDifferentNumbers(t *testing.T) {
	s := NewStore(t.TempDir())
	const n = 8
	got := make([]int, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			v, err := s.Claim("a", nil)
			if err != nil {
				t.Error(err)
			}
			got[i] = v
		}(i)
	}
	close(start)
	wg.Wait()
	seen := map[int]bool{}
	for _, v := range got {
		if seen[v] {
			t.Fatalf("two claims got ADR-%d (%v)", v, got)
		}
		seen[v] = true
	}
	if len(seen) != n {
		t.Fatalf("%d distinct numbers from %d claims: %v", len(seen), n, got)
	}
}

func TestOnMainReadsDesignFromTheMainBranch(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(cmd.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	write(t, filepath.Join(dir, "DESIGN.md"), design)
	git("add", "DESIGN.md")
	git("commit", "-qm", "design")

	// An uncommitted ADR is not on main yet, so it must not count.
	write(t, filepath.Join(dir, "DESIGN.md"), design+"\n**ADR-60: not committed**\n")
	got, err := OnMain(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !got[42] || got[60] {
		t.Fatalf("OnMain %v, want 42 and not 60", got)
	}
	if _, err := OnMain(t.TempDir()); err == nil {
		t.Error("OnMain outside a repo should fail, not report an empty set")
	}
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Unknown keys in adr-claims.json, top level and per claim, survive a claim
// (ADR-62).
func TestClaimsKeepUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	raw := `{"seeded":true,"k_file":1,"claims":[{"n":5,"for":"a","created":"2026-01-01T00:00:00Z","k_claim":1}]}`
	os.WriteFile(filepath.Join(dir, "adr-claims.json"), []byte(raw), 0o644)
	if _, err := NewStore(dir).Claim("b", map[int]bool{1: true}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "adr-claims.json"))
	for _, k := range []string{"k_file", "k_claim"} {
		if !strings.Contains(string(b), `"`+k+`"`) {
			t.Errorf("%s dropped: %s", k, b)
		}
	}
}

// fleet#40: a renumber onto "the next free one in DESIGN.md" can land on a
// number another agent has claimed but not merged.
func TestTakenNamesMainAndOtherAgentsClaims(t *testing.T) {
	claims := []Claim{{N: 86, For: "admiral-tools"}, {N: 87, For: "me"}, {N: 88}}
	got := Taken([]int{85, 86, 87, 88, 89}, map[int]bool{85: true}, claims, "me")
	want := []Collision{{85, "main"}, {86, "admiral-tools"}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Taken = %v, want %v", got, want)
	}
	if s := got[1].String(); !strings.Contains(s, "ADR-86") || !strings.Contains(s, "admiral-tools") {
		t.Errorf("String() = %q", s)
	}
}

func TestBranchAddsCountsNewEntriesNotAmendments(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(cmd.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	commit := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "DESIGN.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", "DESIGN.md")
		git("commit", "-qm", "d")
	}
	git("init", "-q", "-b", "main")
	commit(design)
	git("switch", "-qc", "fleet/x")
	commit(design + "Amendment to ADR-41.\n\n**ADR-43: mine**\n")
	git("switch", "-q", "main")
	commit(design + "\n**ADR-44: theirs**\n") // main moved; not ours
	git("switch", "-q", "fleet/x")
	got, err := BranchAdds(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != 43 {
		t.Fatalf("BranchAdds = %v, want [43]", got)
	}
}
