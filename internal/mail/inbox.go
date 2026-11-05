package mail

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/sturlabragason/fleet/internal/logx"
)

// Send writes text from -> to ("*" = every peer from may message) and returns
// the recipients. Sending needs a granted pair or implicit access (ADR-34).
func (s *Store) Send(from, to, text string) ([]string, error) {
	if err := checkName(from); err != nil {
		return nil, err
	}
	if to != "*" {
		if err := checkName(to); err != nil {
			return nil, err
		}
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
					if !seen[o] && checkName(o) == nil && st.allowed(from, o) {
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
				return fmt.Errorf("no access to %s: request it with fleet_access_request", to)
			}
			rcpt = []string{to}
		}
		id := st.nextID() // one id per message, shared by a broadcast
		for _, r := range rcpt {
			if err := st.deliver(r, id, from, to, KindMsg, text); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	logx.For("mail").Info("send", "from", from, "to", to, "recipients", len(rcpt), "bytes", len(text))
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

// Inbox returns the unread messages and marks them read, or with all the
// last n (default 20) without touching the read marker, so unread messages
// outside that window are never skipped.
func (s *Store) Inbox(agent string, all bool, n int) ([]Msg, error) {
	if err := checkBox(agent); err != nil {
		return nil, err
	}
	var out []Msg
	err := s.update(func(st *state) error {
		ms := s.read(agent)
		if all {
			if n <= 0 {
				n = 20
			}
			out = ms[max(0, len(ms)-n):]
			return nil
		}
		out = unreadOf(ms, readTrim(s.readPath(agent)))
		if len(out) == 0 || out[len(out)-1].ID <= readTrim(s.readPath(agent)) {
			return nil
		}
		return writeAtomic(s.readPath(agent), []byte(out[len(out)-1].ID))
	})
	return out, err
}

// Unread counts agent's unread messages.
func (s *Store) Unread(agent string) int {
	if checkBox(agent) != nil {
		return 0
	}
	return len(unreadOf(s.read(agent), readTrim(s.readPath(agent))))
}

// ClaimNudge reports whether agent should be nudged now: it has unread
// messages and was not nudged since it last read its inbox. A true result is
// recorded (mail/<agent>/nudged = newest id), so one unread batch nudges once.
func (s *Store) ClaimNudge(agent string) (unread int, ok bool, err error) {
	if err := checkBox(agent); err != nil {
		return 0, false, err
	}
	err = s.update(func(st *state) error {
		read := readTrim(s.readPath(agent))
		un := unreadOf(s.read(agent), read)
		unread = len(un)
		if unread == 0 || readTrim(s.nudgePath(agent)) > read {
			return nil
		}
		ok = true
		return writeAtomic(s.nudgePath(agent), []byte(un[len(un)-1].ID))
	})
	return unread, ok, err
}

// ClaimNudgeUpTo claims the nudge for messages up to and including id: true
// only when no nudge has covered id yet, and then the mark moves to it. The
// admiral's report wake uses it, because every report deserves a wake even
// when the admiral has not read the one before it -- the nudge mark says
// what was announced, not what was read (the id order is the message order,
// ADR-51). It returns the mark it replaced, which UnclaimNudgeTo winds back
// to when the wake could not be typed.
func (s *Store) ClaimNudgeUpTo(agent, id string) (prev string, ok bool, err error) {
	if err := checkBox(agent); err != nil {
		return "", false, err
	}
	if id == "" {
		return "", false, nil
	}
	err = s.update(func(st *state) error {
		prev = readTrim(s.nudgePath(agent))
		if prev >= id {
			return nil
		}
		ok = true
		return writeAtomic(s.nudgePath(agent), []byte(id))
	})
	return prev, ok, err
}

// NudgeMark is the newest message id a nudge has announced to agent ("" =
// none). The report wake reads it to see whether the queue holds anything
// the admiral has not been told about yet.
func (s *Store) NudgeMark(agent string) string {
	if checkBox(agent) != nil {
		return ""
	}
	return readTrim(s.nudgePath(agent))
}

// UnclaimNudgeTo winds the mark back to what it was before a claim, so the
// same messages may be announced again. An empty id removes it.
func (s *Store) UnclaimNudgeTo(agent, prev string) error {
	if err := checkBox(agent); err != nil {
		return err
	}
	if prev == "" {
		return s.UnclaimNudge(agent)
	}
	return s.update(func(st *state) error {
		return writeAtomic(s.nudgePath(agent), []byte(prev))
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
