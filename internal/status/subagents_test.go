package status

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "copilot-events", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// states renders a list as "name:state" in order.
func states(l []Subagent) string {
	var p []string
	for _, s := range l {
		p = append(p, s.Name+":"+s.State)
	}
	return strings.Join(p, " ")
}

func TestScanSubagentsFixtures(t *testing.T) {
	for _, c := range []struct {
		file, now, want string
	}{
		// builder done; reviewer done, re-woken by write_agent, done again on
		// its final answer (no second subagent.completed); second builder running.
		{"background.jsonl", "2026-11-05T06:20:00Z", "fleet-builder:done fleet-reviewer:done fleet-builder:running"},
		// At 06:21:30 builder#1 (ended 06:15:21) is outside the 5-minute window.
		{"background.jsonl", "2026-11-05T06:21:30Z", "fleet-reviewer:done fleet-builder:running"},
		// Late cancelled:true for an already-finished agent stays done. (now
		// sits near the first end: finished dots only live 5 minutes.)
		{"cancelled.jsonl", "2026-10-08T11:01:00Z", "docs-review:done"},
		{"cancelled.jsonl", "2026-10-09T10:15:00Z", ""},
		{"sync-shutdown.jsonl", "2026-10-27T09:26:00Z", "estimator:done"},
		// Process restart killed the running pr-shipper; later events keep it failed.
		{"killed-resume.jsonl", "2026-10-17T21:10:00Z", "pr-shipper:failed"},
	} {
		data := fixture(t, c.file)
		got, n := ScanSubagents(data, Subs{}, at(c.now))
		if s := states(got.List); s != c.want {
			t.Errorf("%s @%s: %q, want %q", c.file, c.now, s, c.want)
		}
		if n != int64(len(data)) {
			t.Errorf("%s: consumed %d of %d", c.file, n, len(data))
		}
	}
}

func TestScanSubagentsDetails(t *testing.T) {
	st, _ := ScanSubagents(fixture(t, "background.jsonl"), Subs{}, at("2026-11-05T06:21:30Z"))
	got := st.List
	rev := got[0]
	if rev.ID != "toolu_01YG49fyeybkx77DtMLQ4cjR" || rev.AgentID != "aa59b50e-e9fc-4219-9cbc-121c94ed7ce6" ||
		!rev.Started.Equal(at("2026-11-05T06:15:33.192Z")) || !rev.Ended.Equal(at("2026-11-05T06:19:20.229Z")) {
		t.Errorf("re-woken reviewer: %+v", rev)
	}
	if RunningCount(got) != 1 || !got[1].Started.Equal(at("2026-11-05T06:21:06.415Z")) {
		t.Errorf("running %d: %+v", RunningCount(got), got)
	}
	// builder#1 ends with its final turn, just before subagent.completed.
	st, _ = ScanSubagents(fixture(t, "background.jsonl"), Subs{}, at("2026-11-05T06:20:00Z"))
	if e := st.List[0].Ended; !e.Equal(at("2026-11-05T06:15:21.715Z")) {
		t.Errorf("builder ended %v", e)
	}
	if len(st.Known) != 3 || st.Known[1].AgentID != rev.AgentID || st.Known[1].Name != "fleet-reviewer" {
		t.Errorf("known: %+v", st.Known)
	}
	ks, _ := ScanSubagents(fixture(t, "killed-resume.jsonl"), Subs{}, at("2026-10-17T21:10:00Z"))
	k := ks.List
	last := k[len(k)-1]
	if last.State != SubFailed || !last.Ended.Equal(at("2026-10-17T21:09:23.334Z")) {
		t.Errorf("killed pr-shipper: %+v", last)
	}
}

func TestScanSubagentsIncremental(t *testing.T) {
	data := fixture(t, "background.jsonl")
	now := at("2026-11-05T06:20:00Z")
	want, _ := ScanSubagents(data, Subs{}, now)
	for _, cut := range []int{1, 137, len(data) / 2, len(data) - 5} {
		got, n := ScanSubagents(data[:cut], Subs{}, now)
		if n > 0 && data[n-1] != '\n' {
			t.Fatalf("cut %d: offset %d not at a line boundary", cut, n)
		}
		got, m := ScanSubagents(data[n:], got, now)
		if n+m != int64(len(data)) || !reflect.DeepEqual(got, want) {
			t.Errorf("cut %d: %q (consumed %d), want %q", cut, states(got.List), n+m, states(want.List))
		}
	}
}

