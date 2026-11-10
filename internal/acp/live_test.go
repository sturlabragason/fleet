package acp

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// Live tests against the real ACP servers. They spend no tokens (initialize,
// session/new, session/close) unless FLEET_ACP_LIVE=1 adds a prompt. Skipped
// in CI and where the tool is not installed.
func TestLive(t *testing.T) {
	tools := []struct {
		name     string
		argv     []string
		events   bool
		wantLoad bool
		resume   bool
	}{
		{"copilot", []string{"copilot", "--acp"}, true, true, false},
		{"agy", []string{"agy", "acp"}, false, true, true},
	}
	for _, tool := range tools {
		t.Run(tool.name, func(t *testing.T) {
			if os.Getenv("CI") != "" {
				t.Skip("CI")
			}
			if _, err := exec.LookPath(tool.argv[0]); err != nil {
				t.Skipf("%s not on PATH", tool.argv[0])
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			dir := t.TempDir()
			h := &liveHandler{}
			var stderr strings.Builder
			cl, cmd, err := Start(ctx, tool.argv, dir, nil, &stderr, h)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				cl.Close()
				select {
				case <-cl.Done():
				case <-time.After(10 * time.Second):
					cmd.Process.Kill()
					<-cl.Done()
				}
			}()
			res, err := cl.Initialize(ctx, tool.events)
			if err != nil {
				t.Fatalf("initialize: %v", err)
			}
			caps := res.AgentCapabilities
			t.Logf("%s caps: load=%v resume=%v close=%v", tool.name, caps.CanLoad(), caps.CanResume(), caps.CanClose())
			if caps.CanLoad() != tool.wantLoad || caps.CanResume() != tool.resume || !caps.CanClose() {
				t.Errorf("capabilities %+v, want load=%v resume=%v close", caps, tool.wantLoad, tool.resume)
			}
			id, err := cl.NewSession(ctx, dir, nil)
			if err != nil {
				t.Fatalf("session/new: %v", err)
			}
			if os.Getenv("FLEET_ACP_LIVE") == "1" {
				tr := NewTracker()
				tr.PromptSent()
				pr, err := cl.Prompt(ctx, id, "Reply with the single word OK and do nothing else.")
				r := pr.StopReason
				st := tr.PromptDone(r, err)
				if err != nil || r != StopEndTurn || st.Status != StatusIdle {
					t.Errorf("prompt: %v %v %+v", r, err, st)
				}
				t.Logf("%s usage: %+v", tool.name, pr.Usage)
				if h.chunks() == 0 {
					t.Error("no agent_message_chunk received")
				}
			}
			if err := cl.CloseSession(ctx, id); err != nil {
				t.Errorf("session/close: %v", err)
			}
		})
	}
}

type liveHandler struct {
	mu sync.Mutex
	n  int
}

func (h *liveHandler) SessionUpdate(n SessionNotification) {
	if n.Update.Kind == UpdateAgentMessage {
		h.mu.Lock()
		h.n++
		h.mu.Unlock()
	}
}
func (h *liveHandler) CopilotEvent(CopilotEvent) {}
func (h *liveHandler) RequestPermission(context.Context, RequestPermissionParams) (RequestPermissionResult, error) {
	return CancelledOutcome(), nil
}
func (h *liveHandler) chunks() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.n
}
