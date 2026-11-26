package retro

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/playbook"
	"github.com/sturlabragason/fleet/internal/runs"
)

func TestTrialLifecycle(t *testing.T) {
	home := t.TempDir()
	if _, ok, err := Load(home); ok || err != nil {
		t.Fatalf("Load on empty home = %v, %v", ok, err)
	}
	tr, err := Start(home, "p.md", "- new rule", 50, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Start(home, "q.md", "- other", 50, time.Now()); err == nil {
		t.Fatal("second Start while one runs: want error")
	}
	layer, arm, err := Assign(home, "a1")
	if err != nil || !strings.HasPrefix(arm, tr.ID+"/") {
		t.Fatalf("Assign = %q, %q, %v", layer, arm, err)
	}
	if (arm == tr.ID+"/"+Variant) != (layer == "- new rule") {
		t.Fatalf("arm %q got layer %q", arm, layer)
	}
	if err := Stop(home); err != nil {
		t.Fatal(err)
	}
	if err := Stop(home); err == nil {
		t.Fatal("Stop with no trial: want error")
	}
	again, err := Start(home, "p.md", "- new rule", 50, time.Now().Add(time.Second))
	if err != nil || again.ID == tr.ID {
		t.Fatalf("restart of the same patch: id %q (was %q), %v", again.ID, tr.ID, err)
	}
	for _, tc := range []struct {
		share int
		layer string
	}{{0, "x"}, {100, "x"}, {50, "  \n"}} {
		if _, err := Start(home, "p", tc.layer, tc.share, time.Now()); err == nil {
			t.Errorf("Start(share %d, %q): want error", tc.share, tc.layer)
		}
	}
}

func TestArmOfSplitsByShare(t *testing.T) {
	for _, share := range []int{10, 50, 90} {
		tr := Trial{ID: "abc", Share: share}
		n := 0
		for i := range 2000 {
			name := fmt.Sprintf("agent-%d", i)
			arm := tr.ArmOf(name)
			if again := (Trial{ID: "abc", Share: share}).ArmOf(name); again != arm {
				t.Fatal("ArmOf not deterministic")
			}
			if arm == Variant {
				n++
			}
		}
		if got := n * 100 / 2000; got < share-5 || got > share+5 {
			t.Errorf("share %d: %d%% got the variant", share, got)
		}
	}
}

// ledger makes n runs in arm with done of them done, rework of them
// bounced and interv of them intervened on.
func ledger(id, arm string, n, done, rework, interv int) []runs.Record {
	var out []runs.Record
	for i := range n {
		r := runs.Record{Name: fmt.Sprintf("%s-%d", arm, i), Config: &board.RunConfig{Arm: id + "/" + arm}}
		if i < done {
			r.Last = "done"
		}
		if i < rework {
			r.DoneBounces = 1
		}
		if i < interv {
			r.Interventions = map[string]int{board.KindStuckWake: 1}
		}
		out = append(out, r)
	}
	return out
}

func TestJudge(t *testing.T) {
	tr := Trial{ID: "t1"}
	cases := []struct {
		name    string
		control []runs.Record
		variant []runs.Record
		want    string
		worse   []string
	}{
		{"pending", ledger("t1", Control, 30, 25, 0, 0), ledger("t1", Variant, 5, 5, 0, 0), Pending, nil},
		{"same", ledger("t1", Control, 30, 25, 3, 3), ledger("t1", Variant, 30, 24, 4, 3), NotWorse, nil},
		{"worse done", ledger("t1", Control, 40, 38, 0, 0), ledger("t1", Variant, 40, 10, 0, 0), Worse, []string{"done rate"}},
		{"worse rework and interventions", ledger("t1", Control, 40, 38, 1, 1), ledger("t1", Variant, 40, 38, 30, 30), Worse, []string{"rework", "interventions"}},
		{"other trial ignored", ledger("t0", Control, 40, 40, 0, 0), ledger("t1", Variant, 40, 40, 0, 0), Pending, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := Judge(tr, append(tc.control, tc.variant...))
			if v.State != tc.want || fmt.Sprint(v.Worse) != fmt.Sprint(tc.worse) {
				t.Fatalf("Judge = %s %v, want %s %v", v.State, v.Worse, tc.want, tc.worse)
			}
		})
	}
}

