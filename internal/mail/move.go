package mail

import (
	"encoding/json"
	"fmt"
	"slices"
)

// MoveReports takes the unread reports in the admiral box from whose sender
// satisfies who, and files them in the admiral box to under fresh ids, so
// the receiver's read mark cannot hide them (DESIGN §6.12: give, and an
// admiral closing). who nil moves every unread report. It returns the
// copies as filed.
func (s *Store) MoveReports(from, to string, who func(sender string) bool) ([]Msg, error) {
	if !IsAdmiralBox(from) || !IsAdmiralBox(to) {
		return nil, fmt.Errorf("reports move between admirals only")
	}
	if s.agentDir(from) == s.agentDir(to) {
		return nil, nil
	}
	var moved []Msg
	err := s.update(func(st *state) error {
		all := s.read(from)
		ms, v := s.readView(from, true)
		pick := map[string]bool{}
		for _, m := range v.unread(ms) {
			if who == nil || who(m.From) {
				pick[m.ID+"\x00"+m.From] = true
			}
		}
		if len(pick) == 0 {
			return nil
		}
		var keep []byte
		for _, m := range all {
			if pick[m.ID+"\x00"+m.From] && m.Kind == KindReport {
				id := st.nextID()
				if err := st.deliver(to, id, m.From, to, KindReport, m.Text); err != nil {
					return err
				}
				m.ID, m.To = id, to
				moved = append(moved, m)
				continue
			}
			b, _ := json.Marshal(m)
			keep = append(keep, append(b, '\n')...)
		}
		return writeAtomic(s.inboxPath(from), keep)
	})
	return moved, err
}

// CopyLatestReport files a fresh, unread copy of sender's newest report in
// the admiral box from into the admiral box to, read or not, and leaves the
// original (ADR-185: a handover whose reports the giver already read still
// tells the receiver where the agent is). ok is false when sender has no
// report in from.
func (s *Store) CopyLatestReport(from, to, sender string) (m Msg, ok bool, err error) {
	if !IsAdmiralBox(from) || !IsAdmiralBox(to) {
		return Msg{}, false, fmt.Errorf("reports move between admirals only")
	}
	err = s.update(func(st *state) error {
		all := s.read(from)
		for _, x := range slices.Backward(all) {
			if x.Kind != KindReport || x.From != sender {
				continue
			}
			m = x
			m.ID, m.To = st.nextID(), to
			ok = true
			return st.deliver(to, m.ID, m.From, to, KindReport, m.Text)
		}
		return nil
	})
	return m, ok, err
}
