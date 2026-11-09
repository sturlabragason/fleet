package mcp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/wall"
)

func fastWait(t *testing.T) {
	old := waitTick
	waitTick = 10 * time.Millisecond
	t.Cleanup(func() { waitTick = old })
}

func TestWaitAdmiral(t *testing.T) {
	fastWait(t)
	f := mailHome(t)
	f.SupPane = func() fleet.AdmiralView { return fleet.AdmiralView{} }
	m := f.Mail()
	m.Report("b", "done", "old news") // unread before the wait: not new
	sup := startRPC(t, New(f, wall.New(f.T, fleet.Session)))
	sup.call("initialize", map[string]any{"protocolVersion": ProtocolVersion})

	go func() {
		time.Sleep(50 * time.Millisecond)
		m.Report("b", "progress", "filtered out")
		time.Sleep(50 * time.Millisecond)
		m.Report("a", "blocked", "need the token\nsecond line")
	}()
	got, isErr := sup.tool("fleet_wait", map[string]any{"agents": []string{"a"}, "timeout_s": 5})
	got, _, _ = strings.Cut(got, "\n\n") // the unread-reports suffix every admiral result gets
	if isErr || !strings.HasPrefix(got, "1 new after ") || !strings.Contains(got, "\n- a blocked: need the token") ||
		strings.Contains(got, "second line") || strings.Contains(got, "filtered out") || strings.Contains(got, "old news\n") {
		t.Fatalf("wait: %q", got)
	}
	if n := len(m.Peek(mail.Admiral)); n != 3 {
		t.Fatalf("fleet_wait must not mark anything read: %d unread", n)
	}
	if got, _ := sup.tool("fleet_wait", map[string]any{"timeout_s": 1}); !strings.HasPrefix(got, "timeout after 1s, nothing new") {
		t.Fatalf("timeout: %q", got)
	}
	if got, isErr := sup.tool("fleet_wait", map[string]any{"agents": []string{"nobody"}}); !isErr || !strings.Contains(got, `no agent "nobody"`) {
		t.Fatalf("unknown agent: %q", got)
	}
}

func TestWaitAgentMessage(t *testing.T) {
	fastWait(t)
	f := mailHome(t)
	if err := f.Mail().Decide("a", "b", "grant"); err != nil {
		t.Fatal(err)
	}
	ra := startRPC(t, NewAgent(f, "a"))
	go func() {
		time.Sleep(50 * time.Millisecond)
		f.Mail().Send("b", "a", "rebased, your turn")
	}()
	got, isErr := ra.tool("fleet_wait", map[string]any{"timeout_s": 5})
	if isErr || !strings.Contains(got, "\n- b message: rebased, your turn") || !strings.Contains(got, "fleet_inbox reads them") {
		t.Fatalf("agent wait: %q", got)
	}
}

func TestWaitTimeout(t *testing.T) {
	for in, want := range map[time.Duration]time.Duration{0: WaitDefault, 30 * time.Second: 30 * time.Second, time.Hour: WaitMax} {
		if got, err := WaitTimeout(in); err != nil || got != want {
			t.Errorf("WaitTimeout(%s) = %s, %v; want %s", in, got, err, want)
		}
	}
	if _, err := WaitTimeout(-time.Second); err == nil {
		t.Error("negative timeout accepted")
	}
}

func TestWaitSendsProgress(t *testing.T) {
	fastWait(t)
	old := progressEvery
	progressEvery = 100 * time.Millisecond
	t.Cleanup(func() { progressEvery = old })
	f := mailHome(t)
	r := startRPC(t, NewAgent(f, "a"))
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{
		"name": "fleet_wait", "arguments": map[string]any{"timeout_s": 1}, "_meta": map[string]any{"progressToken": "tok"}}})
	r.in.Write(append(b, '\n'))
	progress := 0
	for r.out.Scan() {
		var m map[string]any
		json.Unmarshal(r.out.Bytes(), &m)
		if m["method"] == "notifications/progress" {
			if p := m["params"].(map[string]any); p["progressToken"] != "tok" {
				t.Fatalf("progress: %v", m)
			}
			progress++
			continue
		}
		text := m["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
		if !strings.HasPrefix(text, "timeout after 1s") {
			t.Fatalf("result: %q", text)
		}
		break
	}
	if progress < 3 {
		t.Fatalf("want progress notifications every 100ms over 1s, got %d", progress)
	}
}
