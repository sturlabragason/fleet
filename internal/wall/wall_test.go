package wall

import (
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/tmux"
)

// noTarget clears the target slot (Esc in the overview), so Show takes the
// append / class+LRU rules instead of replacing the target (DESIGN §5e).
func noTarget(t *testing.T, w *Wall) {
	t.Helper()
	if err := w.Untarget(); err != nil {
		t.Fatal(err)
	}
}

func TestFiveOntoFourSlots(t *testing.T) {
	c := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { c.KillServer() })
	sup, err := c.Run("new-session", "-d", "-s", "f", "-n", "main", "-x", "300", "-y", "80", "-P", "-F", "#{pane_id}", "cat")
	if err != nil {
		t.Skip("tmux unavailable")
	}
	ov, _ := c.Run("split-window", "-d", "-h", "-t", "f:main", "-P", "-F", "#{pane_id}", "cat")
	c.SetPaneOption(sup, "@role", "sup")
	c.SetPaneOption(ov, "@role", "ov")
	var agents []string
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("a%d", i)
		p, _ := c.Spawn("f", "ag-"+name, "", nil, "cat")
		c.SetPaneOption(p, "@fleet_agent", name)
		agents = append(agents, p)
	}
	w := New(c, "f")
	for _, p := range agents {
		noTarget(t, w)
		if _, err := w.Show(p); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	on := w.OnWall()
	if len(on) != 4 || on["a0"] {
		t.Fatalf("on wall: %v (a0 should be evicted)", on)
	}
	s, _ := w.Read()
	if s.Sup == "" || s.Ov == "" || len(s.Wall) != 4 {
		t.Fatalf("state %+v", s)
	}
	if w, _ := c.Run("display", "-p", "-t", sup, "#{pane_width}"); w != "90" {
		t.Errorf("sup width %s", w)
	}
	if err := w.SetPreset("1"); err != nil {
		t.Fatal(err)
	}
	if on := w.OnWall(); len(on) != 1 || !on["a4"] {
		t.Errorf("preset 1 kept %v, want most recent a4", on)
	}
	if err := w.SetPreset("4"); err != nil {
		t.Fatal(err)
	}
	if on := w.OnWall(); len(on) != 4 || !on["a4"] || !on["a3"] || on["a0"] {
		t.Errorf("preset 4 after 1 = %v, want a4 plus 3 most recent hidden (not a0)", on)
	}
	if err := w.ToggleSup(); err != nil {
		t.Fatal(err)
	}
	if s, _ := w.Read(); s.Sup != "" {
		t.Error("sup still visible")
	}
	if err := w.ToggleSup(); err != nil {
		t.Fatal(err)
	}
	if s, _ := w.Read(); s.Sup == "" {
		t.Error("sup not restored")
	}
}

func TestHideFocused(t *testing.T) {
	c := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { c.KillServer() })
	ov, err := c.Run("new-session", "-d", "-s", "f", "-n", "main", "-x", "200", "-y", "50", "-P", "-F", "#{pane_id}", "cat")
	if err != nil {
		t.Skip("tmux unavailable")
	}
	c.SetPaneOption(ov, "@role", "ov")
	p, _ := c.Spawn("f", "ag-a", "", nil, "cat")
	c.SetPaneOption(p, "@fleet_agent", "a")
	w := New(c, "f")
	w.Show(p) // focuses p
	if err := w.HideFocused(); err != nil || len(w.OnWall()) != 0 {
		t.Fatalf("not hidden: %v %v", err, w.OnWall())
	}
	c.Run("select-pane", "-t", ov)
	if err := w.HideFocused(); err != nil {
		t.Fatal(err)
	}
	if s, _ := w.Read(); s.Ov != ov {
		t.Error("overview must stay")
	}
}

