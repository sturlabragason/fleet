package mail

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/sturlabragason/fleet/internal/logx"
)

// Admiral is the reserved recipient of agent reports (DESIGN §6.7) and the
// name of its mailbox.
const Admiral = "admiral"

// LegacySupervisor is the admiral's name before ADR-26. It stays reserved and
// maps to the admiral's mailbox.
const LegacySupervisor = "supervisor"

// Supervisor is the admiral's mailbox.
//
// Deprecated: use Admiral (ADR-26).
const Supervisor = Admiral

// KindReport is a report from an agent to the admiral.
const KindReport = "report"

// MaxSummary caps the one-line headline of a report (board, tile, the line
// typed into the admiral pane), in runes.
const MaxSummary = 200

// MaxReport caps the stored report text, in runes. The full text is what
// fleet_reports returns, so it outlives the agent (DESIGN §6.7).
const MaxReport = 8000

// ReportStates are the states an agent can report.
var ReportStates = []string{"done", "blocked", "question", "progress"}

// ValidState reports whether st is one of ReportStates.
func ValidState(st string) bool {
	for _, s := range ReportStates {
		if s == st {
			return true
		}
	}
	return false
}

// FoldSummary folds whitespace (newlines included) to single spaces, drops
// control and other non-printable runes (the summary is typed into the
// admiral pane, where ESC or ^C would act as keys), and caps the result at
// MaxSummary runes.
func FoldSummary(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsPrint(r) {
			return r
		}
		return -1
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > MaxSummary {
		s = string(r[:MaxSummary-1]) + "…"
	}
	return s
}

// CleanReport keeps a report's full text: line breaks and tabs stay, other
// control and non-printable runes go, trailing spaces and blank edges are
// trimmed, and the result is capped at MaxReport runes.
func CleanReport(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || unicode.IsPrint(r) || r == ' ' {
			return r
		}
		if unicode.IsSpace(r) {
			return ' '
		}
		return -1
	}, s)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	s = strings.Trim(strings.Join(lines, "\n"), "\n ")
	if r := []rune(s); len(r) > MaxReport {
		s = string(r[:MaxReport-1]) + "…"
	}
	return s
}

// Report writes a report from an agent to the admiral's inbox. It needs
// no grant. The message text is "<state>: <full report>" (CleanReport); the
// one-line headline is FoldSummary of it.
func (s *Store) Report(from, st, summary string) (Msg, error) {
	return s.ReportTo(from, Admiral, st, summary)
}

// ReportTo is Report into a mailbox of fleet's choosing: the admiral's, or
// a leader's when the reporter is in its squad (ADR-53). The message is a
// report either way, so the badge, fleet_reports and the board treat it the
// same wherever it lands.
func (s *Store) ReportTo(from, to, st, summary string) (Msg, error) {
	if err := checkName(from); err != nil {
		return Msg{}, err
	}
	if to == "" {
		to = Admiral
	}
	if err := checkBox(to); err != nil {
		return Msg{}, err
	}
	if !ValidState(st) {
		return Msg{}, fmt.Errorf("bad state %q: want one of %s", st, strings.Join(ReportStates, ", "))
	}
	summary = CleanReport(summary)
	if FoldSummary(summary) == "" {
		return Msg{}, fmt.Errorf("empty summary")
	}
	var m Msg
	err := s.update(func(x *state) error {
		id := x.nextID()
		m = Msg{ID: id, From: from, To: to, Kind: KindReport, Text: st + ": " + summary, T: time.Now()}
		return x.deliver(to, id, from, to, KindReport, m.Text)
	})
	if err != nil {
		return Msg{}, err
	}
	logx.For("mail").Info("report", "from", from, "to", to, "state", st)
	return m, nil
}

// Peek returns agent's unread messages without marking them read.
func (s *Store) Peek(agent string) []Msg {
	if checkBox(agent) != nil {
		return nil
	}
	return unreadOf(s.read(agent), readTrim(s.readPath(agent)))
}
