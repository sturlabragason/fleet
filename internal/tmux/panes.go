package tmux

import (
	"strconv"
	"strings"
)

// Spawn opens a detached window (creating session if needed) running cmd.
// Returns the new pane id (%N).
func (c *Client) Spawn(session, window, dir string, env []string, cmd string) (string, error) {
	return c.Run(spawnArgs(c.HasSession(session), session, window, dir, env, cmd)...)
}

// SpawnKeep is Spawn with remain-on-exit on for the new pane, set in the same
// tmux call: tmux runs one client's command list before it reaps a child, so
// a command that exits at once still leaves its dead pane behind. Set in a
// later call, the window could close first and the exit go unseen.
func (c *Client) SpawnKeep(session, window, dir string, env []string, cmd string) (string, error) {
	if Headless {
		p, err := c.Spawn(session, window, dir, env, cmd)
		if err == nil {
			c.SetPaneOption(p, "remain-on-exit", "on")
		}
		return p, err
	}
	const keep = "set-option -p remain-on-exit on"
	exists := c.HasSession(session)
	args := spawnArgs(exists, session, window, dir, env, cmd)
	if !exists {
		// new-session makes the new pane the target of the commands after it.
		return c.Run(append(args, ";", "set-option", "-p", "remain-on-exit", "on")...)
	}
	// new-window -d does not, but its after-new-window hook runs on it; the
	// hook is set for this one call.
	t := "=" + session + ":"
	args = append([]string{"set-hook", "-t", t, "after-new-window", keep, ";"}, args...)
	p, err := c.Run(append(args, ";", "set-hook", "-u", "-t", t, "after-new-window")...)
	if err != nil {
		// tmux skips the rest of the list after a failed command.
		c.Run("set-hook", "-u", "-t", t, "after-new-window")
	}
	return p, err
}

func spawnArgs(exists bool, session, window, dir string, env []string, cmd string) []string {
	var args []string
	if exists {
		args = []string{"new-window", "-d", "-t", session + ":", "-n", window}
	} else {
		args = []string{"new-session", "-d", "-s", session, "-n", window, "-x", "200", "-y", "50"}
	}
	args = append(args, "-P", "-F", "#{pane_id}")
	if dir != "" {
		args = append(args, "-c", dir)
	}
	for _, e := range env {
		args = append(args, "-e", e)
	}
	return append(args, cmd)
}

// SetPaneOption sets a pane-level option such as @fleet_agent.
func (c *Client) SetPaneOption(pane, key, val string) error {
	_, err := c.Run("set-option", "-p", "-t", pane, key, val)
	return err
}

