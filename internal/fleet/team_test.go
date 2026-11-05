package fleet

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/theme"
	"github.com/sturlabragason/fleet/internal/tmux"
)

func TestTeamTagsSendClose(t *testing.T) {
	home := t.TempDir()
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "no-config.toml"))
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	os.WriteFile(filepath.Join(home, "tools.toml"), []byte(`[yn]
command = "sh"
args = ["-c", "printf 'Approve? [y/n] '; cat"]
patterns = "fake"
`), 0o644)
	f, err := New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a1", "a2", "a3"} {
		if _, err := f.Spawn(n, "yn", home, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.Teams.Add("alpha", "a1", "a2", "ghost"); err != nil {
		t.Fatal(err)
	}
	tags := func(agent string) string {
		p, _ := f.Pane(agent)
		out, _ := tc.Run("display-message", "-p", "-t", p, "#{@fleet_team}|#{@fleet_team_color}")
		return out
	}
	f.Refresh()
	for _, n := range []string{"a1", "a2"} {
		if got := tags(n); got != "alpha|"+theme.Blue {
			t.Errorf("%s tags %q", n, got)
		}
	}
	if got := tags("a3"); got != "|" {
		t.Errorf("a3 tags %q", got)
	}
	ps, _ := tc.ListPanes("")
	for _, p := range ps {
		if p.Agent == "a1" && (p.Team != "alpha" || p.TeamColor != theme.Blue) {
			t.Errorf("PaneInfo %+v", p)
		}
	}

	rs, err := f.TeamSend("alpha", "hello team", true)
	if err != nil {
		t.Fatal(err)
	}
	if got := FormatResults("sent", rs); got != "sent: a1, a2; skipped: ghost (not running)" {
		t.Errorf("send results %q", got)
	}
	for _, n := range []string{"a1", "a2"} {
		var out string
		for i := 0; i < 30 && !strings.Contains(out, "hello team"); i++ {
			time.Sleep(100 * time.Millisecond)
			out, _ = f.Peek(n, 0)
		}
		if !strings.Contains(out, "hello team") {
			t.Errorf("%s never got the text: %q", n, out)
		}
	}
	if out, _ := f.Peek("a3", 0); strings.Contains(out, "hello team") {
		t.Error("a3 is not in the team but got the text")
	}

	w := newFakeWall(tc, 1)
	rs, _ = f.TeamShow(w, "alpha")
	if got := FormatResults("on wall", rs); got != "on wall: a1; skipped: a2 (did not fit), ghost (not running)" || len(w.shown) != 1 {
		t.Errorf("show: %q %v", got, w.shown)
	}
	// The wall evicts a teammate to make room: report what is really on it.
	ev := newFakeWall(tc, 3)
	ev.on["a1"], ev.evict = true, "a1"
	rs, _ = f.TeamShow(ev, "alpha")
	if got := FormatResults("on wall", rs); got != "on wall: a2; skipped: a1 (did not fit), ghost (not running)" {
		t.Errorf("show after eviction: %q", got)
	}
	if _, err := f.TeamShow(&fakeWall{err: fmt.Errorf("no wall")}, "alpha"); err == nil {
		t.Error("unreadable wall not reported")
	}
	rs, _ = f.TeamHide(w, "alpha")
	if got := FormatResults("off wall", rs); got != "off wall: a1; skipped: a2 (not on the wall), ghost (not running)" {
		t.Errorf("hide: %q", got)
	}

	f.Teams.Remove("alpha", "a2")
	f.Refresh()
	if got := tags("a2"); got != "|" {
		t.Errorf("a2 tags after remove %q", got)
	}

	if err := f.Close("a1"); err != nil {
		t.Fatal(err)
	}
	if tm, _ := f.Teams.Of("a1"); tm != "" {
		t.Errorf("close left a1 in %q", tm)
	}

	f.Teams.Add("beta", "a2", "a3")
	rs, err = f.TeamClose("beta")
	if err != nil || FormatResults("closed", rs) != "closed: a2, a3" {
		t.Fatalf("team close %v %v", rs, err)
	}
	if len(f.Store.Load().Agents) != 0 {
		t.Error("team close left agents")
	}
	if _, err := f.Teams.Members("beta"); err != nil {
		t.Error("team close removed the team")
	}
}

type fakeWall struct {
	tc            *tmux.Client
	on            map[string]bool
	slots         int
	evict         string // agent the next Show pushes off the wall
	err           error
	shown, hidden []string
}

func newFakeWall(tc *tmux.Client, slots int) *fakeWall {
	return &fakeWall{tc: tc, on: map[string]bool{}, slots: slots}
}

func (w *fakeWall) Place(p string) (bool, error) {
	w.shown = append(w.shown, p)
	name, _ := w.tc.Run("display-message", "-p", "-t", p, "#{@fleet_agent}")
	w.on[strings.TrimSpace(name)] = true
	if w.evict != "" {
		delete(w.on, w.evict)
		w.evict = ""
	}
	return true, nil
}
func (w *fakeWall) Hide(p string) error     { w.hidden = append(w.hidden, p); return nil }
func (w *fakeWall) OnWall() map[string]bool { return w.on }
func (w *fakeWall) Slots() (int, error)     { return w.slots, w.err }
