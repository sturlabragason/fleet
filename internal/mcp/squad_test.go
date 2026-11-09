package mcp

import (
	"fmt"
	"math/rand"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/tmux"
	"github.com/sturlabragason/fleet/internal/wall"
)

// squadHome is a board with a leader, one of its agents and an outsider;
// board entries only, so no tmux pane is needed.
func squadHome(t *testing.T) *fleet.Fleet {
	t.Helper()
	home := t.TempDir()
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "no-config.toml"))
	tc := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { tc.KillServer() })
	f, err := fleet.New(tc, home, "/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	f.Store.Update(func(b *board.Board) error {
		b.Agents["lead"] = &board.Agent{Name: "lead", Profile: "fake", Dir: home, Leader: true}
		b.Agents["m1"] = &board.Agent{Name: "m1", Profile: "fake", Dir: home, LeadBy: "lead"}
		b.Agents["outsider"] = &board.Agent{Name: "outsider", Profile: "fake", Dir: home}
		return nil
	})
	return f
}

// A leader sees the squad tools; an ordinary agent does not, and calling
// one is refused.
func TestSquadToolsAreLeaderOnly(t *testing.T) {
	f := squadHome(t)
	lead, member := startRPC(t, NewAgent(f, "lead")), startRPC(t, NewAgent(f, "m1"))
	names := func(r *rpc) []string {
		var out []string
		for _, tl := range r.call("tools/list", nil)["result"].(map[string]any)["tools"].([]any) {
			out = append(out, tl.(map[string]any)["name"].(string))
		}
		sort.Strings(out)
		return out
	}
	got := strings.Join(names(lead), " ")
	for _, want := range []string{"fleet_squad_spawn", "fleet_squad_status", "fleet_squad_info", "fleet_squad_peek",
		"fleet_squad_send", "fleet_squad_reports", "fleet_squad_close", "fleet_squad_profiles"} {
		if !strings.Contains(got, want) {
			t.Errorf("leader is missing %s: %s", want, got)
		}
	}
	// No prompt-answering tool for a leader: that escalates (ADR-53).
	if strings.Contains(got, "fleet_answer") {
		t.Errorf("a leader must not be able to answer prompts: %s", got)
	}
	if m := strings.Join(names(member), " "); strings.Contains(m, "fleet_squad") {
		t.Errorf("a squad member got squad tools: %s", m)
	}
	// Only a leader is told how to run a squad.
	leadInit := lead.call("initialize", map[string]any{"protocolVersion": ProtocolVersion})["result"].(map[string]any)
	if leadInit["instructions"] != AgentInstructions+LeaderInstructions {
		t.Error("a leader must get the leader instructions")
	}
	memInit := member.call("initialize", map[string]any{"protocolVersion": ProtocolVersion})["result"].(map[string]any)
	if memInit["instructions"] != AgentInstructions {
		t.Error("a squad member must get the plain agent instructions")
	}
	if msg, isErr := member.tool("fleet_squad_status", nil); !isErr || !strings.Contains(msg, "does not lead a squad") {
		t.Errorf("member calling a squad tool: %q (isErr=%v)", msg, isErr)
	}
}

// Every squad tool that names an agent refuses one outside the squad.
func TestSquadToolsRefuseNonMembers(t *testing.T) {
	f := squadHome(t)
	s := NewAgent(f, "lead")
	s.W = wall.New(f.T, fleet.Session)
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"fleet_squad_info", map[string]any{"name": "outsider"}},
		{"fleet_squad_peek", map[string]any{"name": "outsider"}},
		{"fleet_squad_send", map[string]any{"name": "outsider", "text": "hi"}},
		{"fleet_squad_close", map[string]any{"name": "outsider"}},
	} {
		msg, isErr := callText(t, s, tc.tool, tc.args)
		if !isErr || !strings.Contains(msg, "not in your squad") {
			t.Errorf("%s on an outsider: %q (isErr=%v)", tc.tool, msg, isErr)
		}
	}
	if _, ok := f.Store.Load().Agents["outsider"]; !ok {
		t.Fatal("a leader closed an agent outside its squad")
	}
	// Its own member is in scope: the status table lists it and nothing else.
	st, isErr := callText(t, s, "fleet_squad_status", nil)
	if isErr || !strings.Contains(st, "m1") || strings.Contains(st, "outsider") {
		t.Errorf("squad status: %q", st)
	}
}

// An unconfirmed squad close only says what the close would lose; a
// confirmed one closes, on the first call too.
func TestSquadCloseNeedsConfirmation(t *testing.T) {
	f := squadHome(t)
	s := NewAgent(f, "lead")
	msg, isErr := callText(t, s, "fleet_squad_close", map[string]any{"name": "m1"})
	if !isErr || !strings.Contains(msg, "confirmed=true") {
		t.Fatalf("unconfirmed close: %q (isErr=%v)", msg, isErr)
	}
	if _, ok := f.Store.Load().Agents["m1"]; !ok {
		t.Fatal("an unconfirmed close removed the agent")
	}
	if msg, isErr := callText(t, s, "fleet_squad_close", map[string]any{"name": "m1", "confirmed": true}); isErr {
		t.Fatalf("confirmed close: %q", msg)
	}
	if _, ok := f.Store.Load().Agents["m1"]; ok {
		t.Fatal("a confirmed close left the agent on the board")
	}
}

// A leader may not answer a permission prompt for one of its agents.
func TestSquadSendRefusesAPrompt(t *testing.T) {
	f := squadHome(t)
	f.Store.Update(func(b *board.Board) error { b.Agents["m1"].Status = "waiting"; return nil })
	s := NewAgent(f, "lead")
	msg, isErr := callText(t, s, "fleet_squad_send", map[string]any{"name": "m1", "text": "y"})
	if isErr || !strings.Contains(msg, "may not answer prompts") {
		t.Fatalf("send to a prompted agent: %q (isErr=%v)", msg, isErr)
	}
}