// setupWall makes a main window with an admiral, an overview and n agent
// panes in hidden windows, on a throwaway socket.
func setupWall(t *testing.T, n int) (*tmux.Client, *Wall, []string) {
	t.Helper()
	c := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { c.KillServer() })
	sup, err := c.Run("new-session", "-d", "-s", "f", "-n", "main", "-x", "300", "-y", "80", "-P", "-F", "#{pane_id}", "cat")
	if err != nil {
		t.Skip("tmux unavailable")
	}
	ov, _ := c.Run("split-window", "-d", "-h", "-t", "f:main", "-P", "-F", "#{pane_id}", "cat")
	c.SetPaneOption(sup, "@role", "sup")
	c.SetPaneOption(ov, "@role", "ov")
	var agents []string
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("a%d", i)
		p, _ := c.Spawn("f", "ag-"+name, "", nil, "cat")
		c.SetPaneOption(p, "@fleet_agent", name)
		c.SetPaneOption(p, "@fleet_status", "idle")
		agents = append(agents, p)
	}
	return c, New(c, "f"), agents
}

// The waiting pane stays on the wall; an idle one is evicted instead.
func TestEvictionKeepsWaitingPaneOnTheWall(t *testing.T) {
	c, w, agents := setupWall(t, 5)
	c.SetPaneOption(agents[0], "@fleet_status", "waiting")
	c.SetPaneOption(agents[0], "@fleet_status_since", "1")
	for _, p := range agents {
		noTarget(t, w)
		if _, err := w.Show(p); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	on := w.OnWall()
	if len(on) != 4 || !on["a0"] || on["a1"] {
		t.Fatalf("on wall: %v (a0 waiting must stay, idle a1 must go)", on)
	}
}

// Nothing is evictable: Show must leave the wall alone and return nil.
func TestShowRefusesWhenEveryPaneNeedsAttention(t *testing.T) {
	c, w, agents := setupWall(t, 5)
	for _, p := range agents[:4] {
		noTarget(t, w)
		if _, err := w.Show(p); err != nil {
			t.Fatal(err)
		}
		c.SetPaneOption(p, "@fleet_status", "waiting")
		time.Sleep(2 * time.Millisecond)
	}
	noTarget(t, w)
	before := w.OnWall()
	ok, err := w.Show(agents[4])
	if err != nil {
		t.Fatalf("a full, all-waiting wall is a normal outcome: %v", err)
	}
	// Finding 4: a refusal must be visible to the caller, not reported as success.
	if ok {
		t.Error("Show reported success for a pane it refused to join")
	}
	after := w.OnWall()
	if len(after) != 4 || after["a4"] {
		t.Fatalf("wall changed: %v -> %v", before, after)
	}
}

// Finding 3: `mouse on` lets the user drag borders, so the real layout drifts
// from the cached @fleet_layout. An explicit apply must force a real
// select-layout (and the admiral resize that follows it); only the
// refresh-driven ApplyCached may trust the cache.
func TestApplyForcesThroughADriftedCache(t *testing.T) {
	c, w, agents := setupWall(t, 3)
	for _, p := range agents {
		noTarget(t, w)
		if _, err := w.Show(p); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	if err := w.Apply(); err != nil {
		t.Fatal(err)
	}
	if w.opt("@fleet_layout") == "" {
		t.Fatal("no cached layout after apply")
	}
	s, _ := w.Read()
	supW := func() string {
		out, _ := c.Run("display-message", "-p", "-t", s.Sup, "#{pane_width}")
		return strings.TrimSpace(out)
	}
	want := supW()
	// The user drags the admiral border; the rendered layout is unchanged,
	// so @fleet_layout still matches and a cached apply is a no-op.
	c.Run("resize-pane", "-t", s.Sup, "-x", "60")
	if supW() == want {
		t.Skip("tmux did not drift the layout")
	}
	if err := w.ApplyCached(); err != nil {
		t.Fatal(err)
	}
	if supW() == want {
		t.Error("ApplyCached re-applied despite an unchanged cached layout")
	}
	// The explicit apply forces through and un-wedges the wall.
	if err := w.Apply(); err != nil {
		t.Fatal(err)
	}
	if got := supW(); got != want {
		t.Errorf("Apply did not force through the drifted cache: sup width %s, want %s", got, want)
	}
}

// slotOf returns the wall panes of the main window as "id WxH" in order.
func slots(t *testing.T, c *tmux.Client) []string {
	t.Helper()
	ps, err := c.ListPanes("f:main")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range ps {
		if p.Role == "wall" {
			out = append(out, fmt.Sprintf("%s %dx%d", p.ID, p.Width, p.Height))
		}
	}
	return out
}

// fillWall shows the first four agents and leaves the wall full.
func fillWall(t *testing.T, w *Wall, agents []string) {
	t.Helper()
	for _, p := range agents[:4] {
		noTarget(t, w)
		if ok, err := w.Show(p); err != nil || !ok {
			t.Fatalf("show %s: %v %v", p, ok, err)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// slotIndex is the position of pane among the wall slots, -1 if absent.
func slotIndex(sl []string, pane string) int {
	for i, x := range sl {
		if strings.HasPrefix(x, pane+" ") {
			return i
		}
	}
	return -1
}

// DESIGN §5e: a card goes into the target slot, in place — same position,
// same size, nothing else moves — and the target stays on that slot.
func TestShowReplacesTheTarget(t *testing.T) {
	c, w, agents := setupWall(t, 5)
	fillWall(t, w, agents)
	x := agents[1]
	w.setOpt("@fleet_target", x) // I clicked a1, then a card
	before := slots(t, c)
	idx := slotIndex(before, x)
	if idx < 0 {
		t.Fatalf("%s not on the wall: %v", x, before)
	}
	ok, err := w.Show(agents[4])
	if err != nil || !ok {
		t.Fatalf("show: %v %v", ok, err)
	}
	after := slots(t, c)
	if len(after) != len(before) {
		t.Fatalf("wall size changed: %v -> %v", before, after)
	}
	// The incoming pane sits in exactly the slot a1 had.
	wantSlot := strings.Replace(before[idx], x+" ", agents[4]+" ", 1)
	if after[idx] != wantSlot {
		t.Errorf("slot %d is %q, want %q", idx, after[idx], wantSlot)
	}
	// Nothing else moved or resized.
	for i := range before {
		if i != idx && before[i] != after[i] {
			t.Errorf("slot %d moved: %q -> %q", i, before[i], after[i])
		}
	}
	// a1 went back to its own window, named after the agent.
	if on := w.OnWall(); on["a1"] || !on["a4"] {
		t.Errorf("on wall: %v", on)
	}
	win, _ := c.Run("display-message", "-p", "-t", x, "#{window_name}")
	if strings.TrimSpace(win) != "ag-a1" {
		t.Errorf("replaced pane landed in window %q, want ag-a1", win)
	}
	if got := w.opt("@fleet_target"); got != agents[4] {
		t.Errorf("target %q, want it to stay on the slot (now %s)", got, agents[4])
	}
	if got := w.opt("@fleet_undo"); got != agents[4]+" "+x+" a1" {
		t.Errorf("@fleet_undo %q, want %q", got, agents[4]+" "+x+" a1")
	}
	if got := w.TargetAgent(); got != "a4" {
		t.Errorf("TargetAgent %q, want a4", got)
	}
}

// The target is replaced even when the wall has free slots (before ADR-31 a
// card appended there, and only a full wall reused a slot).
func TestShowReplacesTheTargetOnAWallWithRoom(t *testing.T) {
	c, w, agents := setupWall(t, 3)
	for _, p := range agents[:2] {
		noTarget(t, w)
		if ok, err := w.Show(p); err != nil || !ok {
			t.Fatalf("show %s: %v %v", p, ok, err)
		}
	}
	w.setOpt("@fleet_target", agents[0])
	before := slots(t, c)
	if ok, err := w.Show(agents[2]); err != nil || !ok {
		t.Fatalf("show: %v %v", ok, err)
	}
	after := slots(t, c)
	if len(after) != 2 {
		t.Fatalf("appended instead of replacing: %v -> %v", before, after)
	}
	if on := w.OnWall(); on["a0"] || !on["a1"] || !on["a2"] {
		t.Errorf("on wall: %v, want a2 in a0's slot", on)
	}
	if after[slotIndex(before, agents[0])] != strings.Replace(before[slotIndex(before, agents[0])], agents[0]+" ", agents[2]+" ", 1) {
		t.Errorf("a2 not in a0's slot: %v -> %v", before, after)
	}
}

// Place (team show) ignores the target slot: members fill the wall instead of
// replacing each other in ▶.
func TestPlaceIgnoresTheTarget(t *testing.T) {
	_, w, agents := setupWall(t, 3)
	for _, p := range agents {
		w.setOpt("@fleet_target", agents[0])
		if ok, err := w.Place(p); err != nil || !ok {
			t.Fatalf("place %s: %v %v", p, ok, err)
		}
	}
	if on := w.OnWall(); !on["a0"] || !on["a1"] || !on["a2"] {
		t.Errorf("on wall: %v, want all three", on)
	}
}

// An explicit card click beats class-1 protection: a waiting target is still
// replaced.
func TestShowReplacesAWaitingTarget(t *testing.T) {
	c, w, agents := setupWall(t, 5)
	fillWall(t, w, agents)
	x := agents[1]
	c.SetPaneOption(x, "@fleet_status", "waiting")
	c.SetPaneOption(x, "@fleet_status_since", "1")
	w.setOpt("@fleet_target", x)
	if ok, err := w.Show(agents[4]); err != nil || !ok {
		t.Fatalf("show: %v %v", ok, err)
	}
	if on := w.OnWall(); on["a1"] || !on["a4"] {
		t.Errorf("a waiting target must still be replaced: %v", on)
	}
}

// A pinned target is never replaced: with room the card appends, on a full
// wall the class/LRU fallback picks an evictable pane.
func TestShowDoesNotReplaceAPinnedTarget(t *testing.T) {
	c, w, agents := setupWall(t, 5)
	for _, p := range agents[:3] {
		noTarget(t, w)
		if ok, err := w.Show(p); err != nil || !ok {
			t.Fatalf("show %s: %v %v", p, ok, err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	x := agents[1]
	c.SetPaneOption(x, "@fleet_pin", "1")
	w.setOpt("@fleet_target", x)
	if ok, err := w.Show(agents[3]); err != nil || !ok {
		t.Fatalf("show: %v %v", ok, err)
	}
	if on := w.OnWall(); len(on) != 4 || !on["a1"] || !on["a3"] {
		t.Errorf("pinned target with room: want a3 appended, got %v", on)
	}
	w.setOpt("@fleet_target", x)
	if ok, err := w.Show(agents[4]); err != nil || !ok {
		t.Fatalf("show: %v %v", ok, err)
	}
	on := w.OnWall()
	if !on["a1"] {
		t.Error("pinned target was replaced")
	}
	if !on["a4"] || len(on) != 4 || on["a0"] {
		t.Errorf("fallback should evict the LRU a0: %v", on)
	}
}

// A target that left the wall counts as no target: the old rules apply.
func TestShowWithAStaleTargetUsesTheOldRules(t *testing.T) {
	c, w, agents := setupWall(t, 5)
	fillWall(t, w, agents)
	w.setOpt("@fleet_target", agents[4]) // hidden, not on the wall
	if ok, err := w.Show(agents[4]); err != nil || !ok {
		t.Fatalf("show: %v %v", ok, err)
	}
	if on := w.OnWall(); len(on) != 4 || on["a0"] || !on["a4"] {
		t.Errorf("stale target: want LRU a0 evicted, got %v", on)
	}
	// Hiding the target clears it.
	if err := w.Hide(agents[4]); err != nil {
		t.Fatal(err)
	}
	if got := w.opt("@fleet_target"); got != "" {
		t.Errorf("hidden target kept: %q", got)
	}
	_ = c
}

// With no target and a full wall, the pane a card would evict carries
// @fleet_next_out (the dim "next out" in its title); a target clears it.
func TestNextOutMarksTheEvictionCandidate(t *testing.T) {
	_, w, agents := setupWall(t, 5)
	fillWall(t, w, agents)
	noTarget(t, w)
	if got := w.opt("@fleet_next_out"); got != agents[0] {
		t.Errorf("next out %q, want the LRU %s", got, agents[0])
	}
	w.setOpt("@fleet_target", agents[2])
	if err := w.ApplyCached(); err != nil {
		t.Fatal(err)
	}
	if got := w.opt("@fleet_next_out"); got != "" {
		t.Errorf("next out %q with a target, want none", got)
	}
}

// Add appends without replacing; on a full wall the preset steps up, at 9 it
// refuses.
func TestAddStepsThePresetUp(t *testing.T) {
	_, w, agents := setupWall(t, 10)
	fillWall(t, w, agents) // preset 4, full, target on a3
	if ok, err := w.Add(agents[4]); err != nil || !ok {
		t.Fatalf("add: %v %v", ok, err)
	}
	if on := w.OnWall(); len(on) != 5 {
		t.Errorf("add replaced instead of appending: %v", on)
	}
	if p := w.opt("@fleet_wall"); p != "6" {
		t.Errorf("preset %q, want 6", p)
	}
	for _, p := range agents[5:9] {
		if ok, err := w.Add(p); err != nil || !ok {
			t.Fatalf("add %s: %v %v", p, ok, err)
		}
	}
	if on, p := w.OnWall(), w.opt("@fleet_wall"); len(on) != 9 || p != "9" {
		t.Fatalf("on wall %d preset %s, want 9 and 9", len(on), p)
	}
	ok, err := w.Add(agents[9])
	if err != nil || ok {
		t.Errorf("tenth add: %v %v, want a refusal", ok, err)
	}
	if on := w.OnWall(); len(on) != 9 || on["a9"] {
		t.Errorf("tenth add changed the wall: %v", on)
	}
}

func TestPresetUp(t *testing.T) {
	for from, want := range map[string]string{"1": "2", "2": "4", "4": "6", "m": "6", "6": "9", "9": ""} {
		if got := presetUp[from]; got != want {
			t.Errorf("%s -> %q, want %q", from, got, want)
		}
	}
}

// Next walks the waiting agents through the target slot, longest wait first,
// wrapping both ways.
func TestNextWalksWaitingAgents(t *testing.T) {
	c, w, agents := setupWall(t, 4)
	if got, _, err := w.Next(1); err != nil || got != "" {
		t.Fatalf("nothing waiting: %q %v", got, err)
	}
	// a2 waited longest, then a0, then a3. a1 is idle.
	for p, since := range map[string]string{agents[2]: "100", agents[0]: "200", agents[3]: "300"} {
		c.SetPaneOption(p, "@fleet_status", "waiting")
		c.SetPaneOption(p, "@fleet_status_since", since)
	}
	noTarget(t, w)
	if ok, err := w.Show(agents[1]); err != nil || !ok { // target holds a1, not waiting
		t.Fatal(ok, err)
	}
	var got []string
	for _, d := range []int{1, 1, 1, 1, -1, -1} {
		name, ok, err := w.Next(d)
		if err != nil || !ok {
			t.Fatal(ok, err)
		}
		got = append(got, name)
	}
	if s := strings.Join(got, ","); s != "a2,a0,a3,a2,a3,a0" {
		t.Errorf("order %s, want a2,a0,a3,a2,a3,a0", s)
	}
	if on := w.OnWall(); len(on) != 1 || !on["a0"] {
		t.Errorf("Next must cycle one slot: %v", on)
	}
}

// A re-layout must move neither the user nor #{pane_last}: a moved focus
// would, through the pane-focus-in hook, move the target slot too.
func TestApplyLeavesFocusAndLastPaneAlone(t *testing.T) {
	c, w, agents := setupWall(t, 3)
	noTarget(t, w)
	for _, p := range agents {
		if _, err := w.Show(p); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	s, _ := w.Read()
	// I was in a wall pane, then went to the overview to pick the next agent.
	c.Run("select-pane", "-t", s.Wall[0].ID)
	c.Run("select-pane", "-t", s.Ov)
	active := func() string {
		out, _ := c.Run("display-message", "-p", "-t", "f:main", "#{pane_id}")
		return strings.TrimSpace(out)
	}
	last := func() string {
		ps, _ := c.ListPanes("f:main")
		for _, p := range ps {
			if p.Last {
				return p.ID
			}
		}
		return ""
	}
	wantActive, wantLast := active(), last()
	if wantLast != s.Wall[0].ID {
		t.Fatalf("setup: pane_last is %s, want the wall pane %s", wantLast, s.Wall[0].ID)
	}
	for _, apply := range []func() error{w.Apply, w.ApplyCached, w.Apply} {
		if err := apply(); err != nil {
			t.Fatal(err)
		}
		if got := active(); got != wantActive {
			t.Fatalf("apply moved the user: active %s, want %s", got, wantActive)
		}
		if got := last(); got != wantLast {
			t.Fatalf("apply rewrote pane_last: %s, want %s", got, wantLast)
		}
	}
}

// Two applies racing from independent snapshots would scramble pane order, so
// the second one backs off while the first holds the lock.
func TestApplyLockKeepsConcurrentAppliesOut(t *testing.T) {
	c, w, agents := setupWall(t, 3)
	for _, p := range agents {
		noTarget(t, w)
		if _, err := w.Show(p); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	if err := w.Apply(); err != nil {
		t.Fatal(err)
	}
	s, _ := w.Read()
	// Another process is mid-apply.
	w.setOpt("@fleet_applying", strconv.FormatInt(time.Now().Add(applyLockTTL).UnixNano(), 10))
	c.Run("resize-pane", "-t", s.Sup, "-x", "60")
	supW := func() string {
		out, _ := c.Run("display-message", "-p", "-t", s.Sup, "#{pane_width}")
		return strings.TrimSpace(out)
	}
	if supW() != "60" {
		t.Skip("tmux refused the drag")
	}
	if err := w.Apply(); err != nil {
		t.Fatal(err)
	}
	if supW() != "60" {
		t.Error("apply ran while another process held the lock")
	}
	// A stale lock (a crashed apply) must not block the wall forever.
	w.setOpt("@fleet_applying", strconv.FormatInt(time.Now().Add(-time.Minute).UnixNano(), 10))
	if err := w.Apply(); err != nil {
		t.Fatal(err)
	}
	if supW() == "60" {
		t.Error("a stale lock wedged the wall")
	}
}

// geometry is every pane of the main window in window order: id, left, top,
// width, height.
func geometry(t *testing.T, c *tmux.Client) []string {
	t.Helper()
	out, err := c.Run("list-panes", "-t", "f:main", "-F", "#{pane_id} #{pane_left} #{pane_top} #{pane_width} #{pane_height}")
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(out), "\n")
}

func activePane(c *tmux.Client) string {
	out, _ := c.Run("display-message", "-p", "-t", "f:main", "#{pane_id}")
	return strings.TrimSpace(out)
}

// focusHook is what the pane-focus-in hook does (cmd/fleet/up.go): a focused
// wall pane becomes the target, then `fleet wall refresh` (ApplyCached).
func focusHook(t *testing.T, c *tmux.Client, w *Wall, pane string) {
	t.Helper()
	if _, err := c.Run("select-pane", "-t", pane); err != nil {
		t.Fatal(err)
	}
	role, _ := c.Run("display-message", "-p", "-t", pane, "#{@role}")
	if strings.TrimSpace(role) == "wall" {
		w.setOpt("@fleet_target", pane)
	}
	if err := w.ApplyCached(); err != nil {
		t.Fatal(err)
	}
}

// Clicking moves nothing: focusing a wall pane, then the overview, leaves
// every pane's position, size and window order alone (the user: "whenever I
// click something it moves the panels around"). A card into the ▶ pane then
// changes only that slot's occupant.
func TestClicksDoNotMovePanes(t *testing.T) {
	c, w, agents := setupWall(t, 5)
	fillWall(t, w, agents)
	w.setOpt("@fleet_emphasis", "1") // the harder case: clicks must not move panes even with emphasis on
	c.SetPaneOption(agents[2], "@fleet_status", "waiting")
	c.SetPaneOption(agents[2], "@fleet_status_since", strconv.FormatInt(time.Now().Add(-time.Minute).UnixNano(), 10))
	if err := w.ApplyCached(); err != nil { // emphasis settles on the waiting pane
		t.Fatal(err)
	}
	s, _ := w.Read()
	before := geometry(t, c)
	focusHook(t, c, w, agents[0])
	if got := geometry(t, c); strings.Join(got, "\n") != strings.Join(before, "\n") {
		t.Fatalf("focusing a wall pane moved panes:\n%s\n->\n%s", strings.Join(before, "\n"), strings.Join(got, "\n"))
	}
	if got := w.opt("@fleet_target"); got != agents[0] {
		t.Fatalf("target %q, want %s", got, agents[0])
	}
	focusHook(t, c, w, s.Ov)
	if got := geometry(t, c); strings.Join(got, "\n") != strings.Join(before, "\n") {
		t.Fatalf("focusing the overview moved panes:\n%s\n->\n%s", strings.Join(before, "\n"), strings.Join(got, "\n"))
	}
	// A card into ▶ (a0's slot): a4 takes a0's exact geometry, all else stays.
	if ok, err := w.Show(agents[4]); err != nil || !ok {
		t.Fatalf("show: %v %v", ok, err)
	}
	if err := w.ApplyCached(); err != nil {
		t.Fatal(err)
	}
	after := geometry(t, c)
	if len(after) != len(before) {
		t.Fatalf("pane count changed:\n%v\n->\n%v", before, after)
	}
	for i := range before {
		want := before[i]
		if strings.HasPrefix(want, agents[0]+" ") {
			want = agents[4] + strings.TrimPrefix(want, agents[0])
		}
		if after[i] != want {
			t.Errorf("pane %d: %q -> %q, want %q", i, before[i], after[i], want)
		}
	}
}

// The overview keeps keyboard focus through repeated Tab (Next) and card
// clicks (KeepFocus Show): a second key press must reach the overview, not
// the agent the first one put on the wall.
func TestNextAndKeepFocusShowLeaveTheOverviewFocused(t *testing.T) {
	c, w, agents := setupWall(t, 6)
	fillWall(t, w, agents)
	for i, p := range agents[3:6] {
		c.SetPaneOption(p, "@fleet_status", "waiting")
		c.SetPaneOption(p, "@fleet_status_since", strconv.Itoa(100+i))
	}
	s, _ := w.Read()
	focusHook(t, c, w, agents[1]) // a1 is ▶
	focusHook(t, c, w, s.Ov)
	for i := 0; i < 2; i++ {
		name, ok, err := w.Next(1)
		if err != nil || !ok || name == "" {
			t.Fatalf("next %d: %q %v %v", i, name, ok, err)
		}
		if got := activePane(c); got != s.Ov {
			t.Fatalf("next %d (%s) moved focus to %s, want the overview %s", i, name, got, s.Ov)
		}
	}
	w.KeepFocus = true
	for _, p := range []string{agents[0], agents[5], agents[2]} { // on wall, swap in, on wall
		if ok, err := w.Show(p); err != nil || !ok {
			t.Fatalf("show %s: %v %v", p, ok, err)
		}
		if got := activePane(c); got != s.Ov {
			t.Fatalf("show %s moved focus to %s, want the overview %s", p, got, s.Ov)
		}
		if got := w.opt("@fleet_target"); got != p {
			t.Errorf("show %s: target %q", p, got)
		}
	}
	noTarget(t, w)
	if err := w.Hide(agents[0]); err != nil { // room again: the join path
		t.Fatal(err)
	}
	if ok, err := w.Show(agents[0]); err != nil || !ok {
		t.Fatalf("join: %v %v", ok, err)
	}
	if got := activePane(c); got != s.Ov {
		t.Errorf("join moved focus to %s, want the overview %s", got, s.Ov)
	}
	if got := w.opt("@fleet_target"); got != agents[0] {
		t.Errorf("join: target %q", got)
	}
}
