package mail

import (
	"encoding/json"
	"fmt"
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
		for _, m := range unreadOf(ms, readTrim(v.mark)) {
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
