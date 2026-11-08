package tmux

import (
	"strconv"
	"strings"
)

// Spawn opens a detached window (creating session if needed) running cmd.
// Returns the new pane id (%N).
func (c *Client) Spawn(session, window, dir string, env []string, cmd string) (string, error) {
	var args []string
	if c.HasSession(session) {
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
	args = append(args, cmd)
	return c.Run(args...)
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
	if _, err := c.Run("paste-buffer", "-p", "-d", "-b", buf, "-t", pane); err != nil {
		return err
	}
	if enter {
		return c.SendKeys(pane, "Enter")
	}
	return nil
}

// SendKeys sends tmux key names to a pane.
func (c *Client) SendKeys(pane string, keys ...string) error {
	_, err := c.Run(append([]string{"send-keys", "-t", pane}, keys...)...)
	return err
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
}

const paneFmt = "#{pane_id}\t#{session_name}\t#{window_id}\t#{window_name}\t#{@fleet_agent}\t#{pane_dead}\t#{pane_current_command}\t#{pane_width}\t#{pane_height}\t#{@fleet_used}\t#{@role}\t#{@fleet_status}\t#{@fleet_status_since}\t#{@fleet_pin}\t#{pane_last}\t#{@fleet_team}\t#{@fleet_team_color}\t#{pane_dead_status}\t#{pane_dead_signal}\t#{@fleet_admiral}"

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
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 11 {
			continue
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
		ps = append(ps, p)
	}
	return ps, nil
}
