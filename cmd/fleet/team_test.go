package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/team"
)

func TestLsTeamColumn(t *testing.T) {
	now := time.Now()
	as := []*board.Agent{
		{Name: "a1", Profile: "fake", Status: "idle", StatusSince: now, PaneID: "%1", LastLine: "hi"},
		{Name: "bravo2", Profile: "fake", Status: "working", StatusSince: now, PaneID: "%2", Driver: board.DriverACP},
	}
	var plain, empty bytes.Buffer
	writeLs(&plain, as, nil, false)
	writeLs(&empty, as, map[string]team.Info{"other": {Team: "x", Color: "#89b4fa"}}, false)
	if plain.String() != empty.String() || strings.Contains(plain.String(), "TEAM") {
		t.Errorf("no listed agent in a team must keep today's table:\n%s\n%s", plain.String(), empty.String())
	}
	if !strings.HasPrefix(plain.String(), "NAME    PROFILE  DRIVER  STATUS") {
		t.Errorf("header changed: %q", plain.String())
	}
	var withTeam bytes.Buffer
	writeLs(&withTeam, as, map[string]team.Info{"bravo2": {Team: "alpha", Color: "#89b4fa"}}, false)
	lines := strings.Split(withTeam.String(), "\n")
	// Not a TTY: lipgloss drops the colour, so the columns line up as plain text.
	if lines[0] != "NAME    TEAM     PROFILE  DRIVER  STATUS   FOR  PANE  LAST LINE" ||
		!strings.HasPrefix(lines[1], "a1               fake     tmux    idle") ||
		!strings.HasPrefix(lines[2], "bravo2  ◆ alpha  fake     acp     working") {
		t.Errorf("team table:\n%s", withTeam.String())
	}
	var js bytes.Buffer
	writeLs(&js, as, map[string]team.Info{"bravo2": {Team: "alpha", Color: "#89b4fa"}}, true)
	if strings.Count(js.String(), `"team": "alpha"`) != 1 || !strings.Contains(js.String(), `"team_color": "#89b4fa"`) || !strings.Contains(js.String(), `"pane_id": "%1"`) {
		t.Errorf("json:\n%s", js.String())
	}
}

func TestConfirmClose(t *testing.T) {
	for in, want := range map[string]bool{"y\n": true, "YES\n": true, "\n": false, "n\n": false, "sure\n": false, "": false} {
		var out bytes.Buffer
		if got := confirmClose(strings.NewReader(in), &out, "alpha", []string{"a1", "a2"}); got != want {
			t.Errorf("%q: %v", in, got)
		}
		if out.String() != "members: a1, a2\nclose 2 agents in alpha? [y/N] " {
			t.Errorf("prompt %q", out.String())
		}
	}
}
