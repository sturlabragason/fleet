package mail

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
)

func newStore(t *testing.T) *Store { return New(t.TempDir()) }

func mustSend(t *testing.T, s *Store, from, to, text string) []string {
	t.Helper()
	r, err := s.Send(from, to, text)
	if err != nil {
		t.Fatalf("send %s->%s: %v", from, to, err)
	}
	return r
}

func TestRequestGrantSend(t *testing.T) {
	s := newStore(t)
	if _, err := s.Send("a", "b", "hi"); err == nil || err.Error() != "no access to b: request it with fleet_access_request" {
		t.Fatalf("send without grant: %v", err)
	}
	st, err := s.Request("a", "b", "need your API")
	if err != nil || st != Pending {
		t.Fatalf("request: %s %v", st, err)
	}
	in, _ := s.Inbox("b", false, 0)
	if len(in) != 1 || in[0].Kind != KindRequest || in[0].From != "a" || !strings.Contains(in[0].Text, "need your API") {
		t.Fatalf("request msg: %+v", in)
	}
	// The requester can never grant itself.
	if err := s.Respond("a", "b", true); err == nil || err.Error() != "only b or the user can decide this request" {
		t.Fatalf("self-grant: %v", err)
	}
	if err := s.Respond("a", "a", true); err == nil {
		t.Fatal("self respond accepted")
	}
	if _, err := s.Send("a", "b", "hi"); err == nil {
		t.Fatal("send while pending")
	}
	if err := s.Respond("b", "a", true); err != nil {
		t.Fatal(err)
	}
	if in, _ := s.Inbox("a", false, 0); len(in) != 1 || in[0].Kind != KindGrant {
		t.Fatalf("grant msg: %+v", in)
	}
	mustSend(t, s, "a", "b", "hello b")
	mustSend(t, s, "b", "a", "hello a")
	if in, _ := s.Inbox("b", false, 0); len(in) != 1 || in[0].Text != "hello b" || in[0].Kind != KindMsg {
		t.Fatalf("b inbox: %+v", in)
	}
	if err := s.Respond("b", "a", true); err == nil || err.Error() != "no pending request from a" {
		t.Fatalf("respond twice: %v", err)
	}
}

func TestDenyAndAskAgain(t *testing.T) {
	s := newStore(t)
	s.Request("a", "b", "")
	if err := s.Respond("b", "a", false); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.PairOf("a", "b"); p.State != Denied || p.DecidedBy != "b" {
		t.Fatalf("pair %+v", p)
	}
	if _, err := s.Send("a", "b", "x"); err == nil {
		t.Fatal("send after deny")
	}
	if in, _ := s.Inbox("a", false, 0); len(in) != 1 || in[0].Kind != KindDeny {
		t.Fatalf("deny msg %+v", in)
	}
	if st, _ := s.Request("a", "b", "again"); st != Pending {
		t.Fatalf("ask again: %s", st)
	}
}

func TestMutualRequestGrants(t *testing.T) {
	s := newStore(t)
	s.Request("a", "b", "")
	st, err := s.Request("b", "a", "")
	if err != nil || st != Granted {
		t.Fatalf("mutual: %s %v", st, err)
	}
	for _, x := range []string{"a", "b"} {
		in, _ := s.Inbox(x, true, 10)
		if in[len(in)-1].Kind != KindGrant {
			t.Errorf("%s: no grant msg %+v", x, in)
		}
	}
	mustSend(t, s, "a", "b", "ok")
}

func TestRevoke(t *testing.T) {
	for _, by := range []string{"a", "b", User} {
		s := newStore(t)
		if err := s.Decide("a", "b", "grant"); err != nil {
			t.Fatal(err)
		}
		mustSend(t, s, "a", "b", "x")
		var err error
		switch by {
		case User:
			err = s.Decide("b", "a", "revoke")
		case "a":
			err = s.Revoke("a", "b")
		default:
			err = s.Revoke("b", "a")
		}
		if err != nil {
			t.Fatalf("revoke by %s: %v", by, err)
		}
		if _, err := s.Send("a", "b", "x"); err == nil {
			t.Errorf("revoke by %s: a can still send", by)
		}
		if _, err := s.Send("b", "a", "x"); err == nil {
			t.Errorf("revoke by %s: b can still send", by)
		}
		if by != User {
			other := map[string]string{"a": "b", "b": "a"}[by]
			in, _ := s.Inbox(other, true, 1)
			if len(in) != 1 || in[0].Kind != KindRevoke {
				t.Errorf("revoke by %s: %s got %+v", by, other, in)
			}
		}
	}
}

