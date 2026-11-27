package mcp

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// External admiral (ADR-159): an MCP client outside tmux, such as Claude
// Desktop, serving as a registered admiral with no pane. Nothing types into
// its chat, fleet cannot reset or swap it, and the client's tool-call
// timeout is shorter than a tmux admiral's.

// ExternalPointer is an external admiral's MCP server instructions: the
// full rules are about 17 KB, more than a desktop client is known to keep,
// so it gets a pointer to fleet_instructions and what differs for it.
const ExternalPointer = `You are a fleet admiral driven from an MCP client outside tmux. ` +
	`At the start of each chat, call fleet_instructions once and follow the admiral rules it returns. ` +
	`What differs for you: nothing types into this chat, so your agents' reports wait in your mailbox; ` +
	`read them with fleet_reports, or block with fleet_wait (at most 50 s per call here, call it again to keep waiting). ` +
	`fleet_handoff and fleet_admiral_profile are refused: to start fresh, the user opens a new chat. ` +
	`Spawning needs the tmux fleet up (fleet up on the user's machine).`

// WaitExternal caps fleet_wait for an external admiral: Claude Desktop ends
// a tool call after about 60 s and progress does not extend it.
const WaitExternal = 50 * time.Second

// errExternalReplace refuses fleet_handoff and fleet_admiral_profile: they
// replace a tmux pane, and an external admiral has none.
var errExternalReplace = errors.New("this admiral is external (an MCP client outside tmux): fleet cannot replace it or swap its profile. Save what matters with fleet_note; the user starts a new chat to begin fresh")

// connectClient is fleet_admiral_add client=claude-desktop: `fleet connect
// claude-desktop --name NAME`, which registers the external admiral and
// writes the client's config.
func (s *Server) connectClient(ctx context.Context, a args) (string, error) {
	if a.Client != "claude-desktop" {
		return "", fmt.Errorf("client must be claude-desktop")
	}
	if !a.Confirmed {
		return "", fmt.Errorf("not confirmed: this writes Claude Desktop's config; ask the user, then call again with confirmed=true")
	}
	return cliResult(s.runFleet(ctx, "", "connect", "claude-desktop", "--name", a.Name))
}
