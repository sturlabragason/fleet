package wall

import (
	"strconv"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/tmux"
)

func pane(id, st string, used int64) tmux.PaneInfo {
	return tmux.PaneInfo{ID: id, Status: st, Used: used}
}

func TestEvictIdleBeforeWorking(t *testing.T) {
	ps := []tmux.PaneInfo{pane("%1", "working", 1), pane("%2", "idle", 9)}
	if got, ok := EvictCandidate(ps); !ok || got != "%2" {
		t.Errorf("got %q %v, want %%2", got, ok)
	}
}

func TestEvictWorkingOnlyWhenNoIdleLeft(t *testing.T) {
	ps := []tmux.PaneInfo{pane("%1", "working", 5), pane("%2", "working", 2), pane("%3", "waiting", 1)}
	if got, ok := EvictCandidate(ps); !ok || got != "%2" {
		t.Errorf("got %q %v, want the LRU working pane %%2", got, ok)
	}
}

func TestEvictNeverTakesAttentionOrPinned(t *testing.T) {
	for _, st := range []string{"waiting", "error", "stuck"} {
		ps := []tmux.PaneInfo{pane("%1", st, 1), pane("%2", "idle", 9)}
		if got, _ := EvictCandidate(ps); got != "%2" {
			t.Errorf("status %s: evicted %q", st, got)
		}
	}
	ps := []tmux.PaneInfo{{ID: "%1", Status: "idle", Used: 1, Pin: true}, pane("%2", "working", 9)}
	if got, ok := EvictCandidate(ps); !ok || got != "%2" {
		t.Errorf("pinned pane evicted: %q %v", got, ok)
	}
}

func TestEvictLRUWithinClass(t *testing.T) {
	ps := []tmux.PaneInfo{pane("%1", "", 30), pane("%2", "unknown", 10), pane("%3", "idle", 20)}
	if got, _ := EvictCandidate(ps); got != "%2" {
		t.Errorf("got %q, want the least recently used %%2", got)
	}
}

func TestEvictAllProtected(t *testing.T) {
	ps := []tmux.PaneInfo{pane("%1", "waiting", 1), pane("%2", "error", 2), {ID: "%3", Pin: true}}
	if got, ok := EvictCandidate(ps); ok {
		t.Errorf("want ok=false, got %q", got)
	}
	if _, ok := EvictCandidate(nil); ok {
		t.Error("empty wall must not be evictable")
	}
}

// --- emphasis hysteresis -------------------------------------------------

func waiting(id string, since time.Time) tmux.PaneInfo {
	return tmux.PaneInfo{ID: id, Status: "waiting", StatusSince: since.UnixNano()}
}

func TestEmphasisPicksLongestWaiting(t *testing.T) {
	now := time.Unix(1000, 0)
	ps := []tmux.PaneInfo{
		{ID: "%1", Status: "working"},
		waiting("%2", now.Add(-20*time.Second)),
		waiting("%3", now.Add(-60*time.Second)),
	}
	if got := chooseEmphasis(ps, "", time.Time{}, now); got != "%3" {
		t.Errorf("got %q, want the longest-waiting %%3", got)
	}
}

// ADR-31: with nothing needing me the grid is even; the focused pane no
// longer gets emphasis.
func TestEmphasisNoneWithoutAttention(t *testing.T) {
	now := time.Unix(1000, 0)
	ps := []tmux.PaneInfo{{ID: "%1", Status: "working"}, {ID: "%2", Status: "idle"}}
	if got := chooseEmphasis(ps, "", time.Time{}, now); got != "" {
		t.Errorf("got %q, want no emphasis", got)
	}
	// A cached pick that was a focused, non-attention pane is dropped.
	if got := chooseEmphasis(ps, "%2", now, now); got != "" {
		t.Errorf("got %q, want the stale focus pick dropped", got)
	}
}

// ADR-31: focus never changes the pick. chooseEmphasis does not even see the
// active pane, and the tmux path must not read it into the choice: focusing a
// working pane while another waits keeps emphasis on the waiting one.
func TestEmphasisIgnoresFocus(t *testing.T) {
	c, w, agents := setupWall(t, 3)
	for _, p := range agents {
		noTarget(t, w)
		if _, err := w.Show(p); err != nil {
			t.Fatal(err)
		}
	}
	c.SetPaneOption(agents[1], "@fleet_status", "waiting")
	c.SetPaneOption(agents[1], "@fleet_status_since", strconv.FormatInt(time.Now().Add(-time.Hour).UnixNano(), 10))
	s, _ := w.Read()
	if got := w.emphasis(s); got != "" {
		t.Fatalf("emphasis is off by default, got %q", got)
	}
	w.setOpt("@fleet_emphasis", "1")
	if got := w.emphasis(s); got != agents[1] {
		t.Fatalf("emphasis %q, want the waiting %s", got, agents[1])
	}
	c.Run("select-pane", "-t", agents[2])
	if got := w.emphasis(s); got != agents[1] {
		t.Errorf("focusing %s moved emphasis to %q", agents[2], got)
	}
	c.SetPaneOption(agents[1], "@fleet_status", "working")
	s, _ = w.Read()
	if got := w.emphasis(s); got != "" {
		t.Errorf("nothing waiting: emphasis %q, want an even grid", got)
	}
}

