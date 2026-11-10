package mcp

import (
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/supervisor"
	"github.com/sturlabragason/fleet/internal/wall"
)

func admiralHome(t *testing.T) *fleet.Fleet {
	t.Helper()
	f := squadHome(t)
	if _, err := supervisor.Add(f.Home, "print", "", 3); err != nil {
		t.Fatal(err)
	}
	f.Store.Update(func(b *board.Board) error {
		b.Agents["p1"] = &board.Agent{Name: "p1", Profile: "fake", Dir: f.Home, Admiral: "print"}
		return nil
	})
	return f
}

// Main is refused writes on print's agent; reads still work. Without
// --admiral the server is main.
func TestAdmiralScopeRefusal(t *testing.T) {
	f := admiralHome(t)
	main := startRPC(t, NewAdmiral(f, wall.New(f.T, fleet.Session), ""))
	if msg, isErr := main.tool("fleet_close", map[string]any{"name": "p1", "confirmed": true}); !isErr || !strings.Contains(msg, "belongs to admiral print") {
		t.Fatalf("close: %q %v", msg, isErr)
	}
	if msg, isErr := main.tool("fleet_send", map[string]any{"name": "p1", "text": "x"}); !isErr || !strings.Contains(msg, "belongs to admiral print") {
		t.Fatalf("send: %q %v", msg, isErr)
	}
	if msg, _ := main.tool("fleet_status", nil); !strings.Contains(msg, "p1") {
		t.Fatalf("status hides p1: %q", msg)
	}
	if _, isErr := main.tool("fleet_handover", map[string]any{"name": "p1", "to": "main"}); !isErr {
		t.Fatal("main handed over print's agent")
	}
	// Giving away your own agent needs no confirmation (§6.12).
	print := startRPC(t, NewAdmiral(f, wall.New(f.T, fleet.Session), "print"))
	if msg, isErr := print.tool("fleet_handover", map[string]any{"name": "p1", "to": "main"}); isErr {
		t.Fatalf("print could not give p1: %q", msg)
	}
	if got := f.Store.Load().Agents["p1"].Admiral; got != "" && got != board.Main {
		t.Fatalf("p1 still owned by %q", got)
	}
	if NewAdmiral(f, wall.New(f.T, fleet.Session), "").Me() != board.Main || NewAdmiral(f, wall.New(f.T, fleet.Session), "print").Me() != "print" {
		t.Fatal("identity")
	}
}