// PaneOption reads a pane-level option, "" when it is unset.
func (c *Client) PaneOption(pane, key string) string {
	v, err := c.Run("show-options", "-pqv", "-t", pane, key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}

// UnsetPaneOption removes a pane-level option.
func (c *Client) UnsetPaneOption(pane, key string) error {
	_, err := c.Run("set-option", "-p", "-u", "-t", pane, key)
	return err
}

// Capture returns the visible text of a pane (last n lines if n>0), without
// ghost text: it captures with attributes and drops faint text that runs to
// the end of a line (Plain). Claude Code's grey prompt suggestion sits in an
// otherwise empty input box; read as typed input, it blocked every wake into
// the admiral and froze an idle agent at "working" until it read as stuck.
func (c *Client) Capture(pane string, n int) (string, error) {
	args := []string{"capture-pane", "-p", "-e", "-J", "-t", pane}
	if n > 0 {
		args = append(args, "-S", "-"+strconv.Itoa(n))
	}
	out, err := c.Run(args...)
	return Plain(out), err
}

// Paste pastes text into a pane via a buffer, then presses Enter.
func (c *Client) Paste(pane, text string, enter bool) error {
	buf := "fleet-" + strings.TrimPrefix(pane, "%")
	if _, err := c.RunInput(text, "load-buffer", "-b", buf, "-"); err != nil {
		return err
	}
	if _, err := c.Run(leaveMode(pane, "paste-buffer", "-p", "-d", "-b", buf, "-t", pane)...); err != nil {
		return err
	}
	if enter {
		return c.SendKeys(pane, "Enter")
	}
	return nil
}

// leaveMode prefixes a tmux command with `copy-mode -q`, which leaves copy,
// view or any other mode the pane is in (a no-op otherwise). A pane the user
// scrolled back is in copy mode, whose key table takes typed keys and Enter
// instead of the program: a wake line stayed in the admiral's box.
func leaveMode(pane string, cmd ...string) []string {
	return append([]string{"copy-mode", "-q", "-t", pane, ";"}, cmd...)
}

// SendKeys sends tmux key names to a pane, leaving any mode first.
// tmux 3.8 refuses send-keys while the client it picks for the command (the
// most recently active one) is attached read-only, e.g. `attach -r`; then
// the keys are written as raw bytes with paste-buffer, which has no such
// check. Keys without a byte form keep the error.
func (c *Client) SendKeys(pane string, keys ...string) error {
	_, err := c.Run(leaveMode(pane, append([]string{"send-keys", "-t", pane}, keys...)...)...)
	if err == nil || !strings.Contains(err.Error(), "client is read-only") {
		return err
	}
	raw, ok := keyBytes(keys)
	if !ok {
		return err
	}
	buf := "fleet-keys-" + strings.TrimPrefix(pane, "%")
	if _, err := c.RunInput(raw, "load-buffer", "-b", buf, "-"); err != nil {
		return err
	}
	_, err = c.Run(leaveMode(pane, "paste-buffer", "-d", "-r", "-b", buf, "-t", pane)...)
	return err
}

// keyBytes turns send-keys arguments into the bytes the pane would get:
// literal text after -l (with an optional --), Enter, BSpace, Tab, Escape.
func keyBytes(keys []string) (string, bool) {
	var sb strings.Builder
	if len(keys) > 0 && keys[0] == "-l" {
		keys = keys[1:]
		if len(keys) > 0 && keys[0] == "--" {
			keys = keys[1:]
		}
		return strings.Join(keys, ""), len(keys) > 0
	}
	for _, k := range keys {
		switch k {
		case "Enter":
			sb.WriteByte('\r')
		case "BSpace":
			sb.WriteByte(0x7f)
		case "Tab":
			sb.WriteByte('\t')
		case "Escape":
			sb.WriteByte(0x1b)
		default:
			return "", false
		}
	}
	return sb.String(), sb.Len() > 0
}

// KillPane kills one pane.
func (c *Client) KillPane(pane string) error { _, err := c.Run("kill-pane", "-t", pane); return err }

// PaneInfo is one row of list-panes.
type PaneInfo struct {
	ID, Window, Agent, Cmd string
	Session                string
	WindowID               string
	Dead                   bool
	Width, Height          int
	Used                   int64  // @fleet_used
	Role                   string // @role
	Status                 string // @fleet_status
	StatusSince            int64  // @fleet_status_since (unix nanoseconds)
	Pin                    bool   // @fleet_pin == "1"
	Last                   bool   // #{pane_last}: the pane focus came from
	Team                   string // @fleet_team
	TeamColor              string // @fleet_team_color
	DeadStatus             string // #{pane_dead_status}: exit code, "" if alive or killed by a signal
	DeadSignal             string // #{pane_dead_signal}: signal number, "" if none
	Admiral                string // @fleet_admiral: which admiral an @role=sup pane is ("" = main)
	NoReport               string // @fleet_noreport: "1" idle mid-task with no report, "user" the same for the user's agent (ADR-68)
	// OutputGen is #{pane_output_generation}, which moves on
	// every write to the pane; "" on an older tmux without it (ADR-138).
	OutputGen string
}

const paneFmt = "#{pane_id}\t#{session_name}\t#{window_id}\t#{window_name}\t#{@fleet_agent}\t#{pane_dead}\t#{pane_current_command}\t#{pane_width}\t#{pane_height}\t#{@fleet_used}\t#{@role}\t#{@fleet_status}\t#{@fleet_status_since}\t#{@fleet_pin}\t#{pane_last}\t#{@fleet_team}\t#{@fleet_team_color}\t#{pane_dead_status}\t#{pane_dead_signal}\t#{@fleet_admiral}\t#{@fleet_noreport}\t#{pane_output_generation}"

// ListPanes lists panes; target "" means all (-a), otherwise -t target (a window).
func (c *Client) ListPanes(target string) ([]PaneInfo, error) {
	args := []string{"list-panes", "-F", paneFmt}
	if target == "" {
		args = append(args, "-a")
	} else {
		args = append(args, "-t", target)
	}
	out, err := c.Run(args...)
	if err != nil {
		return nil, err
	}
	var ps []PaneInfo
	seen := map[string]bool{}
	for line := range strings.SplitSeq(out, "\n") {
		if p, ok := parsePane(strings.Split(line, "\t")); ok && !seen[p.ID] {
			seen[p.ID] = true
			ps = append(ps, p)
		}
	}
	return ps, nil
}

// A window linked into two sessions (a grouped session, ADR-108) is listed
// once per session by `list-panes -a`. ListPanes and Snapshot keep a pane's
// first row: sessions are listed by name, so the fleet session's row wins
// over that of a session whose name extends it.

// parsePane reads one paneFmt row; extra fields after it are ignored.
func parsePane(f []string) (PaneInfo, bool) {
	if len(f) < 11 {
		return PaneInfo{}, false
	}
	w, _ := strconv.Atoi(f[7])
	h, _ := strconv.Atoi(f[8])
	u, _ := strconv.ParseInt(f[9], 10, 64)
	p := PaneInfo{ID: f[0], Session: f[1], WindowID: f[2], Window: f[3], Agent: f[4], Dead: f[5] == "1", Cmd: f[6], Width: w, Height: h, Used: u, Role: f[10]}
	// Fields appended after @role stay optional so short lines still parse.
	if len(f) > 11 {
		p.Status = f[11]
	}
	if len(f) > 12 {
		p.StatusSince, _ = strconv.ParseInt(f[12], 10, 64)
	}
	if len(f) > 13 {
		p.Pin = f[13] == "1"
	}
	if len(f) > 14 {
		p.Last = f[14] == "1"
	}
	if len(f) > 16 {
		p.Team, p.TeamColor = f[15], f[16]
	}
	if len(f) > 18 {
		p.DeadStatus, p.DeadSignal = f[17], f[18]
	}
	if len(f) > 19 {
		p.Admiral = f[19]
	}
	if len(f) > 20 {
		p.NoReport = f[20]
	}
	if len(f) > 21 {
		p.OutputGen = f[21]
	}
	return p, true
}