func TestReadSubagentsFile(t *testing.T) {
	data := fixture(t, "background.jsonl")
	now := at("2026-11-05T06:20:00Z")
	want, _ := ScanSubagents(data, Subs{}, now)
	path := filepath.Join(t.TempDir(), "events.jsonl")

	// Missing file: unchanged.
	prev := Subs{List: []Subagent{{ID: "x", State: SubRunning}}}
	if got, off := ReadSubagents(path, 42, prev, now); off != 42 || !reflect.DeepEqual(got, prev) {
		t.Errorf("missing file: %v %d", got, off)
	}

	// Written in two parts, the first ending mid-line.
	cut := len(data)/2 + 3
	os.WriteFile(path, data[:cut], 0o644)
	got, off := ReadSubagents(path, 0, Subs{}, now)
	if off >= int64(cut) || data[off-1] != '\n' {
		t.Fatalf("offset %d (cut %d) not at a line boundary", off, cut)
	}
	os.WriteFile(path, data, 0o644)
	got, off = ReadSubagents(path, off, got, now)
	if off != int64(len(data)) || !reflect.DeepEqual(got, want) {
		t.Errorf("incremental: %q @%d, want %q", states(got.List), off, states(want.List))
	}
	// Nothing new: same result.
	if again, off2 := ReadSubagents(path, off, got, now); off2 != off || !reflect.DeepEqual(again, want) {
		t.Errorf("idle poll changed things: %q @%d", states(again.List), off2)
	}

	// File replaced by a shorter one: restart from 0 with an empty list.
	short := fixture(t, "sync-shutdown.jsonl")
	os.WriteFile(path, short, 0o644)
	got, off = ReadSubagents(path, off, got, at("2026-10-27T09:26:00Z"))
	if off != int64(len(short)) || states(got.List) != "estimator:done" || len(got.Known) != 1 {
		t.Errorf("after shrink: %q @%d known %+v", states(got.List), off, got.Known)
	}
}

func TestReadSubagentsHugeLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	big := `{"type":"assistant.message","data":{"content":"` + strings.Repeat("A", 3<<20) + `"}}` + "\n"
	start := `{"type":"subagent.started","data":{"toolCallId":"t1","agentName":"big"},"agentId":"a1","timestamp":"2026-11-05T06:00:00Z"}` + "\n"
	broken := `{"type":"subagent.completed","data":{` + "\n"
	os.WriteFile(path, []byte(big+broken+start+big), 0o644)
	got, off := ReadSubagents(path, 0, Subs{}, at("2026-11-05T06:01:00Z"))
	if states(got.List) != "big:running" || off != int64(2*len(big)+len(broken)+len(start)) {
		t.Errorf("%q @%d", states(got.List), off)
	}
}

func ev(typ, ts, data string, extra ...string) string {
	x := ""
	if len(extra) > 0 {
		x = `,"agentId":"` + extra[0] + `"`
	}
	return fmt.Sprintf(`{"type":%q,"data":%s%s,"timestamp":%q}`, typ, data, x, ts) + "\n"
}

func TestScanSubagentsWriteAgentIDs(t *testing.T) {
	log := ev("subagent.started", "2026-11-05T06:00:00Z", `{"toolCallId":"t1","agentName":"a"}`, "A") +
		ev("subagent.started", "2026-11-05T06:00:01Z", `{"toolCallId":"t2","agentName":"b"}`, "B") +
		ev("subagent.started", "2026-11-05T06:00:02Z", `{"toolCallId":"t3","agentName":"c"}`, "C") +
		ev("subagent.completed", "2026-11-05T06:01:00Z", `{"toolCallId":"t1"}`) +
		ev("subagent.completed", "2026-11-05T06:01:00Z", `{"toolCallId":"t2"}`) +
		ev("tool.execution_complete", "2026-11-05T06:01:00Z", `{"toolCallId":"t3","success":false}`) +
		ev("tool.execution_start", "2026-11-05T06:02:00Z", `{"toolCallId":"w1","toolName":"write_agent","arguments":{"agent_ids":["A","C"],"scope":"x"}}`)
	got, _ := ScanSubagents([]byte(log), Subs{}, at("2026-11-05T06:03:00Z"))
	if s := states(got.List); s != "a:running b:done c:running" {
		t.Errorf("%q", s)
	}
	// A cancelled completion of a running subagent is a failure.
	got, _ = ScanSubagents([]byte(ev("subagent.completed", "2026-11-05T06:04:00Z", `{"toolCallId":"t1","cancelled":true}`)), got, at("2026-11-05T06:05:00Z"))
	if s := states(got.List); s != "a:failed b:done c:running" {
		t.Errorf("%q", s)
	}
}