// fleet_reports leaves messages unread; fleet_inbox leaves reports unread.
func TestAdmiralSeparateReadMarks(t *testing.T) {
	f := admiralHome(t)
	st := f.Mail()
	if _, err := st.ReportTo("p1", "admiral.print", "done", "r1"); err != nil {
		t.Fatal(err)
	}
	if err := f.AccessDecide(mail.Admiral, "admiral.print", "grant"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Send(mail.Admiral, "admiral.print", "hello"); err != nil {
		t.Fatal(err)
	}
	pr := startRPC(t, NewAdmiral(f, wall.New(f.T, fleet.Session), "print"))
	if msg, _ := pr.tool("fleet_reports", nil); !strings.Contains(msg, "r1") {
		t.Fatalf("reports: %q", msg)
	}
	if len(st.PeekMsgs("admiral.print")) != 2 { // the grant notice and the hello
		t.Fatal("fleet_reports marked the message read")
	}
	if msg, _ := pr.tool("fleet_inbox", nil); !strings.Contains(msg, "hello") {
		t.Fatalf("inbox: %q", msg)
	}
	if len(st.Peek("admiral.print")) != 0 {
		t.Fatal("reports still unread after fleet_reports")
	}
	st.ReportTo("p1", "admiral.print", "done", "r2")
	pr.tool("fleet_inbox", nil)
	if len(st.Peek("admiral.print")) != 1 {
		t.Fatal("fleet_inbox marked a report read")
	}
}

// An empty to/from/name must not fall through to main's mailbox.
func TestAdmiralCallRejectsEmptyNames(t *testing.T) {
	f := admiralHome(t)
	pr := startRPC(t, NewAdmiral(f, wall.New(f.T, fleet.Session), "print"))
	for tool, in := range map[string]map[string]any{
		"fleet_msg_send":       {"to": "", "text": "hi"},
		"fleet_access_request": {"to": " ", "reason": "x"},
		"fleet_access_respond": {"from": "", "decision": "grant"},
		"fleet_access_revoke":  {"name": ""},
	} {
		if msg, isErr := pr.tool(tool, in); !isErr || !strings.Contains(msg, "required") {
			t.Errorf("%s: %q %v", tool, msg, isErr)
		}
	}
	if n := len(f.Mail().All()); n != 0 {
		t.Errorf("mail written: %d", n)
	}
}

// The status header names the admiral once more than one exists.
func TestAdmiralHeaderNamesAdmiral(t *testing.T) {
	f := admiralHome(t)
	for _, name := range []string{"", "print"} {
		s := NewAdmiral(f, wall.New(f.T, fleet.Session), name)
		want := "admiral " + s.Me() + ":"
		if got := s.admiralHeader(); !strings.HasPrefix(got, want) {
			t.Errorf("%q header %q, want prefix %q", name, got, want)
		}
	}
	solo := squadHome(t)
	if got := NewAdmiral(solo, wall.New(solo.T, fleet.Session), "").admiralHeader(); !strings.HasPrefix(got, "admiral: ") {
		t.Errorf("single admiral header %q", got)
	}
}

// Unknown identity: reads work, every change is refused.
func TestUnknownAdmiralRefusesWrites(t *testing.T) {
	f := admiralHome(t)
	c := startRPC(t, NewAdmiral(f, wall.New(f.T, fleet.Session), Unknown))
	if msg, isErr := c.tool("fleet_status", nil); isErr || !strings.Contains(msg, "p1") {
		t.Fatalf("status: %q %v", msg, isErr)
	}
	for tool, a := range map[string]map[string]any{
		"fleet_send":  {"name": "p1", "text": "x"},
		"fleet_close": {"name": "p1", "confirmed": true},
		"fleet_spawn": {"name": "n1", "profile": "fake"},
	} {
		if msg, isErr := c.tool(tool, a); !isErr || !strings.Contains(msg, "admiral identity unknown") || !strings.Contains(msg, "fleet admiral restart --name <name>") {
			t.Errorf("%s: %q %v", tool, msg, isErr)
		}
	}
}

// Every admiral tool is refused under unknown identity unless it is one of
// the plain reads, so a write tool added later can't slip through.
func TestIdentityBoundCoversEveryWrite(t *testing.T) {
	reads := map[string]bool{"fleet_status": true, "fleet_peek": true, "fleet_info": true, "fleet_brief": true,
		"fleet_profiles": true, "fleet_sessions": true, "fleet_access": true, "fleet_touching": true, "fleet_changes": true}
	for _, d := range toolDefs {
		if !reads[d.Name] && !identityBound[d.Name] {
			t.Errorf("%s is neither a read nor identity-bound", d.Name)
		}
	}
}

// fleet#19: a send to a closed agent names resume=true, only its owner may
// resume it, and closed records stay out of fleet_status.
func TestSendToClosedAgent(t *testing.T) {
	f := admiralHome(t)
	f.Store.Update(func(b *board.Board) error {
		b.AddClosed(&board.Agent{Name: "c1", Profile: "fake", Dir: f.Home + "/gone", SessionID: "SESSION1", Admiral: "print"}, "", time.Now())
		return nil
	})
	main := startRPC(t, NewAdmiral(f, wall.New(f.T, fleet.Session), ""))
	if msg, isErr := main.tool("fleet_send", map[string]any{"name": "c1", "text": "x", "resume": true}); !isErr || !strings.Contains(msg, "belongs to admiral print") {
		t.Fatalf("main resumed print's agent: %q %v", msg, isErr)
	}
	print := startRPC(t, NewAdmiral(f, wall.New(f.T, fleet.Session), "print"))
	if msg, isErr := print.tool("fleet_send", map[string]any{"name": "c1", "text": "x"}); !isErr || !strings.Contains(msg, "c1 closed at ") || !strings.Contains(msg, "pass resume=true to resume its last session") {
		t.Fatalf("refusal: %q %v", msg, isErr)
	}
	if msg, isErr := print.tool("fleet_send", map[string]any{"name": "c1", "text": "x", "resume": true}); !isErr || !strings.Contains(msg, "is gone") {
		t.Fatalf("resume into a gone dir: %q %v", msg, isErr)
	}
	if msg, _ := print.tool("fleet_status", nil); strings.Contains(msg, "c1") {
		t.Fatalf("status lists a closed agent: %q", msg)
	}
}