func TestBroadcastOnlyGranted(t *testing.T) {
	s := newStore(t)
	s.Decide("a", "b", "grant")
	s.Decide("a", "c", "grant")
	s.Request("a", "d", "") // pending: not a recipient
	r := mustSend(t, s, "a", "*", "all hands")
	sort.Strings(r)
	if strings.Join(r, ",") != "b,c" {
		t.Fatalf("recipients %v", r)
	}
	if in, _ := s.Inbox("d", false, 0); len(in) != 1 || in[0].Kind != KindRequest {
		t.Errorf("d got %+v", in)
	}
	if in, _ := s.Inbox("c", true, 1); in[0].To != "*" || in[0].Text != "all hands" {
		t.Errorf("c got %+v", in)
	}
	if _, err := s.Send("d", "*", "x"); err == nil {
		t.Error("broadcast with no grants")
	}
}

func TestInboxReadAndAll(t *testing.T) {
	s := newStore(t)
	s.Decide("a", "b", "grant")
	s.Inbox("b", false, 0) // drop the grant msg
	for i := 0; i < 3; i++ {
		mustSend(t, s, "a", "b", fmt.Sprint("m", i))
	}
	if n := s.Unread("b"); n != 3 {
		t.Fatalf("unread %d", n)
	}
	in, _ := s.Inbox("b", false, 0)
	if len(in) != 3 || s.Unread("b") != 0 {
		t.Fatalf("inbox %d unread %d", len(in), s.Unread("b"))
	}
	if in, _ := s.Inbox("b", false, 0); len(in) != 0 {
		t.Fatalf("second read %+v", in)
	}
	all, _ := s.Inbox("b", true, 2)
	if len(all) != 2 || all[0].Text != "m1" || all[1].Text != "m2" {
		t.Fatalf("all %+v", all)
	}
	// all=true must not mark unread messages outside the window as read.
	mustSend(t, s, "a", "b", "u1")
	mustSend(t, s, "a", "b", "u2")
	if all, _ := s.Inbox("b", true, 1); len(all) != 1 || s.Unread("b") != 2 {
		t.Fatalf("all=true moved the read marker: unread %d", s.Unread("b"))
	}
	s.Inbox("b", false, 0)
	// Persistence: a new Store on the same dir sees the same state.
	s2 := New(s.Dir)
	mustSend(t, s2, "b", "a", "back")
	if s2.Unread("b") != 0 || len(s2.All()) != 1 {
		t.Fatal("state not persisted")
	}
}

func TestConcurrentSend(t *testing.T) {
	s := newStore(t)
	s.Decide("a", "b", "grant")
	s.Decide("c", "b", "grant")
	s.Inbox("b", false, 0)
	var wg sync.WaitGroup
	for _, from := range []string{"a", "c"} {
		for i := 0; i < 25; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := s.Send(from, "b", fmt.Sprint(from, i)); err != nil {
					t.Error(err)
				}
			}()
		}
	}
	wg.Wait()
	in, _ := s.Inbox("b", false, 0)
	if len(in) != 50 {
		t.Fatalf("got %d lines", len(in))
	}
	for i := 1; i < len(in); i++ {
		if in[i].ID <= in[i-1].ID {
			t.Fatalf("ids not increasing: %s then %s", in[i-1].ID, in[i].ID)
		}
	}
}

func TestCapAndNames(t *testing.T) {
	s := newStore(t)
	s.Decide("a", "b", "grant")
	if _, err := s.Send("a", "b", strings.Repeat("x", MaxText+1)); err == nil {
		t.Error("over cap accepted")
	}
	mustSend(t, s, "a", "b", strings.Repeat("x", MaxText))
	if _, err := s.Request("a", "../etc", ""); err == nil {
		t.Error("path name accepted")
	}
}

func TestForgetAgent(t *testing.T) {
	s := newStore(t)
	s.Decide("a", "b", "grant")
	s.Request("a", "c", "")
	s.Decide("b", "c", "grant")
	mustSend(t, s, "b", "a", "keep me")
	if err := s.ForgetAgent("a"); err != nil {
		t.Fatal(err)
	}
	if len(s.Pairs("a")) != 0 || len(s.All()) != 1 {
		t.Fatalf("pairs left %+v", s.All())
	}
	if _, err := s.Send("b", "a", "x"); err == nil {
		t.Error("send to forgotten agent")
	}
	if s.Unread("a") == 0 {
		t.Error("mailbox dropped")
	}
}

func TestClaimNudgeOncePerBatch(t *testing.T) {
	s := newStore(t)
	s.Decide("a", "b", "grant")
	if _, ok, _ := s.ClaimNudge("b"); !ok {
		t.Fatal("first nudge refused")
	}
	mustSend(t, s, "a", "b", "x")
	if n, ok, _ := s.ClaimNudge("b"); ok || n != 2 {
		t.Fatalf("same batch nudged again (n=%d)", n)
	}
	s.Inbox("b", false, 0)
	if _, ok, _ := s.ClaimNudge("b"); ok {
		t.Fatal("nudge with nothing unread")
	}
	mustSend(t, s, "a", "b", "y")
	if n, ok, _ := s.ClaimNudge("b"); !ok || n != 1 {
		t.Fatalf("new batch not nudged (n=%d)", n)
	}
}