func TestScanSubagentsFinalAnswer(t *testing.T) {
	msg := func(ts, reqs string) string {
		return ev("assistant.message", ts, `{"content":"x","toolRequests":`+reqs+`}`, "A")
	}
	log := ev("subagent.started", "2026-11-05T06:00:00Z", `{"toolCallId":"t1","agentName":"a"}`, "A") +
		ev("subagent.completed", "2026-11-05T06:01:00Z", `{"toolCallId":"t1"}`) +
		msg("2026-11-05T06:01:30Z", `[]`) + // final answers of a done agent change nothing
		ev("tool.execution_start", "2026-11-05T06:02:00Z", `{"toolCallId":"w1","toolName":"write_agent","arguments":{"agent_id":"A"}}`)
	now := at("2026-11-05T06:05:00Z")
	st, _ := ScanSubagents([]byte(log), Subs{}, now)
	if s := states(st.List); s != "a:running" {
		t.Fatalf("re-woken: %q", s)
	}
	end := func(ts string) string { return ev("assistant.turn_end", ts, `{}`, "A") }
	// A tool-less message followed by a tool call in the same turn is mid-turn.
	st, _ = ScanSubagents([]byte(msg("2026-11-05T06:02:05Z", `[]`)+msg("2026-11-05T06:02:10Z", `[{"name":"bash"}]`)+end("2026-11-05T06:02:11Z")), st, now)
	if s := states(st.List); s != "a:running" {
		t.Fatalf("intermediate turn: %q", s)
	}
	st, _ = ScanSubagents([]byte(msg("2026-11-05T06:02:15Z", `[]`)), st, now)
	if s := states(st.List); s != "a:running" {
		t.Fatalf("before turn_end: %q", s)
	}
	st, _ = ScanSubagents([]byte(end("2026-11-05T06:02:20Z")), st, now)
	if s := states(st.List); s != "a:done" || !st.List[0].Ended.Equal(at("2026-11-05T06:02:20Z")) {
		t.Errorf("final answer: %q %+v", s, st.List)
	}
	// Missing toolRequests counts as none.
	st, _ = ScanSubagents([]byte(ev("tool.execution_start", "2026-11-05T06:03:00Z", `{"toolName":"write_agent","arguments":{"agent_id":"A"}}`)+
		ev("assistant.message", "2026-11-05T06:03:05Z", `{"content":"done"}`, "A")+end("2026-11-05T06:03:10Z")), st, now)
	if s := states(st.List); s != "a:done" || !st.List[0].Ended.Equal(at("2026-11-05T06:03:10Z")) {
		t.Errorf("no toolRequests: %q %+v", s, st.List)
	}
}

func TestScanSubagentsWakePruned(t *testing.T) {
	log := ev("subagent.started", "2026-11-05T06:00:00Z", `{"toolCallId":"t1","agentName":"rev"}`, "A") +
		ev("subagent.completed", "2026-11-05T06:01:00Z", `{"toolCallId":"t1"}`)
	st, _ := ScanSubagents([]byte(log), Subs{}, at("2026-11-05T07:00:00Z"))
	if len(st.List) != 0 || len(st.Known) != 1 {
		t.Fatalf("not pruned/remembered: %+v", st)
	}
	wake := ev("tool.execution_start", "2026-11-05T07:00:05Z", `{"toolName":"write_agent","arguments":{"agent_id":"A"}}`)
	got, _ := ScanSubagents([]byte(wake), st, at("2026-11-05T07:00:10Z"))
	if s := states(got.List); s != "rev:running" || got.List[0].ID != "t1" || got.List[0].AgentID != "A" ||
		!got.List[0].Started.Equal(at("2026-11-05T07:00:05Z")) {
		t.Errorf("re-wake after prune: %+v", got.List)
	}
	unknown := ev("tool.execution_start", "2026-11-05T07:00:05Z", `{"toolName":"write_agent","arguments":{"agent_id":"Z"}}`)
	if got, _ := ScanSubagents([]byte(unknown), st, at("2026-11-05T07:00:10Z")); len(got.List) != 0 {
		t.Errorf("unknown agent id woke something: %+v", got.List)
	}
}

