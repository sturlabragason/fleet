package status

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// read loads a fleet snap fixture (ADR-42).
func read(t *testing.T, tool, state string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", tool, state+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// feed runs screens through Match and the stuck rule the way Refresh does,
// one screen per tick, and returns the status recorded for each. A screen
// equal to the one before it counts as no change, so the no-progress clock
// keeps running.
func feed(t *testing.T, tool string, after, tick time.Duration, screens []string) ([]Status, *StuckState) {
	t.Helper()
	set := Load(t.TempDir())[tool]
	var mem *StuckState
	var out []Status
	now := time.Now()
	last, lastChange := "\x00", now
	for _, s := range screens {
		now = now.Add(tick)
		changed := s != last
		if changed {
			last, lastChange = s, now
		}
		st, line, ok := set.Match(s)
		if !ok {
			st = Working
			line = ""
		}
		var rec Status
		rec, mem = NextStuck(mem, StuckIn{Status: st, Line: line, Changed: changed, Still: now.Sub(lastChange), After: after, Now: now})
		out = append(out, rec)
	}
	return out, mem
}

func repeat(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}

// Trigger (a): the same error banner coming back after the screen moved is
// grinding, and the second sighting is stuck. The first is only an error.
func TestStuckSameErrorTwice(t *testing.T) {
	errScreen := read(t, "fake", "error")
	work := read(t, "fake", "working")
	// error, then a retry that shows work, then the very same error again.
	got, mem := feed(t, "fake", 0, time.Second, []string{errScreen, work, errScreen + "\n"})
	if got[0] != Error {
		t.Fatalf("first sighting: got %s, want error", got[0])
	}
	if got[1] != Working {
		t.Fatalf("retry: got %s, want working", got[1])
	}
	if got[2] != Stuck {
		t.Fatalf("second sighting: got %s, want stuck", got[2])
	}
	if !strings.Contains(mem.Why(), "the same error twice in a row") {
		t.Errorf("reason: %q", mem.Why())
	}
	// Derive the banner from the fixture, so recapturing it with `fleet snap`
	// cannot break this assertion.
	var banner string
	for _, l := range strings.Split(errScreen, "\n") {
		if strings.HasPrefix(l, "✖ Error:") {
			banner = l
			break
		}
	}
	if banner == "" {
		t.Fatal("the error fixture has no ✖ Error: banner")
	}
	if !strings.Contains(mem.Why(), banner) {
		t.Errorf("reason should quote the banner %q: %q", banner, mem.Why())
	}
}

// The episode lasts as long as the grind: a retry screen between two
// sightings does not end it, reaching the prompt does.
func TestStuckEpisodeSpansTheRetryLoop(t *testing.T) {
	errScreen := read(t, "fake", "error")
	work := read(t, "fake", "working")
	screens := []string{errScreen, work, errScreen + "\n", work + "\n", errScreen + "\n\n", read(t, "fake", "idle")}
	got, mem := feed(t, "fake", 0, time.Second, screens)
	for i, want := range []Status{Error, Working, Stuck, Stuck, Stuck, Idle} {
		if got[i] != want {
			t.Errorf("tick %d: got %s, want %s", i, got[i], want)
		}
	}
	if mem.Stuck() {
		t.Error("the prompt should have ended the episode")
	}
}

// An error banner sitting on a screen that never changes is one failure, not
// one per refresh: only the no-progress trigger may call that stuck.
func TestStuckErrorBannerHeldOnScreenIsOneFailure(t *testing.T) {
	got, _ := feed(t, "fake", 0, time.Second, repeat(read(t, "fake", "error"), 5))
	for i, st := range got {
		if st != Error {
			t.Fatalf("tick %d: got %s, want error", i, st)
		}
	}
}

// Trigger (b): working with a frozen screen past the threshold.
func TestStuckNoProgress(t *testing.T) {
	work := read(t, "fake", "working")
	got, mem := feed(t, "fake", 5*time.Second, time.Second, repeat(work, 8))
	for i, st := range got[:5] {
		if st != Working {
			t.Fatalf("tick %d: got %s, want working", i, st)
		}
	}
	if got[5] != Stuck {
		t.Fatalf("at the threshold: got %s, want stuck", got[5])
	}
	if mem.Why() != "working with no screen change for 5s" {
		t.Errorf("reason: %q", mem.Why())
	}
}

// 0 turns the no-progress trigger off (stuck_after = "off").
func TestStuckNoProgressOff(t *testing.T) {
	got, _ := feed(t, "fake", 0, time.Minute, repeat(read(t, "fake", "working"), 30))
	for i, st := range got {
		if st == Stuck {
			t.Fatalf("tick %d: stuck with the trigger off", i)
		}
	}
}

// The thing the rule must not do: a long build that is printing output is
// progressing, however long it runs.
func TestStuckLongProgressingBuildIsNotStuck(t *testing.T) {
	work := read(t, "fake", "working")
	var screens []string
	for i := 0; i < 60; i++ {
		screens = append(screens, work+"\n  compiling package "+strings.Repeat("x", i))
	}
	got, mem := feed(t, "fake", time.Minute, 30*time.Second, screens) // 30 minutes of build
	for i, st := range got {
		if st != Working {
			t.Fatalf("tick %d: got %s, want working", i, st)
		}
	}
	if mem.Stuck() {
		t.Error("a progressing build must never be stuck")
	}
}

// It clears when the screen changes, under both triggers, and the memory is
// dropped so the next episode starts clean.
func TestStuckClearsOnScreenChange(t *testing.T) {
	work := read(t, "fake", "working")
	errScreen := read(t, "fake", "error")
	idle := read(t, "fake", "idle")
	t.Run("no progress", func(t *testing.T) {
		screens := append(repeat(work, 6), work+"\n  linking")
		got, mem := feed(t, "fake", 5*time.Second, time.Second, screens)
		if got[5] != Stuck {
			t.Fatalf("expected stuck at tick 5, got %s", got[5])
		}
		if got[6] != Working {
			t.Errorf("after the screen moved: got %s, want working", got[6])
		}
		if mem.Stuck() {
			t.Error("episode should be over")
		}
	})
	t.Run("same error twice", func(t *testing.T) {
		got, mem := feed(t, "fake", 0, time.Second, []string{errScreen, work, errScreen + "\n", idle})
		if got[2] != Stuck {
			t.Fatalf("expected stuck at tick 2, got %s", got[2])
		}
		if got[3] != Idle {
			t.Errorf("after the screen moved: got %s, want idle", got[3])
		}
		if mem != nil {
			t.Errorf("memory should be dropped, got %+v", mem)
		}
	})
}

// A stuck episode is one episode: Since is set once and Notified survives,
// so the caller reports it once however many refreshes it spans.
func TestStuckEpisodeIsClaimedOnce(t *testing.T) {
	work := read(t, "fake", "working")
	set := Load(t.TempDir())["fake"]
	var mem *StuckState
	now := time.Now()
	var starts, notifies int
	for i := 0; i < 10; i++ {
		now = now.Add(time.Second)
		st, line, _ := set.Match(work)
		var rec Status
		rec, mem = NextStuck(mem, StuckIn{Status: st, Line: line, Still: time.Hour, After: time.Minute, Now: now})
		if rec != Stuck {
			t.Fatalf("tick %d: got %s", i, rec)
		}
		if i == 0 {
			starts++
		} else if !mem.Since.Equal(now.Add(-time.Duration(i) * time.Second)) {
			t.Errorf("tick %d: Since moved to %v", i, mem.Since)
		}
		// What Refresh does: claim the episode, then never again.
		if !mem.Notified {
			mem.Notified = true
			notifies++
		}
	}
	if starts != 1 || notifies != 1 {
		t.Errorf("starts=%d notifies=%d, want 1 and 1", starts, notifies)
	}
}

// A refresh that could not read the screen carries the board's own "stuck"
// back in; the triggers decide again rather than latching it.
func TestStuckStatusCarriedBackInDoesNotLatch(t *testing.T) {
	st, mem := NextStuck(&StuckState{Since: time.Now(), Reason: "old"}, StuckIn{Status: Stuck, Changed: true, Still: 0, After: time.Minute, Now: time.Now()})
	if st != Working {
		t.Errorf("got %s, want working", st)
	}
	if mem.Stuck() {
		t.Error("episode should have cleared")
	}
}

// vibe's home-directory startup banner stays on screen for the whole session.
// It is a warning, not a failure: counting it as an error would make every
// vibe agent in a home directory read as error and then, on its second
// screen, as stuck.
func TestVibeStartupWarningIsNotAnError(t *testing.T) {
	screen := read(t, "vibe", "idle-home-warning")
	if st, _ := Load(t.TempDir())["vibe"].Detect(screen); st != Idle {
		t.Fatalf("got %s, want idle", st)
	}
	got, mem := feed(t, "vibe", 0, time.Second, []string{screen, screen + "\n  ◆ Read main.go", screen + "\n  ◆ Read go.mod"})
	for i, st := range got {
		if st == Stuck {
			t.Fatalf("tick %d: a startup warning must not trigger the stuck rule", i)
		}
	}
	if mem != nil {
		t.Errorf("no error memory expected, got %+v", mem)
	}
}

// A banner that merely stays on screen is one failure however much else
// moves. This is the window-scan case (ADR-42): for claude, agy and vibe
// Match reports the banner for as long as it is anywhere on screen, so
// "the screen changed" is not evidence that the agent failed again.
func TestStuckHeldBannerWithMovingScreenIsOneFailure(t *testing.T) {
	base := read(t, "vibe", "error")
	var screens []string
	for i := 0; i < 10; i++ {
		screens = append(screens, base+"\n  ⎿ read "+strings.Repeat("f", i)+".go")
	}
	got, mem := feed(t, "vibe", 0, time.Second, screens)
	for i, st := range got {
		if st != Error {
			t.Fatalf("tick %d: got %s, want error", i, st)
		}
	}
	if mem.Stuck() {
		t.Errorf("one banner held on screen must not be stuck: %q", mem.Reason)
	}
	if mem.ErrSeen != 1 {
		t.Errorf("ErrSeen = %d, want 1", mem.ErrSeen)
	}
}

// An agent that failed twice, recovered and is making progress goes back to
// working: the error memory expires after ErrorForget instead of holding the
// tile red until the agent next reaches its prompt.
func TestStuckErrorMemoryExpiresWhileWorking(t *testing.T) {
	errScreen := read(t, "fake", "error")
	work := read(t, "fake", "working")
	screens := []string{errScreen, work, errScreen + "\n"}
	for i := 0; i < 10; i++ { // 10 ticks of 30s = 5 minutes of real progress
		screens = append(screens, work+"\n  compiling "+strings.Repeat("p", i))
	}
	got, mem := feed(t, "fake", 0, 30*time.Second, screens)
	if got[2] != Stuck {
		t.Fatalf("tick 2: got %s, want stuck", got[2])
	}
	if got[len(got)-1] != Working {
		t.Fatalf("after %v of progress: got %s, want working", ErrorForget, got[len(got)-1])
	}
	if mem != nil {
		t.Errorf("memory should have expired, got %+v", mem)
	}
	// It expires, it does not expire instantly: the retry still counts.
	for i, st := range got[3:7] {
		if st != Stuck {
			t.Fatalf("tick %d: got %s, want stuck (still inside ErrorForget)", i+3, st)
		}
	}
}