func TestPromote(t *testing.T) {
	cases := []struct {
		name, old, layer, want string
	}{
		{"append to user layer", "- mine", "- added", "- mine\n\n- added\n"},
		{"no user layer", "", "- added", "- added\n"},
		{"replace", "- mine", playbook.ReplaceMarker + "\n- only", playbook.ReplaceMarker + "\n- only\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			user := filepath.Join(home, "worker.md")
			if tc.old != "" {
				if err := os.WriteFile(user, []byte(tc.old), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(user+".bak", []byte("- stale"), 0o600); err != nil {
				t.Fatal(err)
			}
			tr, err := Start(home, "p.md", tc.layer, 50, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if err := Promote(home, tr, Verdict{State: Pending}); err == nil {
				t.Fatal("Promote on a pending verdict: want error")
			}
			if err := Promote(home, tr, Verdict{State: NotWorse}); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(user)
			if string(got) != tc.want {
				t.Fatalf("worker.md = %q, want %q", got, tc.want)
			}
			if bak, _ := os.ReadFile(user + ".bak"); string(bak) != tc.old {
				t.Fatalf("worker.md.bak = %q, want %q", bak, tc.old)
			}
			if _, ok, _ := Load(home); ok {
				t.Fatal("trial still running after promote")
			}
		})
	}
}

// The variant arm's composed playbook is what promote produces: the
// layer sits where the user layer would put it.
func TestPromoteMatchesVariant(t *testing.T) {
	old := playbook.Ceiling
	playbook.Ceiling = os.TempDir()
	t.Cleanup(func() { playbook.Ceiling = old })
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "worker.md"), []byte("- mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	variant, _ := playbook.ResolveTrial(playbook.Pro, home, "", "- added")
	tr, err := Start(home, "p.md", "- added", 50, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := Promote(home, tr, Verdict{State: NotWorse}); err != nil {
		t.Fatal(err)
	}
	if after, _ := playbook.Resolve(playbook.Pro, home, ""); after != variant {
		t.Fatal("playbook after promote differs from the variant arm's")
	}
}

func TestRulesAnchorsInPlaybook(t *testing.T) {
	for _, r := range Rules {
		if !strings.Contains(playbook.Default(), r.Anchor) {
			t.Errorf("rule anchor %q is no longer in the worker playbook; update retro.Rules", r.Anchor)
		}
	}
}

func TestCountRulesAndCandidates(t *testing.T) {
	evs := []board.Event{
		{Kind: board.KindStuckWake},
		{Kind: board.KindDoneRefused, Detail: "unmerged"},
		{Kind: board.KindDoneRefused, Detail: "no_merge_perm"},
	}
	rep := Report{Runs: MinArmRuns, Rules: countRules(evs)}
	fired := map[string]int{}
	for _, r := range rep.Rules {
		fired[r.Anchor] = r.Fired
	}
	if fired["Escalate early"] != 1 || fired["Done means committed and merged"] != 1 || fired["Report with `fleet_report`"] != 0 {
		t.Fatalf("fired = %v", fired)
	}
	if c := rep.Candidates(); len(c) != 2 {
		t.Fatalf("candidates = %v, want the two rules that never fired", c)
	}
	rep.Runs = MinArmRuns - 1
	if c := rep.Candidates(); len(c) != 0 {
		t.Fatalf("candidates with too few runs = %v", c)
	}
}

func TestRetroThemes(t *testing.T) {
	home := t.TempDir()
	box := filepath.Join(home, "mail", "admiral")
	if err := os.MkdirAll(box, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	var lines []string
	for i, text := range []string{
		"done: x\nretro: CI was slow to start",
		"done: y\n- retro: slow CI queue again",
		"done: z\nretro: the CI runner",
		"done: old\nretro: CI CI CI",
		"progress: no retro here",
	} {
		ts := now
		if i == 3 {
			ts = now.Add(-48 * time.Hour)
		}
		lines = append(lines, fmt.Sprintf(`{"id":"%d","from":"a","to":"admiral","kind":"report","text":%q,"t":%q}`, i, text, ts.Format(time.RFC3339Nano)))
	}
	lines = append(lines, lines[0]) // the same report twice counts once
	if err := os.WriteFile(filepath.Join(box, "inbox.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := retroLines(home, now.Add(-time.Hour))
	if len(got) != 3 {
		t.Fatalf("retroLines = %q", got)
	}
	// "ci" is under four letters and "slow" is in two lines: no theme yet.
	if th := themes(got); len(th) != 0 {
		t.Fatalf("themes = %+v, want none", th)
	}
	th := themes(append(got, "slow again"))
	if len(th) != 1 || th[0].Word != "slow" || th[0].Lines != 3 {
		t.Fatalf("themes = %+v, want slow in 3 lines", th)
	}
}
