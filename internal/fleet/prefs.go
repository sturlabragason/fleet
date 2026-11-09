package fleet

import (
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/prefs"
)

// Live settings (DESIGN §7, §7a, ADR-44). max_working and the [memory]
// guard are read from config.toml on every use, the way agent_mail is
// (ADR-34), so the user can retune a running fleet -- the overview, the
// admiral's MCP server, an agent -- without restarting anything. A file
// that no longer parses keeps the values loaded at start.
func (f *Fleet) livePrefs() *prefs.Prefs {
	p, err := prefs.Load()
	if err != nil {
		logx.For("fleet").Debug("config.toml; keeping the settings loaded at start", "err", err)
		return f.Prefs
	}
	return p
}

// MaxWorking is the current max_working: how many agents may work at once
// before a spawn queues, 0 = no cap.
func (f *Fleet) MaxWorking() int { return f.livePrefs().MaxWorking() }

// MemPrefs is the current [memory] section: the RAM guard settings.
func (f *Fleet) MemPrefs() prefs.Memory { return f.livePrefs().Memory() }

// DefaultProfile is the current [defaults] profile: what a spawn tool uses
// when the call names no profile ("" = none set).
func (f *Fleet) DefaultProfile() string { return f.livePrefs().DefaultProfile() }
