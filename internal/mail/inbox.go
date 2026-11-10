package mail

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/logx"
)

// Send writes text from -> to ("*" = every peer from may message) and returns
// the recipients. Sending needs a granted pair or implicit access (ADR-34).
func (s *Store) Send(from, to, text string) ([]string, error) {
	return s.SendAs(from, to, text, false)
}

// SendAs is Send with the FYI flag: fyi mail needs no action (ADR-92).
func (s *Store) SendAs(from, to, text string, fyi bool) ([]string, error) {
	if err := checkPair(from, to); err != nil {
		return nil, err
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("empty message")
	}
	if len(text) > MaxText {
		return nil, fmt.Errorf("message too long: %d bytes (max %d)", len(text), MaxText)
	}
	var rcpt []string
	err := s.update(func(st *state) error {
		if to == "*" {
			seen := map[string]bool{from: true}
			for _, p := range st.pairs {
				if o := p.Other(from); p.State == Granted && p.Has(from) && !seen[o] {
					seen[o] = true
					rcpt = append(rcpt, o)
				}
			}
			if s.Peers != nil {
				for _, o := range s.Peers(from) {
					if !seen[o] && checkBox(o) == nil && st.allowed(from, o) {
						seen[o] = true
						rcpt = append(rcpt, o)
					}
				}
			}
			if len(rcpt) == 0 {
				return fmt.Errorf("no access to any agent: request it with fleet_access_request")
			}
		} else {
			if from == to || !st.allowed(from, to) {
				return fmt.Errorf("no access to %s: request it with fleet_access_request%s", to, s.hint(from, to))
			}
			rcpt = []string{to}
		}
		id := st.nextID() // one id per message, shared by a broadcast
		for _, r := range rcpt {
			m := Msg{ID: id, From: from, To: to, Kind: KindMsg, Text: text, T: time.Now(), FYI: fyi}
			if err := st.deliverMsg(r, m); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	logx.For("mail").Info("send", "from", from, "to", to, "recipients", len(rcpt), "bytes", len(text), "fyi", fyi)
	return rcpt, nil
}

// Log returns the last n (default 50) agent-to-agent messages across every
// inbox, oldest first; a broadcast shows once with To "*". It is the
// admiral's view of the traffic (fleet_access limit=N, fleet access log).
func (s *Store) Log(n int) []Msg {
	if n <= 0 {
		n = 50
	}
	boxes, _ := os.ReadDir(s.Dir)
	byID := map[string]Msg{}
	for _, b := range boxes {
		if !b.IsDir() {
			continue
		}
		for _, m := range s.read(b.Name()) {
			if m.Kind != KindMsg {
				continue
			}
			if _, dup := byID[m.ID+"\x00"+m.From]; !dup {
				byID[m.ID+"\x00"+m.From] = m
			}
		}
	}
	out := make([]Msg, 0, len(byID))
	for _, m := range byID {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out[max(0, len(out)-n):]
}

// read returns every message in agent's inbox, oldest first.
func (s *Store) read(agent string) []Msg {
	f, err := os.Open(s.inboxPath(agent))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Msg
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var m Msg
		if err := json.Unmarshal(sc.Bytes(), &m); err == nil && m.ID != "" {
			out = append(out, m)
		}
	}
	return out
}

func unreadOf(ms []Msg, read string) []Msg {
	for i, m := range ms {
		if m.ID > read {
			return ms[i:]
		}
	}
	return nil
}

// view is what one reader of a box sees: every message, or for an admiral's
// box only its reports or only the rest, each with a read mark of its own
// (DESIGN §6.12): fleet_reports must not eat messages and fleet_inbox must
// not eat reports.
type view struct {
	keep func(Msg) bool
	mark string
}

func (s *Store) viewOf(box string, reports bool) view {
	switch {
	case !IsAdmiralBox(box):
		return view{func(Msg) bool { return true }, s.readPath(box)}
	case reports:
		return view{func(m Msg) bool { return m.Kind == KindReport }, s.readPath(box)}
	}
	return view{func(m Msg) bool { return m.Kind != KindReport }, s.msgsReadPath(box)}
}

// idsPath holds the ids read one by one above the read mark (InboxFrom),
// one per line, so reading one sender's reports leaves the rest unread.
func (v view) idsPath() string { return v.mark + ".ids" }

func readIDs(path string) map[string]bool {
	ids := map[string]bool{}
	for _, id := range strings.Fields(readTrim(path)) {
		ids[id] = true
	}
	return ids
}

// unread is the part of ms (this view's messages) not yet read: newer than
// the mark and not read one by one.
func (v view) unread(ms []Msg) []Msg {
	out := unreadOf(ms, readTrim(v.mark))
	ids := readIDs(v.idsPath())
	if len(ids) == 0 {
		return out
	}
	var keep []Msg
	for _, m := range out {
		if !ids[m.ID] {
			keep = append(keep, m)
		}
	}
	return keep
}

// markRead marks read in this view and moves the mark over every message
// of ms that is now read, keeping above it only the ids it cannot cover.
func (v view) markRead(ms, read []Msg) error {
	if len(read) == 0 {
		return nil
	}
	mark := readTrim(v.mark)
	ids := readIDs(v.idsPath())
	for _, m := range read {
		ids[m.ID] = true
	}
	newMark := mark
	for _, m := range unreadOf(ms, mark) {
		if !ids[m.ID] {
			break
		}
		newMark = m.ID
	}
	var rest []string
	for id := range ids {
		if id > newMark {
			rest = append(rest, id)
		}
	}
	sort.Strings(rest)
	if newMark != mark {
		if err := writeAtomic(v.mark, []byte(newMark)); err != nil {
			return err
		}
	}
	if len(rest) == 0 {
		if err := os.Remove(v.idsPath()); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return writeAtomic(v.idsPath(), []byte(strings.Join(rest, "\n")+"\n"))
}

func (s *Store) readView(box string, reports bool) ([]Msg, view) {
	v := s.viewOf(box, reports)
	var out []Msg
	for _, m := range s.read(box) {
		if v.keep(m) {
			out = append(out, m)
		}
	}
	return out, v
}

// Inbox returns the unread messages and marks them read, or with all the
// last n (default 20) without touching the read marker, so unread messages
// outside that window are never skipped. For an admiral's box these are the
// reports; its other messages are InboxMsgs.
func (s *Store) Inbox(agent string, all bool, n int) ([]Msg, error) {
	return s.inbox(agent, true, all, n, "")
}

// InboxFrom is Inbox for the messages from one sender only. Without all it
// marks just those read: the others stay unread however they interleave.
func (s *Store) InboxFrom(agent, from string, all bool, n int) ([]Msg, error) {
	if err := checkName(from); err != nil {
		return nil, err
	}
	return s.inbox(agent, true, all, n, from)
}

// InboxMsgs is Inbox for what an admiral's box holds besides reports:
// messages from other admirals and access requests, grants and revokes.
func (s *Store) InboxMsgs(agent string, all bool, n int) ([]Msg, error) {
	if !IsAdmiralBox(agent) {
		return nil, fmt.Errorf("%q is not an admiral", agent)
	}
	return s.inbox(agent, false, all, n, "")
}

func (s *Store) inbox(agent string, reports, all bool, n int, from string) ([]Msg, error) {
	if err := checkBox(agent); err != nil {
		return nil, err
	}
	var out []Msg
	err := s.update(func(st *state) error {
		ms, v := s.readView(agent, reports)
		if all {
			if n <= 0 {
				n = 20
			}
			pick := onlyFrom(ms, from)
			out = pick[max(0, len(pick)-n):]
			return nil
		}
		out = onlyFrom(v.unread(ms), from)
		return v.markRead(ms, out)
	})
	return out, err
}

func onlyFrom(ms []Msg, from string) []Msg {
	if from == "" {
		return ms
	}
	var out []Msg
	for _, m := range ms {
		if m.From == from {
			out = append(out, m)
		}
	}
	return out
}

// Unread counts agent's unread messages (an admiral's: unread reports).
func (s *Store) Unread(agent string) int { return len(s.Peek(agent)) }

// UnreadFYI counts agent's unread messages that need no action (ADR-92).
func (s *Store) UnreadFYI(agent string) int {
	n := 0
	for _, m := range s.Peek(agent) {
		if m.FYI {
			n++
		}
	}
	return n
}

// UnreadMsgs counts the unread messages of an admiral's box that are not
// reports.
func (s *Store) UnreadMsgs(agent string) int { return len(s.PeekMsgs(agent)) }

// ClaimNudge reports whether agent should be nudged now: it has unread
// messages and was not nudged since it last read its inbox. A true result is
// recorded (mail/<agent>/nudged = newest id), so one unread batch nudges once.
func (s *Store) ClaimNudge(agent string) (unread int, ok bool, err error) {
	if err := checkBox(agent); err != nil {
		return 0, false, err
	}
	err = s.update(func(st *state) error {
		read := readTrim(s.readPath(agent))
		un := view{nil, s.readPath(agent)}.unread(s.read(agent))
		unread = len(un)
		if unread == 0 || readTrim(s.nudgePath(agent)) > read {
			return nil
		}
		ok = true
		return writeAtomic(s.nudgePath(agent), []byte(un[len(un)-1].ID))
	})
	return unread, ok, err
}

// ClaimWakeUpTo claims the report wake for messages up to and including id:
// true only when no wake has covered id yet, and then the wake mark moves to
// it. Every report deserves a wake even when the box owner has not read the
// one before it -- the mark says what was announced, not what was read (the
// id order is the message order, ADR-51). The mark is mail/<box>/woken, apart
// from the peer-mail nudge mark, so a peer message nudged into a leader's
// pane cannot swallow a squad report's wake (ADR-59). It returns the mark it
// replaced, which UnclaimWakeTo winds back to when the wake could not be typed.
func (s *Store) ClaimWakeUpTo(agent, id string) (prev string, ok bool, err error) {
	if err := checkBox(agent); err != nil {
		return "", false, err
	}
	if id == "" {
		return "", false, nil
	}
	err = s.update(func(st *state) error {
		prev = readTrim(s.wokenPath(agent))
		if prev >= id {
			return nil
		}
		ok = true
		return writeAtomic(s.wokenPath(agent), []byte(id))
	})
	return prev, ok, err
}

// WakeMark is the newest message id a report wake has announced to agent
// ("" = none). The wake reads it to see whether the queue holds anything the
// box owner has not been told about yet.
func (s *Store) WakeMark(agent string) string {
	if checkBox(agent) != nil {
		return ""
	}
	return readTrim(s.wokenPath(agent))
}

// UnclaimWakeTo winds the wake mark back to what it was before a claim, so
// the same reports may be announced again. An empty id removes it.
func (s *Store) UnclaimWakeTo(agent, prev string) error {
	if err := checkBox(agent); err != nil {
		return err
	}
	return s.update(func(st *state) error {
		if prev == "" {
			if err := os.Remove(s.wokenPath(agent)); err != nil && !os.IsNotExist(err) {
				return err
			}
			return nil
		}
		return writeAtomic(s.wokenPath(agent), []byte(prev))
	})
}

// UnclaimNudge undoes ClaimNudge when the nudge could not be typed, so the
// same unread batch may be nudged again.
func (s *Store) UnclaimNudge(agent string) error {
	if err := checkBox(agent); err != nil {
		return err
	}
	return s.update(func(st *state) error {
		if err := os.Remove(s.nudgePath(agent)); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	})
}
