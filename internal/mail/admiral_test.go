package mail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdmiralNamesAndBoxes(t *testing.T) {
	if checkName("admiral.print") == nil {
		t.Error("admiral. prefix accepted as an agent name")
	}
	for _, bad := range []string{"admiral.", "admiral..", "admiral.a/b", "admiral.-x", "agent1"} {
		if IsAdmiralBox(bad) {
			t.Errorf("IsAdmiralBox(%q)", bad)
		}
	}
	for _, ok := range []string{Admiral, LegacySupervisor, "admiral.print"} {
		if !IsAdmiralBox(ok) {
			t.Errorf("IsAdmiralBox(%q) = false", ok)
		}
	}
}

func TestCheckPairRefusesAdmiralAgentMail(t *testing.T) {
	s := newStore(t)
	if _, err := s.Send("a", "admiral.print", "hi"); err == nil {
		t.Error("agent -> admiral accepted")
	}
	if _, err := s.Send("admiral.print", "a", "hi"); err == nil {
		t.Error("admiral -> agent accepted")
	}
	if _, err := s.Request("a", Admiral, "x"); err == nil {
		t.Error("agent request to admiral accepted")
	}
	if err := s.Decide("admiral.print", "a", "grant"); err == nil {
		t.Error("admiral/agent grant accepted")
	}
	if err := s.Decide(Admiral, "admiral.print", "grant"); err != nil {
		t.Errorf("admiral pair refused: %v", err)
	}
}

// Main's reports keep the single `read` mark old binaries use; the extra
// mark only exists for admirals' other messages.
func TestMainInboxUsesLegacyReadMark(t *testing.T) {
	s := newStore(t)
	m1, _ := s.Report("a", "done", "one")
	m2, _ := s.Report("b", "done", "two")
	in, err := s.Inbox(Admiral, false, 0)
	if err != nil || len(in) != 2 {
		t.Fatalf("inbox: %+v %v", in, err)
	}
	dir := filepath.Join(s.Dir, Admiral)
	b, err := os.ReadFile(filepath.Join(dir, "read"))
	if err != nil || strings.TrimSpace(string(b)) != m2.ID || m1.ID >= m2.ID {
		t.Fatalf("read mark %q %v, want %q", b, err, m2.ID)
	}
	if _, err := os.Stat(filepath.Join(dir, "read_msgs")); err == nil {
		t.Error("read_msgs written by a reports-only read")
	}
	if s.Unread(Admiral) != 0 {
		t.Error("still unread")
	}
}

func TestPeekAllOrdersReportsAndMessages(t *testing.T) {
	s := newStore(t)
	if err := s.Decide(Admiral, "admiral.print", "grant"); err != nil {
		t.Fatal(err)
	}
	s.Inbox(Admiral, false, 0) // drop the grant note
	s.InboxMsgs(Admiral, false, 0)
	r1, _ := s.Report("a", "done", "first")
	mustSend(t, s, "admiral.print", Admiral, "second")
	r3, _ := s.Report("b", "done", "third")
	all := s.PeekAll(Admiral)
	if len(all) != 3 || all[0].ID != r1.ID || all[1].Text != "second" || all[2].ID != r3.ID {
		t.Fatalf("PeekAll: %+v", all)
	}
	// Reading messages must not eat reports, and vice versa.
	if ms, _ := s.InboxMsgs(Admiral, false, 0); len(ms) != 1 || s.Unread(Admiral) != 2 {
		t.Fatalf("msgs %+v unread reports %d", ms, s.Unread(Admiral))
	}
}

func TestMoveReports(t *testing.T) {
	s := newStore(t)
	s.Report("a", "done", "from a")
	s.Report("b", "blocked", "from b")
	moved, err := s.MoveReports(Admiral, "admiral.print", func(from string) bool { return from == "a" })
	if err != nil || len(moved) != 1 || moved[0].From != "a" || moved[0].To != "admiral.print" {
		t.Fatalf("moved %+v %v", moved, err)
	}
	if p := s.Peek("admiral.print"); len(p) != 1 || p[0].From != "a" {
		t.Fatalf("receiver: %+v", p)
	}
	if p := s.Peek(Admiral); len(p) != 1 || p[0].From != "b" {
		t.Fatalf("sender keeps the rest: %+v", p)
	}
	// The receiver's read mark must not hide a moved report: read a newer one first.
	s.Inbox("admiral.print", false, 0)
	s.Report("c", "done", "newer")
	s.ReportTo("c", "admiral.print", "done", "newer to print")
	s.Inbox("admiral.print", false, 0)
	moved, err = s.MoveReports(Admiral, "admiral.print", nil)
	if err != nil || len(moved) != 2 {
		t.Fatalf("second move %+v %v", moved, err)
	}
	if p := s.Peek("admiral.print"); len(p) != 2 {
		t.Fatalf("moved reports hidden by read mark: %+v", p)
	}
	if _, err := s.MoveReports("a", "admiral.print", nil); err == nil {
		t.Error("move from an agent box accepted")
	}
	if m, err := s.MoveReports(Admiral, Admiral, nil); err != nil || m != nil {
		t.Errorf("move to self: %+v %v", m, err)
	}
}

func TestInboxFromMarksOnlyThatSender(t *testing.T) {
	s := newStore(t)
	s.Report("a", "done", "a1")
	s.Report("b", "done", "b1")
	s.Report("a", "done", "a2")
	s.Report("b", "done", "b2")
	texts := func(ms []Msg) (out []string) {
		for _, m := range ms {
			out = append(out, strings.TrimPrefix(m.Text, "done: "))
		}
		return out
	}
	if got, _ := s.InboxFrom(Admiral, "a", true, 1); strings.Join(texts(got), ",") != "a2" {
		t.Fatalf("all from a, n=1: %v", texts(got))
	}
	if s.Unread(Admiral) != 4 {
		t.Fatal("all=true marked something read")
	}
	if got, _ := s.InboxFrom(Admiral, "a", false, 0); strings.Join(texts(got), ",") != "a1,a2" {
		t.Fatalf("from a: %v", texts(got))
	}
	if got := texts(s.Peek(Admiral)); strings.Join(got, ",") != "b1,b2" {
		t.Fatalf("b must stay unread: %v", got)
	}
	s.Report("a", "done", "a3")
	if got, _ := s.Inbox(Admiral, false, 0); strings.Join(texts(got), ",") != "b1,b2,a3" {
		t.Fatalf("plain read after a filtered one: %v", texts(got))
	}
	if _, err := os.Stat(filepath.Join(s.Dir, Admiral, "read.ids")); !os.IsNotExist(err) {
		t.Errorf("read.ids left behind: %v", err)
	}
	if s.Unread(Admiral) != 0 {
		t.Error("still unread")
	}
}
