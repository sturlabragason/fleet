package fleet

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sturlabragason/fleet/internal/logx"
)

// Result is what a team action did to one member.
type Result struct {
	Agent string
	OK    bool
	Note  string // why it was skipped
}

// FormatResults renders results as one line: "sent: a1, a2; skipped: a3 (not running)".
func FormatResults(verb string, rs []Result) string {
	var ok, skip []string
	for _, r := range rs {
		if r.OK {
			ok = append(ok, r.Agent)
		} else {
			skip = append(skip, r.Agent+" ("+r.Note+")")
		}
	}
	if len(ok) == 0 && len(skip) == 0 {
		return "team has no members"
	}
	var parts []string
	if len(ok) > 0 {
		parts = append(parts, verb+": "+strings.Join(ok, ", "))
	}
	if len(skip) > 0 {
		parts = append(parts, "skipped: "+strings.Join(skip, ", "))
	}
	return strings.Join(parts, "; ")
}

// livePanes maps agent name to its pane id, skipping panes whose process exited.
func (f *Fleet) livePanes() map[string]string {
	out := map[string]string{}
	ps, _ := f.T.ListPanes("")
	for _, p := range ps {
		if p.Agent != "" && !p.Dead {
			out[p.Agent] = p.ID
		}
	}
	return out
}

// TeamMember is one member in a listing.
type TeamMember struct {
	Name    string `json:"name"`
	Running bool   `json:"running"`
}

// TeamRow is one team in a listing.
type TeamRow struct {
	Name    string       `json:"name"`
	Color   string       `json:"color"`
	Members []TeamMember `json:"members"`
}

// TeamList returns every team, sorted, with running members marked.
func (f *Fleet) TeamList() []TeamRow {
	live := f.livePanes()
	tf := f.Teams.Load()
	out := []TeamRow{}
	for _, n := range tf.Names() {
		t := tf.Teams[n]
		r := TeamRow{Name: n, Color: t.Color, Members: []TeamMember{}}
		for _, m := range t.Members {
			_, ok := live[m]
			r.Members = append(r.Members, TeamMember{Name: m, Running: ok})
		}
		out = append(out, r)
	}
	return out
}

// TeamSend sends text to every running member with Send (safer submit).
// Unless confirmed, a member on a prompt is skipped: Enter would answer it.
func (f *Fleet) TeamSend(team, text string, confirmed bool) ([]Result, error) {
	ms, err := f.Teams.Members(team)
	if err != nil {
		return nil, err
	}
	live := f.livePanes()
	var rs []Result
	for _, m := range ms {
		_, ok := live[m]
		if !ok {
			rs = append(rs, Result{m, false, "not running"})
			continue
		}
		if !confirmed {
			if w, err := f.Prompting(m); err != nil || w != "" {
				rs = append(rs, Result{m, false, waitingNote(w, err)})
				continue
			}
		}
		if err := f.Send(m, text); err != nil {
			rs = append(rs, Result{m, false, err.Error()})
			continue
		}
		rs = append(rs, Result{Agent: m, OK: true})
	}
	logx.For("fleet").Info("team send", "team", team, "bytes", len(text), "members", len(ms))
	return rs, nil
}

// TeamClose closes every member on the board. The team stays (empty).
func (f *Fleet) TeamClose(team string) ([]Result, error) {
	ms, err := f.Teams.Members(team)
	if err != nil {
		return nil, err
	}
	onBoard := f.Store.Load().Agents
	var rs []Result
	for _, m := range ms {
		if _, ok := onBoard[m]; !ok {
			rs = append(rs, Result{m, false, "not on the board"})
			continue
		}
		if err := f.Close(m); err != nil {
			rs = append(rs, Result{m, false, err.Error()})
			continue
		}
		rs = append(rs, Result{Agent: m, OK: true})
	}
	return rs, nil
}

// Waller is the part of wall.Wall team show/hide needs.
type Waller interface {
	Place(pane string) (bool, error) // onto the wall without using the ▶ slot
	Hide(pane string) error
	OnWall() map[string]bool
	Slots() (int, error)
}

// TeamShow puts running members on the wall, stopping at the wall's limit.
func (f *Fleet) TeamShow(w Waller, team string) ([]Result, error) {
	ms, err := f.Teams.Members(team)
	if err != nil {
		return nil, err
	}
	slots, err := w.Slots()
	if err != nil {
		return nil, fmt.Errorf("read wall: %w", err)
	}
	live := f.livePanes()
	on := w.OnWall()
	// Members already on the wall hold their slots first.
	sort.SliceStable(ms, func(i, j int) bool { return on[ms[i]] && !on[ms[j]] })
	var rs []Result
	shown, full := 0, false
	for _, m := range ms {
		p, ok := live[m]
		switch {
		case !ok:
			rs = append(rs, Result{m, false, "not running"})
			continue
		case on[m]:
			shown++
			rs = append(rs, Result{Agent: m, OK: true})
			continue
		case full || shown >= slots:
			rs = append(rs, Result{m, false, "did not fit"})
			continue
		}
		ok, err := w.Place(p)
		switch {
		case err != nil:
			rs = append(rs, Result{m, false, err.Error()})
		case !ok:
			full = true
			rs = append(rs, Result{m, false, "did not fit"})
		default:
			shown++
			rs = append(rs, Result{Agent: m, OK: true})
		}
	}
	// A later Show may have evicted an earlier member; report what is really there.
	final := w.OnWall()
	for i, r := range rs {
		if r.OK && !final[r.Agent] {
			rs[i] = Result{r.Agent, false, "did not fit"}
		}
	}
	return rs, nil
}

// TeamHide takes members that are on the wall back to their hidden windows.
func (f *Fleet) TeamHide(w Waller, team string) ([]Result, error) {
	ms, err := f.Teams.Members(team)
	if err != nil {
		return nil, err
	}
	live := f.livePanes()
	on := w.OnWall()
	var rs []Result
	for _, m := range ms {
		p, ok := live[m]
		switch {
		case !ok:
			rs = append(rs, Result{m, false, "not running"})
		case !on[m]:
			rs = append(rs, Result{m, false, "not on the wall"})
		default:
			if err := w.Hide(p); err != nil {
				rs = append(rs, Result{m, false, err.Error()})
			} else {
				rs = append(rs, Result{Agent: m, OK: true})
			}
		}
	}
	return rs, nil
}

// MovedNote describes agents Add moved from another team ("" if none).
func MovedNote(moved map[string]string) string {
	if len(moved) == 0 {
		return ""
	}
	var ns []string
	for a, from := range moved {
		ns = append(ns, fmt.Sprintf("%s from %s", a, from))
	}
	sort.Strings(ns)
	return " (moved " + strings.Join(ns, ", ") + ")"
}