// A refused write_agent (agent not found, not in background mode) undoes its wake.
func TestScanSubagentsFailedWrite(t *testing.T) {
	log := ev("subagent.started", "2026-11-05T06:00:00Z", `{"toolCallId":"t1","agentName":"a"}`, "A") +
		ev("subagent.completed", "2026-11-05T06:01:00Z", `{"toolCallId":"t1"}`) +
		ev("subagent.started", "2026-11-05T06:00:00Z", `{"toolCallId":"t2","agentName":"b"}`, "B") +
		ev("subagent.completed", "2026-11-05T06:01:00Z", `{"toolCallId":"t2"}`)
	now := at("2026-11-05T06:03:00Z")
	st, _ := ScanSubagents([]byte(log), Subs{}, now)
	start := func(call, id string) string {
		return ev("tool.execution_start", "2026-11-05T06:02:00Z", `{"toolCallId":"`+call+`","toolName":"write_agent","arguments":{"agent_id":"`+id+`"}}`)
	}
	done := func(call string, ok bool) string {
		return ev("tool.execution_complete", "2026-11-05T06:02:00Z", fmt.Sprintf(`{"toolCallId":%q,"success":%v}`, call, ok))
	}
	// Start and completion may land in different polls.
	st, _ = ScanSubagents([]byte(start("w1", "A")+start("w2", "B")), st, now)
	if s := states(st.List); s != "a:running b:running" {
		t.Fatalf("woken: %q", s)
	}
	st, _ = ScanSubagents([]byte(done("w1", false)+done("w2", true)), st, now)
	if s := states(st.List); s != "a:done b:running" || !st.List[0].Ended.Equal(at("2026-11-05T06:01:00Z")) || st.List[1].WakeCall != "" {
		t.Errorf("settled: %q %+v", s, st.List)
	}
	// A pruned subagent restored by a refused write_agent goes away again.
	st, _ = ScanSubagents(nil, st, at("2026-11-05T07:00:00Z"))
	st, _ = ScanSubagents([]byte(start("w3", "A")+done("w3", false)), Subs{Known: st.Known}, at("2026-11-05T07:00:00Z"))
	if len(st.List) != 0 {
		t.Errorf("restored on a refused write: %+v", st.List)
	}
}

func TestRememberBounded(t *testing.T) {
	var k []KnownSub
	for i := 0; i < SubKnownMax+5; i++ {
		k = remember(k, KnownSub{AgentID: fmt.Sprint(i)})
	}
	k = remember(k, KnownSub{AgentID: "10", Name: "again"})
	if len(k) != SubKnownMax || k[0].AgentID != "5" || k[len(k)-1].Name != "again" {
		t.Errorf("%d %+v", len(k), k)
	}
}

func TestPruneSubagents(t *testing.T) {
	now := at("2026-11-05T12:00:00Z")
	var l []Subagent
	for i := 0; i < 16; i++ {
		s := Subagent{ID: fmt.Sprint(i), Name: fmt.Sprint(i), State: SubDone, Started: now.Add(time.Duration(i-30) * time.Minute)}
		s.Ended = now.Add(-time.Duration(16-i) * 10 * time.Second) // all within 5 minutes
		if i%5 == 0 {
			s.State, s.Ended = SubRunning, time.Time{}
		}
		l = append(l, s)
	}
	l = append(l, Subagent{ID: "old", Name: "old", State: SubFailed, Started: now.Add(-time.Hour), Ended: now.Add(-SubKeep - time.Second)})
	got := PruneSubagents(l, now)
	if len(got) != SubMax {
		t.Fatalf("len %d", len(got))
	}
	// 4 running (0,5,10,15) stay; the 4 oldest finished (1,2,3,4) go; "old" is out of the window.
	if s := states(got); s != "0:running 5:running 6:done 7:done 8:done 9:done 10:running 11:done 12:done 13:done 14:done 15:running" {
		t.Errorf("%q", s)
	}
	// More than SubMax running: the oldest running go.
	var r []Subagent
	for i := 0; i < 14; i++ {
		r = append(r, Subagent{Name: fmt.Sprint(i), State: SubRunning, Started: now.Add(time.Duration(i) * time.Second)})
	}
	if got := PruneSubagents(r, now); len(got) != SubMax || got[0].Name != "2" {
		t.Errorf("%q", states(got))
	}
	if got := EndSubagents(r[:2], now); states(got) != "0:failed 1:failed" || !got[0].Ended.Equal(now) || r[0].State != SubRunning {
		t.Errorf("end: %q (input mutated: %v)", states(got), r[0].State)
	}
}

