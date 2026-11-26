package fleet

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/plan"
	"github.com/sturlabragason/fleet/internal/tmux"
)

// planFleet runs the cheap "cat" fake profile on a fleet-test socket, with
// room for a writer and a reviewer, and records closes and tells.
func planFleet(t *testing.T) (*Fleet, *[]string) {
	t.Helper()
	home := t.TempDir()
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte("[cat]\ncommand = \"sh\"\nargs = [\"-c\", \"cat\"]\npatterns = \"fake\"\n"), 0o644)
	cfg := filepath.Join(home, "config.toml")
	os.WriteFile(cfg, []byte("[defaults]\nmax_working = 4\nready_timeout = \"100ms\"\n"), 0o644)
	t.Setenv("FLEET_CONFIG", cfg)
	f, err := New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	f.FreeMem = func() (uint64, error) { return 64 << 30, nil }
	var log []string
	f.CloseLater = func(name string) error { log = append(log, "close "+name); return f.Close(name) }
	f.Tell = func(name, text string) error { log = append(log, "tell "+name+": "+text); return nil }
	return f, &log
}

func planPhase(t *testing.T, f *Fleet, name, task string) *plan.TaskState {
	t.Helper()
	s, err := plan.Load(f.Home, name)
	if err != nil {
		t.Fatal(err)
	}
	return s.Tasks[task]
}

// A task starts when the one it is after reports done and passes its
// done-check, through the Report and done-check hooks.
func TestPlanStartsTasksAsDependenciesFinish(t *testing.T) {
	f, _ := planFleet(t)
	dir := t.TempDir()
	p, err := plan.Parse("deps", fmt.Sprintf("[task.a]\nprofile = \"cat\"\ndir = %q\nprompt = \"do a\"\ncheck = \"true\"\n\n[task.b]\nprofile = \"cat\"\ndir = %q\nprompt = \"do b\"\nafter = [\"a\"]\n", dir, dir))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.RunPlan(p, ""); err != nil {
		t.Fatal(err)
	}
	b := f.Store.Load()
	if b.Agents["a"] == nil || b.Agents["b"] != nil || b.Agents["a"].Card.Stage != StageWrite {
		t.Fatalf("after start: a=%+v b=%+v", b.Agents["a"], b.Agents["b"])
	}
	if _, err := f.Report("a", "done", "did a"); err != nil {
		t.Fatal(err)
	}
	if ph := planPhase(t, f, "deps", "a").Phase; ph != plan.Done {
		t.Fatalf("a is %s after its check passed", ph)
	}
	if f.Store.Load().Agents["b"] == nil {
		t.Fatal("b not started after a finished")
	}
	if _, err := f.Report("b", "done", "did b"); err != nil {
		t.Fatal(err)
	}
	s, _ := plan.Load(f.Home, "deps")
	if !s.Finished() || s.Tasks["b"].Phase != plan.Done {
		t.Fatalf("plan not finished:\n%s", s.Summary())
	}
	// Running it again resumes it; other tasks under the same name are refused.
	if _, err := f.RunPlan(p, ""); err != nil {
		t.Fatalf("resume: %v", err)
	}
	p.Tasks[0].Prompt = "changed"
	if _, err := f.RunPlan(p, ""); err == nil || !strings.Contains(err.Error(), "other tasks") {
		t.Fatalf("changed plan: %v", err)
	}
}

func TestPlanRefusesSameVendorReviewer(t *testing.T) {
	f, _ := planFleet(t)
	f.Profiles["c1"], f.Profiles["c2"] = f.Profiles["claude"], f.Profiles["claude"]
	p, err := plan.Parse("rv", "[task.a]\nprofile = \"c1\"\ndir = \"/tmp\"\nprompt = \"p\"\nreview = { profile = \"c2\" }\n")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.RunPlan(p, ""); err == nil || !strings.Contains(err.Error(), "same vendor") {
		t.Fatalf("same-vendor reviewer: %v", err)
	}
}

// startReview puts a plan with one reviewed task on disk with its writer
// already on the board, as if fleet had spawned it.
func startReview(t *testing.T, f *Fleet, name string, rounds int) {
	t.Helper()
	dir := t.TempDir()
	p, err := plan.Parse(name, fmt.Sprintf("[task.w]\nprofile = \"cat\"\ndir = %q\nprompt = \"write it\"\ncheck = \"true\"\nreview = { profile = \"cat\", max_rounds = %d }\n", dir, rounds))
	if err != nil {
		t.Fatal(err)
	}
	s := plan.New(p, "", time.Now())
	s.Tasks["w"] = &plan.TaskState{Phase: plan.Write, Round: 1, Since: time.Now()}
	if err := plan.Update(f.Home, name, func(*plan.State) (*plan.State, error) { return s, nil }); err != nil {
		t.Fatal(err)
	}
	f.Store.Update(func(b *board.Board) error {
		b.Agents["w"] = &board.Agent{Name: "w", Profile: "cat", Dir: dir, Created: time.Now(),
			Card: &board.Card{Check: "true", Stage: StageWrite, May: []string{ReviewOnly}}}
		return nil
	})
}

