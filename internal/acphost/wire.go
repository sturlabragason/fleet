package acphost

import (
	"context"
	"io"
	"os/exec"

	"github.com/sturlabragason/fleet/internal/acp"
)

// defaultStart is acp.Start behind the host's own interfaces.
func defaultStart(ctx context.Context, argv []string, dir string, env []string, stderr io.Writer, h handler) (agentConn, *exec.Cmd, error) {
	c, cmd, err := acp.Start(ctx, argv, dir, env, stderr, h)
	if err != nil {
		return nil, nil, err
	}
	return c, cmd, nil
}

// tracker adapts *acp.Tracker to stateTracker.
type tracker struct{ t *acp.Tracker }

func newTracker() stateTracker { return tracker{acp.NewTracker()} }

func (t tracker) Ready() trackState      { return trackState(t.t.Ready()) }
func (t tracker) PromptSent() trackState { return trackState(t.t.PromptSent()) }
func (t tracker) PromptDone(r acp.StopReason, err error) trackState {
	return trackState(t.t.PromptDone(r, err))
}
func (t tracker) Update(u acp.SessionUpdate) trackState { return trackState(t.t.Update(u)) }
func (t tracker) PermissionAsked(p acp.RequestPermissionParams) trackState {
	return trackState(t.t.PermissionAsked(p))
}
func (t tracker) PermissionAnswered(id string) trackState {
	return trackState(t.t.PermissionAnswered(id))
}
func (t tracker) Copilot(e acp.CopilotEvent) trackState { return trackState(t.t.Copilot(e)) }
func (t tracker) Exited(err error) trackState           { return trackState(t.t.Exited(err)) }
func (t tracker) State() trackState                     { return trackState(t.t.State()) }

var (
	_ agentConn   = (*acp.Client)(nil)
	_ acp.Handler = (*host)(nil)
)