// before returns the fixture lines logged before the timestamp prefix ts.
func before(data []byte, ts string) []byte {
	var head []byte
	for _, l := range strings.SplitAfter(string(data), "\n") {
		if i := strings.Index(l, `"timestamp":"`); i >= 0 && l[i+13:] >= ts {
			break
		}
		head = append(head, l...)
	}
	return head
}

// A real subagent sent a tool-less message mid-turn (13:14:57) and kept
// working; only the turn that ends without tool calls finishes it.
func TestScanSubagentsMidTurnEmptyMessage(t *testing.T) {
	data := fixture(t, "midturn-empty.jsonl")
	head := before(data, "2026-09-23T13:16")
	st, _ := ScanSubagents(head, Subs{}, at("2026-09-23T13:16:00Z"))
	if s := states(st.List); !strings.HasSuffix(s, ":running") {
		t.Fatalf("after mid-turn empty message: %q", s)
	}
	st, _ = ScanSubagents(data[len(head):], st, at("2026-09-23T13:17:00Z"))
	if s := states(st.List); !strings.HasSuffix(s, ":done") || st.List[0].Ended.Before(at("2026-09-23T13:16:52Z")) {
		t.Errorf("at the end: %q %+v", s, st.List)
	}
}

// A message queued while the real subagent was busy restarted it right after
// its final turn (07:02:45), with no write_agent after the end. It ran on
// until 07:04:25.
func TestScanSubagentsQueuedWake(t *testing.T) {
	data := fixture(t, "queued-wake.jsonl")
	for _, c := range []struct{ until, want string }{
		{"2026-09-29T07:02:45.7", "research:done"},
		{"2026-09-29T07:03", "research:running"},
		{"2026-09-29T07:05", "research:done"},
	} {
		st, _ := ScanSubagents(before(data, c.until), Subs{}, at(c.until[:16]+":59Z"))
		if s := states(st.List); s != c.want {
			t.Errorf("until %s: %q, want %q", c.until, s, c.want)
		}
	}
}

// agy's subagents come from fleet's ACP transcript: an invoke_subagent call
// runs until its tool call completes or the turn ends.
func TestReadACPSubagents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a1.jsonl")
	now := time.Date(2026, 11, 10, 9, 6, 20, 0, time.UTC)
	lines := `{"at":"2026-11-10T09:06:10Z","kind":"user","text":"go"}
{"at":"2026-11-10T09:06:14Z","kind":"tool","title":"Run invoke_subagent","status":"in_progress","tool_id":"c1","sub":"OK Responder"}
{"at":"2026-11-10T09:06:15Z","kind":"tool","title":"Run invoke_subagent","status":"in_progress","tool_id":"c2","sub":"research"}
{"at":"2026-11-10T09:06:16Z","kind":"tool","title":"Read file","status":"completed","tool_id":"c3"}
`
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	st, off := ReadACPSubagents(path, 0, Subs{}, now)
	if RunningCount(st.List) != 2 || st.List[0].Name != "OK Responder" || off != int64(len(lines)) {
		t.Fatalf("running %+v off %d", st.List, off)
	}
	more := `{"at":"2026-11-10T09:06:31Z","kind":"tool","title":"Run invoke_subagent","status":"completed","tool_id":"c1","sub":"OK Responder"}
`
	appendTo(t, path, more)
	st, off = ReadACPSubagents(path, off, st, now)
	if RunningCount(st.List) != 1 || st.List[0].State != SubDone {
		t.Fatalf("after completion %+v", st.List)
	}
	appendTo(t, path, `{"at":"2026-11-10T09:06:33Z","kind":"status","status":"idle","text":"acp:end_turn"}
`)
	st, _ = ReadACPSubagents(path, off, st, now)
	if RunningCount(st.List) != 0 {
		t.Fatalf("turn end left %+v running", st.List)
	}
}

func appendTo(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}