// passStage records a done report (and a passed check) for name.
func passStage(f *Fleet, name, summary string) {
	f.Store.Update(func(b *board.Board) error {
		a := b.Agents[name]
		a.Report = &board.Report{State: "done", Summary: summary, At: time.Now().Add(time.Millisecond)}
		if a.Card != nil && a.Card.Check != "" {
			a.Card.Checked = &board.Check{Pass: true}
		}
		return nil
	})
}

func TestPlanReviewFindingsGoBackUntilTheCap(t *testing.T) {
	f, log := planFleet(t)
	startReview(t, f, "cap", 2)
	passStage(f, "w", "wrote it")
	f.PlanTick()
	rev := f.Store.Load().Agents["w-review"]
	if rev == nil || rev.Card.Stage != StageReview || planPhase(t, f, "cap", "w").Phase != plan.Reviewing {
		t.Fatalf("reviewer not started: %+v", rev)
	}
	passStage(f, "w-review", "findings: off by one at a.go:3")
	f.PlanTick()
	ts := planPhase(t, f, "cap", "w")
	if ts.Phase != plan.Write || ts.Round != 2 {
		t.Fatalf("after findings: %+v", ts)
	}
	if w := f.Store.Load().Agents["w"]; w.Card.Checked != nil {
		t.Fatal("writer's done-check not re-armed")
	}
	if !slices.ContainsFunc(*log, func(l string) bool { return strings.HasPrefix(l, "tell w:") && strings.Contains(l, "off by one") }) ||
		!slices.Contains(*log, "close w-review") {
		t.Fatalf("log %q", *log)
	}
	passStage(f, "w", "fixed it")
	f.PlanTick()
	passStage(f, "w-review", "findings: still off by one")
	f.PlanTick()
	if ts := planPhase(t, f, "cap", "w"); ts.Phase != plan.Blocked {
		t.Fatalf("cap not enforced: %+v", ts)
	}
	ms, err := f.Mail().InboxFrom(mail.Admiral, "w", true, 5)
	if err != nil || len(ms) == 0 || !strings.Contains(ms[len(ms)-1].Text, "still off by one") {
		t.Fatalf("admiral not told: %v %+v", err, ms)
	}
}

func TestPlanReviewPassSendsWriterToMerge(t *testing.T) {
	f, log := planFleet(t)
	startReview(t, f, "ok", 2)
	passStage(f, "w", "wrote it")
	f.PlanTick()
	passStage(f, "w-review", "Pass. Looks right.")
	f.PlanTick()
	if ts := planPhase(t, f, "ok", "w"); ts.Phase != plan.Merge {
		t.Fatalf("after pass: %+v", ts)
	}
	w := f.Store.Load().Agents["w"]
	if slices.Contains(w.Card.May, ReviewOnly) || !slices.Contains(w.Card.May, "merge-local") || w.Card.Checked != nil {
		t.Fatalf("writer card not opened for the merge: %+v", w.Card)
	}
	if !slices.ContainsFunc(*log, func(l string) bool {
		return strings.HasPrefix(l, "tell w:") && strings.Contains(l, "Merge your branch")
	}) {
		t.Fatalf("log %q", *log)
	}
	passStage(f, "w", "merged")
	f.PlanTick()
	if ts := planPhase(t, f, "ok", "w"); ts.Phase != plan.Done {
		t.Fatalf("after merge: %+v", ts)
	}
}

// A writer that closed after its stage (nothing left to merge, e.g. no git
// repo) is done once the review passes, not blocked as missing.
func TestPlanReviewPassWithWriterGoneIsDone(t *testing.T) {
	f, _ := planFleet(t)
	startReview(t, f, "gone", 2)
	passStage(f, "w", "wrote it")
	f.PlanTick()
	f.Store.Update(func(b *board.Board) error { delete(b.Agents, "w"); return nil })
	passStage(f, "w-review", "pass")
	f.PlanTick()
	if ts := planPhase(t, f, "gone", "w"); ts.Phase != plan.Done {
		t.Fatalf("after pass with writer gone: %+v", ts)
	}
}

func TestPassVerdict(t *testing.T) {
	for in, want := range map[string]bool{"pass": true, "PASS.": true, "done: pass, nothing": true, "`pass`": true,
		"findings: x": false, "passing grade?": false, "": false} {
		if passVerdict(in) != want {
			t.Errorf("passVerdict(%q) = %v", in, !want)
		}
	}
}
