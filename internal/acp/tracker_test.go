package acp

import (
	"errors"
	"testing"
)

func TestTracker(t *testing.T) {
	ask := func(id, title string) func(*Tracker) State {
		return func(tr *Tracker) State {
			return tr.PermissionAsked(RequestPermissionParams{
				ToolCall: ToolCallRef{ToolCallID: id, Title: title},
				Options:  []PermissionOption{{OptionID: "a", Name: "Allow"}, {OptionID: "r", Name: "Reject"}},
			})
		}
	}
	answer := func(id string) func(*Tracker) State {
		return func(tr *Tracker) State { return tr.PermissionAnswered(id) }
	}
	sent := func(tr *Tracker) State { return tr.PromptSent() }
	done := func(r StopReason) func(*Tracker) State {
		return func(tr *Tracker) State { return tr.PromptDone(r, nil) }
	}
	failed := func(tr *Tracker) State { return tr.PromptDone("", errors.New("x")) }
	upd := func(kind string) func(*Tracker) State {
		return func(tr *Tracker) State { return tr.Update(SessionUpdate{Kind: kind}) }
	}
	cop := func(typ, agent string) func(*Tracker) State {
		return func(tr *Tracker) State { return tr.Copilot(CopilotEvent{Type: typ, AgentID: agent}) }
	}
	ready := func(tr *Tracker) State { return tr.Ready() }
	exited := func(tr *Tracker) State { return tr.Exited(errors.New("exit 3")) }

	tests := []struct {
		name  string
		steps []func(*Tracker) State
		want  State
	}{
		{"new is starting", nil, State{Status: "starting", Event: "acp:start"}},
		{"replayed history while starting stays starting", []func(*Tracker) State{upd(UpdateAgentMessage), upd(UpdateToolCall)}, State{Status: "starting", Event: "acp:start"}},
		{"ready is idle", []func(*Tracker) State{ready}, State{Status: "idle", Event: "acp:ready"}},
		{"replay then ready is idle", []func(*Tracker) State{upd(UpdateAgentMessage), ready}, State{Status: "idle", Event: "acp:ready"}},
		{"ready after prompt keeps working", []func(*Tracker) State{sent, ready}, State{Status: "working", Event: "acp:prompt"}},
		{"ready while waiting keeps waiting", []func(*Tracker) State{ask("t", "A"), ready}, State{Status: "waiting", WaitingFor: "A: Allow / Reject", Event: "acp:permission"}},
		{"ready after exit stays error", []func(*Tracker) State{exited, ready}, State{Status: "error", Event: "acp:exited"}},
		{"update after ready is working", []func(*Tracker) State{ready, upd(UpdateToolCall)}, State{Status: "working", Event: "acp:tool_call"}},
		{"prompt sent", []func(*Tracker) State{sent}, State{Status: "working", Event: "acp:prompt"}},
		{"prompt done", []func(*Tracker) State{sent, done(StopEndTurn)}, State{Status: "idle", Event: "acp:end_turn"}},
		{"prompt cancelled is idle", []func(*Tracker) State{sent, done(StopCancelled)}, State{Status: "idle", Event: "acp:cancelled"}},
		{"prompt error", []func(*Tracker) State{sent, failed}, State{Status: "error", Event: "acp:prompt-error"}},
		{"steer rejected keeps working", []func(*Tracker) State{sent, sent, failed}, State{Status: "working", Event: "acp:prompt"}},
		{"steer rejected keeps the question open", []func(*Tracker) State{sent, ask("t1", "A"), sent, failed}, State{Status: "waiting", WaitingFor: "A: Allow / Reject", Event: "acp:permission"}},
		{"steer rejected then turn ends idle", []func(*Tracker) State{sent, sent, failed, done(StopEndTurn)}, State{Status: "idle", Event: "acp:end_turn"}},
		{"last prompt failing is error", []func(*Tracker) State{sent, sent, done(StopEndTurn), failed}, State{Status: "error", Event: "acp:prompt-error"}},
		{"steer: first response keeps working", []func(*Tracker) State{sent, sent, done(StopEndTurn)}, State{Status: "working", Event: "acp:prompt"}},
		{"steer: both responses idle", []func(*Tracker) State{sent, sent, done(StopEndTurn), done(StopEndTurn)}, State{Status: "idle", Event: "acp:end_turn"}},
		{"permission waits", []func(*Tracker) State{sent, ask("t1", "Run ls")}, State{Status: "waiting", WaitingFor: "Run ls: Allow / Reject", Event: "acp:permission"}},
		{"permission answered back to working", []func(*Tracker) State{sent, ask("t1", "Run ls"), answer("t1")}, State{Status: "working", Event: "acp:answered"}},
		{"permission dominates copilot idle", []func(*Tracker) State{sent, ask("t1", "Run ls"), cop("assistant.idle", "")}, State{Status: "waiting", WaitingFor: "Run ls: Allow / Reject", Event: "acp:permission"}},
		{"permission dominates updates", []func(*Tracker) State{sent, ask("t1", "Run"), upd(UpdateToolCall)}, State{Status: "waiting", WaitingFor: "Run: Allow / Reject", Event: "acp:permission"}},
		{"two permissions: still waiting after one answer", []func(*Tracker) State{sent, ask("t1", "A"), ask("t2", "B"), answer("t1")}, State{Status: "waiting", WaitingFor: "B: Allow / Reject", Event: "acp:permission"}},
		{"two permissions both answered", []func(*Tracker) State{sent, ask("t1", "A"), ask("t2", "B"), answer("t2"), answer("t1")}, State{Status: "working", Event: "acp:answered"}},
		{"unknown answer changes nothing", []func(*Tracker) State{sent, ask("t1", "A"), answer("zz")}, State{Status: "waiting", WaitingFor: "A: Allow / Reject", Event: "acp:permission"}},
		{"permission title falls back", []func(*Tracker) State{func(tr *Tracker) State {
			return tr.PermissionAsked(RequestPermissionParams{ToolCall: ToolCallRef{ToolCallID: "t", Kind: "execute"}})
		}}, State{Status: "waiting", WaitingFor: "execute", Event: "acp:permission"}},
		{"turn end clears open questions", []func(*Tracker) State{sent, ask("t1", "A"), done(StopCancelled)}, State{Status: "idle", Event: "acp:cancelled"}},
		{"copilot turn_start", []func(*Tracker) State{sent, done(StopEndTurn), cop("assistant.turn_start", "")}, State{Status: "working", Event: "copilot:assistant.turn_start"}},
		{"copilot assistant.idle", []func(*Tracker) State{sent, cop("assistant.idle", "")}, State{Status: "idle", Event: "copilot:assistant.idle"}},
		{"copilot session.idle", []func(*Tracker) State{sent, cop("session.idle", "")}, State{Status: "idle", Event: "copilot:session.idle"}},
		{"turn_end is not idle", []func(*Tracker) State{sent, cop("assistant.turn_end", "")}, State{Status: "working", Event: "acp:prompt"}},
		{"subagent idle ignored", []func(*Tracker) State{sent, cop("assistant.idle", "sub1")}, State{Status: "working", Event: "acp:prompt"}},
		{"subagent turn_start ignored", []func(*Tracker) State{sent, done(StopEndTurn), cop("assistant.turn_start", "sub1")}, State{Status: "idle", Event: "acp:end_turn"}},
		{"tool call while idle is working", []func(*Tracker) State{sent, done(StopEndTurn), upd(UpdateToolCall)}, State{Status: "working", Event: "acp:tool_call"}},
		{"agent message while idle is working", []func(*Tracker) State{sent, done(StopEndTurn), upd(UpdateAgentMessage)}, State{Status: "working", Event: "acp:agent_message_chunk"}},
		{"plan or thought while idle stays idle", []func(*Tracker) State{sent, done(StopEndTurn), upd(UpdatePlan), upd(UpdateAgentThought)}, State{Status: "idle", Event: "acp:end_turn"}},
		{"exit is error", []func(*Tracker) State{sent, exited}, State{Status: "error", Event: "acp:exited"}},
		{"exit clears questions", []func(*Tracker) State{sent, ask("t", "A"), exited}, State{Status: "error", Event: "acp:exited"}},
		{"nothing after exit", []func(*Tracker) State{exited, sent, ask("t", "A"), cop("assistant.turn_start", ""), done(StopEndTurn)}, State{Status: "error", Event: "acp:exited"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tr := NewTracker()
			got := tr.State()
			for _, s := range tc.steps {
				got = s(tr)
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
			if st := tr.State(); st != got {
				t.Errorf("State() = %+v, last step returned %+v", st, got)
			}
		})
	}
}
