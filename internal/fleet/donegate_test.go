package fleet

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/runs"
)

// gateFleet is mailFleet with one more agent, "w", alone in a fresh repo
// with a spawn baseline, the given effort and may, and Tell recorded.
func gateFleet(t *testing.T, effort string, may ...string) (f *Fleet, dir string, told *[]string, pasted *[]string) {
	t.Helper()
	f, pasted = mailFleet(t)
	fakeAdm(f, pasted, new(true))
	f.CloseLater = func(string) error { return nil }
	var tl []string
	f.Tell = func(name, text string) error { tl = append(tl, name+": "+text); return nil }
	dir = repo(t)
	if _, err := f.Spawn("w", "cat", dir, ""); err != nil {
		t.Fatal(err)
	}
	if err := f.Store.Update(func(b *board.Board) error {
		a := b.Agents["w"]
		a.Dir, a.Effort, a.Worktree, a.Baseline = dir, effort, nil, Baseline(dir)
		if len(may) > 0 {
			a.Card = &board.Card{May: may, Result: "result.md"}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return f, dir, &tl, pasted
}

func lastReport(t *testing.T, f *Fleet) string {
	t.Helper()
	ms := f.Mail().Peek(mail.Admiral)
	if len(ms) == 0 {
		return ""
	}
	return ms[len(ms)-1].Text
}

// ADR-160: a done with the agent's own work left behind goes back to the
// agent, twice at most, then to the admiral as blocked.
func TestDoneGateBouncesLeftWorkThenEscalates(t *testing.T) {
	f, dir, told, pasted := gateFleet(t, "pro")
	os.WriteFile(filepath.Join(dir, "g"), []byte("mine"), 0o644)
	for i := 1; i <= MaxDoneBounces; i++ {
		note, err := f.Report("w", "done", "shipped")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(note, "uncommitted changes by the agent: g") || !strings.Contains(note, "fleet merge") {
			t.Fatalf("bounce %d note: %q", i, note)
		}
		if len(*told) != i || !strings.HasPrefix((*told)[i-1], "w: ") {
			t.Fatalf("bounce %d told: %q", i, *told)
		}
		if r := f.Store.Load().Agents["w"].Report; r.State != "progress" {
			t.Fatalf("bounce %d board state %q", i, r.State)
		}
		if len(*pasted) != 0 {
			t.Fatalf("admiral woken by a bounce: %q", *pasted)
		}
	}
	if _, err := f.Report("w", "done", "shipped"); err != nil {
		t.Fatal(err)
	}
	if got := lastReport(t, f); !strings.HasPrefix(got, "blocked: ") || !strings.Contains(got, "uncommitted changes by the agent: g") {
		t.Fatalf("escalation: %q", got)
	}
	if len(*told) != MaxDoneBounces || len(*pasted) != 1 {
		t.Fatalf("after escalation told %d pasted %q", len(*told), *pasted)
	}
}

func TestDoneGatePassesCleanSupersededAndShared(t *testing.T) {
	// Committed and merged: done as usual.
	f, dir, told, _ := gateFleet(t, "pro")
	os.WriteFile(filepath.Join(dir, "g"), []byte("mine"), 0o644)
	git(t, dir, "add", "g")
	git(t, dir, "commit", "-q", "-m", "g")
	if _, err := f.Report("w", "done", "shipped"); err != nil || len(*told) != 0 || !strings.HasPrefix(lastReport(t, f), "done: ") {
		t.Fatalf("clean: err %v told %q report %q", err, *told, lastReport(t, f))
	}

	// Superseded: passes the gate, the admiral sees the left work.
	f, dir, told, _ = gateFleet(t, "pro")
	os.WriteFile(filepath.Join(dir, "g"), []byte("mine"), 0o644)
	if _, err := f.Report("w", "done", "superseded by d2-legend"); err != nil || len(*told) != 0 || !strings.HasPrefix(lastReport(t, f), "done: ") {
		t.Fatalf("superseded: err %v told %q", err, *told)
	}

	// A checkout shared with another agent: fleet can't tell whose files.
	f, dir, told, _ = gateFleet(t, "light")
	if _, err := f.Spawn("x", "cat", dir, ""); err != nil {
		t.Fatal(err)
	}
	f.Store.Update(func(b *board.Board) error { b.Agents["x"].Dir, b.Agents["x"].Worktree = dir, nil; return nil })
	os.WriteFile(filepath.Join(dir, "g"), []byte("whose"), 0o644)
	if _, err := f.Report("w", "done", "reviewed"); err != nil || len(*told) != 0 || !strings.HasPrefix(lastReport(t, f), "done: ") {
		t.Fatalf("shared: err %v told %q", err, *told)
	}
}

// A standard agent without merge-local commits but can't merge: the admiral
// gets it as blocked at once, and no bounce asks it to merge.
func TestDoneGateUnmergedWithoutMergeRightEscalates(t *testing.T) {
	for _, tc := range []struct {
		name, effort string
		may          []string
		bounce       bool
	}{
		{"standard", "standard", nil, false},
		{"standard with merge-local", "standard", []string{"merge-local"}, true},
		{"pro", "pro", nil, true},
		{"review-only pro", "pro", []string{ReviewOnly}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, dir, told, _ := gateFleet(t, tc.effort, tc.may...)
			git(t, dir, "checkout", "-q", "-b", "feat")
			os.WriteFile(filepath.Join(dir, "g"), []byte("mine"), 0o644)
			git(t, dir, "add", "g")
			git(t, dir, "commit", "-q", "-m", "g")
			if _, err := f.Report("w", "done", "shipped"); err != nil {
				t.Fatal(err)
			}
			if bounced := len(*told) == 1; bounced != tc.bounce {
				t.Fatalf("bounced %v, want %v (%q)", bounced, tc.bounce, *told)
			}
			if slices.Contains(tc.may, ReviewOnly) {
				if got := lastReport(t, f); !strings.HasPrefix(got, "done: ") {
					t.Fatalf("review-only done: %q", got)
				}
				return
			}
			if !tc.bounce {
				got := lastReport(t, f)
				if !strings.HasPrefix(got, "blocked: ") || !strings.Contains(got, "merge-local") || !strings.Contains(got, "not merged into main") {
					t.Fatalf("escalation: %q", got)
				}
			}
		})
	}
}

// ADR-187: a clean done after a refusal keeps the run's lifetime refusals,
// by reason, on its run record.
func TestDoneGateRefusalsReachRunRecord(t *testing.T) {
	f, dir, _, _ := gateFleet(t, "pro")
	os.WriteFile(filepath.Join(dir, "g"), []byte("mine"), 0o644)
	if _, err := f.Report("w", "done", "shipped"); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "g")
	git(t, dir, "commit", "-q", "-m", "g")
	if _, err := f.Report("w", "done", "shipped\nretro: slow build"); err != nil {
		t.Fatal(err)
	}
	created := f.Store.Load().Agents["w"].Created
	if err := f.Close("w"); err != nil {
		t.Fatal(err)
	}
	rs, err := runs.Read(f.Home, created)
	if err != nil || len(rs) != 1 {
		t.Fatalf("runs %+v err %v", rs, err)
	}
	r := rs[0]
	if r.DoneBounces != 1 || r.DoneRefusals["uncommitted"] != 1 || !r.HasRetro || r.Exit != "closed" {
		t.Fatalf("record %+v", r)
	}
}
