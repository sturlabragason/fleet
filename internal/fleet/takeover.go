package fleet

import (
	"fmt"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/status"
)

// Takeover moves an agent's session between drivers (ADR-82): parks it and
// wakes the same session on the other driver, so the user can work in the
// tool's own screen (toACP false: acp → tmux) and hand it back to fleet's
// ACP host later (toACP true: tmux → acp). The agent counts as the user's
// from then on, as if they had typed in its pane. Park's refusals apply: a
// working or waiting agent is refused, so no turn is cut short. If the wake
// on the new driver fails, the agent is woken on its old one.
func (f *Fleet) Takeover(name string, toACP bool) (*board.Agent, error) {
	a, ok := f.Store.Load().Agents[name]
	if !ok {
		return nil, fmt.Errorf("no agent %q", name)
	}
	from, to, want := "tmux", "acp", board.DriverACP
	if !toACP {
		from, to, want = "acp", "tmux", ""
	}
	if a.ACP() == toACP {
		return nil, fmt.Errorf("%s already runs on the %s driver", name, to)
	}
	p, ok := f.Profiles[a.Profile]
	switch {
	case !ok:
		return nil, fmt.Errorf("unknown profile %q", a.Profile)
	case toACP && !p.HasACP():
		return nil, fmt.Errorf("profile %s has no ACP server", a.Profile)
	case !toACP && len(p.Resume) == 0:
		return nil, fmt.Errorf("profile %s can't resume a session in its own screen", a.Profile)
	}
	parked := a.Status == string(status.Parked)
	if !parked {
		if err := f.Park(name, "taken over"); err != nil {
			return nil, fmt.Errorf("can't take over %s: %w", name, err)
		}
	}
	setDriver := func(d string) error {
		return f.Store.Update(func(b *board.Board) error {
			cur, ok := b.Agents[name]
			if !ok {
				return fmt.Errorf("no agent %q", name)
			}
			if cur.Status != string(status.Parked) {
				return fmt.Errorf("agent %q is %s, not parked", name, cur.Status)
			}
			cur.Driver, cur.ACPServer = d, ""
			return nil
		})
	}
	if err := setDriver(want); err != nil {
		return nil, err
	}
	// The host's state file must not stand for the tool's screen, nor a
	// tmux agent's hook state for a new host.
	f.forgetHook(name)
	w, err := f.Wake(name, true)
	if err != nil {
		if rerr := setDriver(a.Driver); rerr == nil {
			if _, werr := f.Wake(name, true); werr != nil {
				return nil, fmt.Errorf("take over %s: %w; waking it again on %s failed too: %v (it stays parked)", name, err, from, werr)
			}
			return nil, fmt.Errorf("take over %s: %w; it runs on %s again", name, err, from)
		}
		return nil, fmt.Errorf("take over %s: %w (it stays parked)", name, err)
	}
	now := time.Now()
	f.Store.Update(func(b *board.Board) error {
		if cur, ok := b.Agents[name]; ok {
			cur.UserAt = now
			w = cur
		}
		return nil
	})
	logx.For("fleet").Info("taken over", "agent", name, "from", from, "to", to, "session_id", w.SessionID, "pane", w.PaneID)
	return w, nil
}