func TestEmphasisHoldsAgainstANewWaitingPane(t *testing.T) {
	now := time.Unix(1000, 0)
	// %2 holds emphasis because it failed; %3 then starts waiting, which ranks
	// higher — but only once the current choice has held long enough.
	cur := tmux.PaneInfo{ID: "%2", Status: "error", StatusSince: now.Add(-5 * time.Minute).UnixNano()}
	fresh := waiting("%3", now.Add(-EmphasisDwell-time.Second))
	ps := []tmux.PaneInfo{{ID: "%1", Status: "working"}, cur, fresh}
	at := now.Add(-EmphasisHold + 5*time.Second)
	if got := chooseEmphasis(ps, "%2", at, now); got != "%2" {
		t.Errorf("stole emphasis before the hold: %q", got)
	}
	at = now.Add(-EmphasisHold - time.Second)
	if got := chooseEmphasis(ps, "%2", at, now); got != "%3" {
		t.Errorf("after the hold the waiting pane wins, got %q", got)
	}
}

func TestEmphasisDwellMustPass(t *testing.T) {
	now := time.Unix(1000, 0)
	ps := []tmux.PaneInfo{
		{ID: "%1", Status: "working"},
		waiting("%2", now.Add(-5*time.Minute)),
		waiting("%3", now.Add(-time.Second)), // waiting, but not yet for Dwell
	}
	at := now.Add(-time.Hour)
	if got := chooseEmphasis(ps, "%2", at, now); got != "%2" {
		t.Errorf("a pane waiting < dwell must not win: %q", got)
	}
}

func TestEmphasisMovesWhenItsPaneLeavesTheWall(t *testing.T) {
	now := time.Unix(1000, 0)
	ps := []tmux.PaneInfo{{ID: "%1", Status: "working"}, waiting("%3", now.Add(-time.Second))}
	if got := chooseEmphasis(ps, "%9", now, now); got != "%3" {
		t.Errorf("got %q, want an immediate move to %%3", got)
	}
}

func TestEmphasisMovesWhenItStopsNeedingAttention(t *testing.T) {
	now := time.Unix(1000, 0)
	ps := []tmux.PaneInfo{{ID: "%1", Status: "idle"}, {ID: "%2", Status: "working"}, waiting("%3", now)}
	if got := chooseEmphasis(ps, "%1", now, now); got != "%3" {
		t.Errorf("got %q, want %%3 once %%1 stopped waiting", got)
	}
}

// Finding 5: a pane tagged by an older build has no @fleet_status_since, so its
// dwell is unknown and must count as not satisfied rather than ~55 years.
func TestEmphasisUntaggedStatusSinceFailsDwell(t *testing.T) {
	now := time.Unix(1000, 0)
	ps := []tmux.PaneInfo{
		{ID: "%1", Status: "error", StatusSince: now.Add(-5 * time.Minute).UnixNano()},
		{ID: "%2", Status: "waiting"}, // StatusSince 0: never tagged
	}
	at := now.Add(-time.Hour) // the hold passed long ago
	if got := chooseEmphasis(ps, "%1", at, now); got != "%1" {
		t.Errorf("a pane with no status_since must not steal emphasis, got %q", got)
	}
}

// An untagged pane (an older build set @fleet_status but not _since) must not
// sit at the head of the queue and block every properly dwelled candidate.
func TestEmphasisUntaggedPaneDoesNotBlockTheQueue(t *testing.T) {
	now := time.Unix(1000, 0)
	ps := []tmux.PaneInfo{
		{ID: "%1", Status: "working"},
		{ID: "%2", Status: "waiting"}, // StatusSince 0: never tagged
		waiting("%3", now.Add(-5*time.Minute)),
	}
	if got := chooseEmphasis(ps, "", time.Time{}, now); got != "%3" {
		t.Errorf("first choice got %q, want %%3", got)
	}
	// %1 was the pick (an old focus pick) and stopped needing attention.
	if got := chooseEmphasis(ps, "%1", now.Add(-time.Hour), now); got != "%3" {
		t.Errorf("got %q, want the dwelled %%3 (the untagged %%2 must not block it)", got)
	}
}
