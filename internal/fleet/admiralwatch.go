package fleet

import (
	"strings"
	"sync"
	"time"

	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/supervisor"
	"github.com/sturlabragason/fleet/internal/tmux"
)

// followEvery is how often the resident poll checks an admiral's session
// store for a /clear (ADR-101); a store glob is too dear for every tick.
const followEvery = 15 * time.Second

// exitToldOpt marks an admiral pane whose exit was reported; every respawn
// of the pane (setAdmiralPaneOpts, fleet up) clears it.
const exitToldOpt = "@fleet_exit_told"

type admiralWatch struct {
	mu       sync.Mutex
	followed map[string]time.Time
}

// watchAdmirals runs in the resident poll: it reports an admiral pane that
// exited, once, and keeps each live admiral's session.json on the session
// its tool is writing now (ADR-101).
func (f *Fleet) watchAdmirals(panes []tmux.PaneInfo, now time.Time) {
	for _, p := range panes {
		if p.Role != "sup" || p.Session != Session {
			continue
		}
		name := p.Admiral
		if name == "" {
			name = supervisor.Main
		}
		if p.Dead {
			if f.T.PaneOption(p.ID, exitToldOpt) == "" {
				go f.admiralExited(name, p)
			}
			continue
		}
		if f.followDue(name, now) {
			go f.followAdmiral(name)
		}
	}
}

func (f *Fleet) followDue(name string, now time.Time) bool {
	f.admWatch.mu.Lock()
	defer f.admWatch.mu.Unlock()
	if f.admWatch.followed == nil {
		f.admWatch.followed = map[string]time.Time{}
	}
	if now.Sub(f.admWatch.followed[name]) < followEvery {
		return false
	}
	f.admWatch.followed[name] = now
	return true
}

func (f *Fleet) followAdmiral(name string) {
	unlock, err := supervisor.TryLockOf(f.Home, name)
	if err != nil {
		return // a reset or restart is running; it reads the session itself
	}
	defer unlock()
	from, to, err := supervisor.FollowOf(f.Home, name, f.Profiles)
	log := logx.For("admiral")
	if err != nil {
		log.Warn("follow session", "admiral", name, "err", err)
	} else if to != "" {
		log.Info("session moved", "admiral", name, "from", from, "to", to)
	}
}

// admiralExited reports a dead admiral pane once. A restart or swap holds
// the admiral lock while its new process may die and be restored, so a held
// lock skips this tick; the pane is checked again under the lock.
func (f *Fleet) admiralExited(name string, p tmux.PaneInfo) {
	unlock, err := supervisor.TryLockOf(f.Home, name)
	if err != nil {
		return
	}
	defer unlock()
	out, err := f.T.Run("display-message", "-p", "-t", p.ID, "#{pane_dead}")
	if err != nil || strings.TrimSpace(out) != "1" || f.T.PaneOption(p.ID, exitToldOpt) != "" {
		return
	}
	f.T.SetPaneOption(p.ID, exitToldOpt, "1")
	f.reportAdmiralExit(name, p)
}

// reportAdmiralExit tells the main admiral that admiral name's pane exited;
// main's own exit can only go to the log and the status line.
func (f *Fleet) reportAdmiralExit(name string, p tmux.PaneInfo) {
	how := exitHow(exitOf(p, time.Now()))
	text := "admiral " + name + "'s pane exited (" + how + ")"
	log := logx.For("admiral")
	log.Warn("admiral pane exited", "admiral", name, "pane", p.ID, "exit", how)
	if name == supervisor.Main {
		f.T.Run("display-message", "-d", "8000", "-l", "fleet: the admiral's pane exited ("+how+"); fleet admiral restart brings it back")
		return
	}
	text += "; fleet admiral restart --name " + name + " brings it back"
	if m, err := f.Mail().ReportTo(name, mail.Admiral, "blocked", text); err != nil {
		log.Error("admiral exit report failed", "admiral", name, "err", err)
	} else {
		f.wakeBox(mail.Admiral, m.ID, name, "blocked", "[fleet] "+text+".")
	}
}