func TestImplicitAccess(t *testing.T) {
	s := newStore(t)
	team := map[string]string{"a": "t1", "b": "t1", "c": "t2"}
	s.Implicit = func(x, y string) bool { return team[x] != "" && team[x] == team[y] }
	s.Peers = func(x string) []string { return []string{"a", "b", "c"} }

	mustSend(t, s, "a", "b", "no request needed")
	if _, err := s.Send("a", "c", "hi"); err == nil {
		t.Fatal("cross-team send without grant")
	}
	if st, err := s.Request("a", "b", "x"); err != nil || st != Granted {
		t.Fatalf("request between teammates: %s %v", st, err)
	}
	if in, _ := s.Inbox("b", false, 0); len(in) != 1 || in[0].Kind != KindMsg {
		t.Fatalf("teammate request must not notify: %+v", in)
	}
	if r := mustSend(t, s, "a", "*", "all"); strings.Join(r, ",") != "b" {
		t.Fatalf("broadcast: %v", r)
	}
	// A cross-team grant joins the broadcast.
	if err := s.Decide("a", "c", "grant"); err != nil {
		t.Fatal(err)
	}
	r := mustSend(t, s, "a", "*", "all")
	sort.Strings(r)
	if strings.Join(r, ",") != "b,c" {
		t.Fatalf("broadcast with grant: %v", r)
	}

	// Revoking implicit access sticks as a denied pair, until granted again.
	if err := s.Revoke("b", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send("a", "b", "hi"); err == nil || s.Allowed("b", "a") {
		t.Fatal("send after revoke of implicit access")
	}
	if p, ok := s.PairOf("a", "b"); !ok || p.State != Denied || p.DecidedBy != "b" {
		t.Fatalf("revoked pair: %+v %v", p, ok)
	}
	if st, _ := s.Request("a", "b", "again"); st != Pending {
		t.Fatalf("request after revoke: %s", st)
	}
	if err := s.Respond("b", "a", true); err != nil {
		t.Fatal(err)
	}
	mustSend(t, s, "a", "b", "back")
	if err := s.Decide("a", "b", "revoke"); err != nil {
		t.Fatal(err)
	}
	if s.Allowed("a", "b") {
		t.Fatal("user revoke of teammates")
	}
}

func TestLog(t *testing.T) {
	s := newStore(t)
	s.Implicit = func(x, y string) bool { return true }
	s.Peers = func(string) []string { return []string{"a", "b", "c"} }
	mustSend(t, s, "a", "b", "one")
	mustSend(t, s, "b", "*", "two")
	if _, err := s.Request("a", "a", "self"); err == nil {
		t.Fatal("self request")
	}
	l := s.Log(0)
	if len(l) != 2 || l[0].Text != "one" || l[1].To != "*" || l[1].From != "b" {
		t.Fatalf("log: %+v", l)
	}
	if l := s.Log(1); len(l) != 1 || l[0].Text != "two" {
		t.Fatalf("log n=1: %+v", l)
	}
}

// ClaimNudgeUpTo claims per message id, not per read mark: the admiral's
// report wake needs a second report to wake it even with the first unread.
func TestClaimNudgeUpTo(t *testing.T) {
	s := newStore(t)
	s.Decide("a", "b", "grant")
	mustSend(t, s, "a", "b", "x")
	first := s.Peek("b")[0].ID
	if prev, ok, _ := s.ClaimNudgeUpTo("b", first); !ok || prev != "" {
		t.Fatalf("first claim refused (prev %q)", prev)
	}
	if _, ok, _ := s.ClaimNudgeUpTo("b", first); ok {
		t.Fatal("same message claimed twice")
	}
	if got := s.NudgeMark("b"); got != first {
		t.Fatalf("mark %q, want %q", got, first)
	}
	mustSend(t, s, "a", "b", "y")
	un := s.Peek("b")
	second := un[len(un)-1].ID
	prev, ok, _ := s.ClaimNudgeUpTo("b", second)
	if !ok || prev != first {
		t.Fatalf("a newer message, still unread, was not claimable (prev %q)", prev)
	}
	if _, ok, _ := s.ClaimNudgeUpTo("b", ""); ok {
		t.Fatal("claimed nothing")
	}
	// Winding back leaves the first message announced and the second not:
	// a failed wake must not re-announce what was already typed.
	if err := s.UnclaimNudgeTo("b", prev); err != nil {
		t.Fatal(err)
	}
	if got := s.NudgeMark("b"); got != first {
		t.Fatalf("wound back to %q, want %q", got, first)
	}
	if _, ok, _ := s.ClaimNudgeUpTo("b", second); !ok {
		t.Fatal("unclaimed message not claimable again")
	}
	if err := s.UnclaimNudgeTo("b", ""); err != nil {
		t.Fatal(err)
	}
	if got := s.NudgeMark("b"); got != "" {
		t.Fatalf("wound back to %q, want none", got)
	}
}
