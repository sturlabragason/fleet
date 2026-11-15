package mail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReportToAdmiral(t *testing.T) {
	s := newStore(t)
	m, err := s.Report("a", "done", "fixed nav\non branch feat/x")
	if err != nil {
		t.Fatalf("report without grant: %v", err)
	}
	if m.Kind != KindReport || m.To != Admiral || m.Text != "done: fixed nav\non branch feat/x" {
		t.Fatalf("msg %+v", m)
	}
	if got := FoldSummary("ok\x1b[201~\x03x\x7f"); got != "ok[201~x" {
		t.Fatalf("control runes kept: %q", got)
	}
	if n := s.Unread(Admiral); n != 1 {
		t.Fatalf("admiral unread %d", n)
	}
	if p := s.Peek(Admiral); len(p) != 1 || s.Unread(Admiral) != 1 {
		t.Fatalf("peek must not mark read: %+v", p)
	}
	if _, ok, _ := s.ClaimNudge(Admiral); !ok {
		t.Fatal("first report in a batch must claim the nudge")
	}
	in, err := s.Inbox(Admiral, false, 0)
	if err != nil || len(in) != 1 || in[0].ID != m.ID || in[0].From != "a" {
		t.Fatalf("admiral inbox: %+v %v", in, err)
	}
	if s.Unread(Admiral) != 0 {
		t.Fatal("inbox did not mark read")
	}
	if _, err := s.Report("a", "finished", "x"); err == nil {
		t.Fatal("bad state accepted")
	}
	if _, err := s.Report("a", "progress", " \n "); err == nil {
		t.Fatal("empty summary accepted")
	}
	// The full text is kept (DESIGN §6.7); only the headline is capped.
	full := strings.Repeat("é", 500) + "\n\ndetail\x1b[2J line  "
	long, _ := s.Report("a", "progress", full)
	if got := strings.TrimPrefix(long.Text, "progress: "); got != strings.Repeat("é", 500)+"\n\ndetail[2J line" {
		t.Fatalf("full report not kept: %q", got)
	}
	if got := []rune(FoldSummary(long.Text)); len(got) != MaxSummary || got[len(got)-1] != '…' {
		t.Fatalf("headline not capped at %d runes: %d", MaxSummary, len(got))
	}
	huge, _ := s.Report("a", "progress", strings.Repeat("x", MaxReport+50))
	if got := []rune(strings.TrimPrefix(huge.Text, "progress: ")); len(got) != MaxReport || got[len(got)-1] != '…' {
		t.Fatalf("report not capped at %d runes: %d", MaxReport, len(got))
	}
	for _, from := range []string{Admiral, LegacySupervisor, User} {
		if _, err := s.Report(from, "done", "x"); err == nil {
			t.Fatalf("%s reported as an agent", from)
		}
	}
}

func TestAdmiralIsReserved(t *testing.T) {
	s := newStore(t)
	for _, n := range []string{Admiral, LegacySupervisor} {
		if _, err := s.Send("a", n, "hi"); err == nil {
			t.Fatalf("send to %s accepted", n)
		}
		if _, err := s.Send(n, "a", "hi"); err == nil {
			t.Fatalf("send as %s accepted", n)
		}
		if _, err := s.Request("a", n, "x"); err == nil {
			t.Fatalf("request to %s accepted", n)
		}
		if _, err := s.Request(n, "a", "x"); err == nil {
			t.Fatalf("request as %s accepted", n)
		}
		if checkName(n) == nil {
			t.Fatalf("checkName(%q) ok", n)
		}
		if err := checkBox(n); err != nil {
			t.Fatalf("checkBox(%q): %v", n, err)
		}
	}
}

// An old binary still writes reports into mail/supervisor/inbox.jsonl; after
// the migration mail/supervisor is a symlink, so Inbox(Admiral) sees them.
func TestLegacySupervisorBoxIsShared(t *testing.T) {
	s := newStore(t)
	if _, err := s.Report("a", "done", "new"); err != nil {
		t.Fatal(err)
	}
	if dst, err := os.Readlink(filepath.Join(s.Dir, LegacySupervisor)); err != nil || dst != Admiral {
		t.Fatalf("legacy link: %q %v", dst, err)
	}
	f, err := os.OpenFile(filepath.Join(s.Dir, "supervisor", "inbox.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"id":"99999999999999999999","from":"b","to":"supervisor","kind":"report","text":"done: old"}` + "\n")
	f.Close()
	in, err := s.Inbox(Admiral, false, 0)
	if err != nil || len(in) != 2 || in[1].Text != "done: old" {
		t.Fatalf("inbox: %+v %v", in, err)
	}
	if s.Unread(LegacySupervisor) != 0 {
		t.Fatal("legacy name is not the same box")
	}
}

// A pre-existing mail/supervisor box is read in place until a locked
// mutation moves it to mail/admiral and leaves mail/supervisor as a symlink.
func TestLegacySupervisorBoxMigrates(t *testing.T) {
	s := newStore(t)
	old := filepath.Join(s.Dir, "supervisor")
	os.MkdirAll(old, 0o755)
	os.WriteFile(filepath.Join(old, "inbox.jsonl"), []byte(`{"id":"00000000000000000001","from":"b","to":"supervisor","kind":"report","text":"done: before"}`+"\n"), 0o644)
	if n := s.Unread(Admiral); n != 1 {
		t.Fatalf("unread before migration %d", n)
	}
	if st, err := os.Lstat(old); err != nil || st.Mode()&os.ModeSymlink != 0 {
		t.Fatal("a read moved mail/supervisor outside the mail lock")
	}
	if _, err := s.Report("a", "done", "after"); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(s.Dir, Admiral, "inbox.jsonl")); err != nil || st.Size() == 0 {
		t.Fatal("inbox not moved to mail/admiral")
	}
	if st, _ := os.Lstat(old); st.Mode()&os.ModeSymlink == 0 {
		t.Fatal("mail/supervisor is not a symlink")
	}
	if n := s.Unread(Admiral); n != 2 {
		t.Fatalf("unread after migration %d", n)
	}
}
