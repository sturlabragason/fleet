package status

import (
	"testing"
	"time"
)

func TestParsePayload(t *testing.T) {
	for _, in := range []string{
		`{"message":" hi ","notification_type":"permission_prompt","tool_name":"Bash","agent_id":"x1","agent_type":"explore"}`,
		`{"message":"hi","notificationType":"permission_prompt","toolName":"Bash","agentId":"x1","agentType":"explore"}`,
	} {
		want := Payload{Message: "hi", NotificationType: "permission_prompt", ToolName: "Bash", AgentID: "x1", AgentType: "explore"}
		if got := ParsePayload([]byte(in)); got != want {
			t.Errorf("%s: %+v", in, got)
		}
	}
	if got := ParsePayload([]byte(`{"agentName":"task"}`)).AgentType; got != "task" {
		t.Errorf("agentName: %q", got)
	}
	if got := (Payload{NotificationType: "idle_prompt"}).WaitingFor("Notification"); got != "idle prompt" {
		t.Errorf("WaitingFor notification %q", got)
	}
}

func TestRecordSubs(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	if _, ok := ReadHookSubs(home, "a.1", now); ok {
		t.Fatal("subs before any hook")
	}
	// A session end before any subagent creates nothing: hooks stay "not live".
	RecordSubs(home, "a.1", "sessionend", Payload{}, now)
	if _, ok := ReadHookSubs(home, "a.1", now); ok {
		t.Fatal("session end created the subs file")
	}
	rec := func(event string, p Payload) {
		t.Helper()
		if err := RecordSubs(home, "a.1", event, p, now); err != nil {
			t.Fatal(err)
		}
	}
	rec("pretooluse", Payload{})
	if _, ok := ReadHookSubs(home, "a.1", now); ok {
		t.Fatal("non-subagent event created the subs file")
	}
	rec("SubagentStart", Payload{AgentID: "c1", AgentType: "explore"}) // Claude
	rec("subagentStart", Payload{AgentType: "task"})                  // Copilot: no id
	rec("subagentStart", Payload{AgentType: "task"})
	rec("subagentstart", Payload{AgentID: "c1", AgentType: "explore"}) // repeat: no new entry
	rec("subagentStop", Payload{AgentType: "task"})                    // oldest running task
	rec("SubagentStop", Payload{AgentID: "c1"})
	subs, ok := ReadHookSubs(home, "a.1", now)
	if !ok || len(subs) != 3 {
		t.Fatalf("subs %+v %v", subs, ok)
	}
	want := []struct{ id, name, state string }{{"c1", "explore", SubDone}, {"hook-2", "task", SubDone}, {"hook-3", "task", SubRunning}}
	for i, w := range want {
		if s := subs[i]; s.ID != w.id || s.Name != w.name || s.State != w.state {
			t.Errorf("sub %d %+v want %+v", i, s, w)
		}
	}
	rec("sessionEnd", Payload{})
	subs, _ = ReadHookSubs(home, "a.1", now)
	for _, s := range subs {
		if s.State == SubRunning {
			t.Errorf("running after session end: %+v", s)
		}
	}
	DropHookSubs(home, "a.1")
	if _, ok := ReadHookSubs(home, "a.1", now); ok {
		t.Error("subs after drop")
	}
}
