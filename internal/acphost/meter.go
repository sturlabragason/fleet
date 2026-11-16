package acphost

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/acp"
	"github.com/sturlabragason/fleet/internal/spend"
)

// The usage footer (ADR-151): the pane's last row shows what the agent's
// own TUI would put in its status line, from what fleet can read without a
// network call. Rate-limit windows come only from where the provider
// reports them (claude-agent-acp's usage_update _meta; Copilot's premium
// allowance in its events.jsonl, left out when unlimited); a window nobody
// reported is left out, never guessed. Context fill comes from usage_update
// (used/size), else from the spend reader; the model from the usage, else
// the session's model, with the reasoning effort when the
// agent reports one (an ACP "thought_level" config option); spend is the session's cost ($) or the spend reader's
// short figure.
//
//	5h ▰▰▱▱ 60% left (4h56m)  7d ▰▱▱▱ 80% left (3d)  ctx ▰▱▱▱ 12%  Claude Opus 5.5 · high  $1.20

// meterEvery is how often the host re-reads the spend reader (one stat when
// the session file is unchanged); a finished turn reads at once.
const meterEvery = 15 * time.Second

// window is one rate-limit window.
type window struct {
	label  string
	used   int       // percent
	resets time.Time // zero when not reported
}

// windowOrder and windowLabel are the windows the footer knows how to name,
// in the order shown. A type not listed (overage) is shown by its raw name
// after them (ADR-157).
var (
	windowOrder = []string{"five_hour", "seven_day", "seven_day_opus", "seven_day_sonnet", "copilot_premium"}
	windowLabel = map[string]string{"five_hour": "5h", "seven_day": "7d", "seven_day_opus": "7d opus",
		"seven_day_sonnet": "7d sonnet", "copilot_premium": "premium"}
)

// meter is what the footer shows.
type meter struct {
	windows    map[string]window
	ctx        int  // percent
	hasCtx     bool // ctx is known
	ctxUpdated bool // ctx came from a usage_update, which beats the spend reader
	model      string
	usageModel bool              // model came from a usage_update
	names      map[string]string // model id → display name
	effort     string            // reasoning effort, "" when not reported
	effortSet  bool              // effort came from a config_option_update
	cost       string            // the session cost a usage_update reported
	spent      string            // the spend reader's short figure
}

// setModels records the session's model and the names the agent gives its
// models, so the footer says "Claude Opus 5.5", not an id.
func (m *meter) setModels(ms acp.Models) {
	if m.names == nil {
		m.names = map[string]string{}
	}
	for _, x := range ms.Available {
		if x.Name != "" {
			m.names[x.ModelID] = x.Name
		}
	}
	if ms.Current != "" && !m.usageModel {
		m.model = ms.Current
	}
	if !m.effortSet {
		m.effort = ms.Effort
	}
}

// config reads a config_option_update, which beats the session result: a
// model switch during setup sends one with the new model's effort, or none.
func (m *meter) config(opts []acp.ConfigOption) {
	m.effort, m.effortSet = acp.Effort(opts), true
}

// usage reads a usage_update.
func (m *meter) usage(u acp.SessionUpdate) {
	if u.Size > 0 {
		m.ctx, m.hasCtx, m.ctxUpdated = int(u.Used*100/u.Size), true, true
	}
	if u.Cost != nil && u.Cost.Currency == "USD" && u.Cost.Amount > 0 {
		m.cost = fmt.Sprintf("$%.2f", u.Cost.Amount)
	}
	rl, model := u.UsageMeta()
	if model != "" {
		m.model, m.usageModel = model, true
	}
	if rl != nil && rl.Type != "" {
		used := rl.Utilization
		if used <= 1 {
			used *= 100
		}
		label := windowLabel[rl.Type]
		if label == "" {
			label = rl.Type
		}
		w := window{label: label, used: int(used + 0.5)}
		if rl.ResetsAt > 0 {
			w.resets = time.Unix(rl.ResetsAt, 0)
		}
		m.setWindow(rl.Type, w)
	}
}

func (m *meter) setWindow(k string, w window) {
	if m.windows == nil {
		m.windows = map[string]window{}
	}
	m.windows[k] = w
}

// spend reads the spend reader's figure for the session.
func (m *meter) spend(s spend.Spend) {
	if s.Context > 0 && !m.ctxUpdated {
		m.ctx, m.hasCtx = s.Context, true
	}
	if s.Model != "" && !m.usageModel {
		m.model = s.Model
	}
	m.spent = s.Short()
	if q := s.Quota; q != nil && !q.Unlimited {
		m.setWindow("copilot_premium", window{label: windowLabel["copilot_premium"], used: 100 - int(q.LeftPct+0.5), resets: q.Resets})
	}
}

func (m *meter) empty() bool {
	return len(m.windows) == 0 && !m.hasCtx && m.model == "" && m.cost == "" && m.spent == ""
}

// items are the footer's parts, most important first; the footer drops
// them from the end until it fits.
func (m *meter) items(pl palette, now time.Time) [][]seg {
	var out [][]seg
	keys := make([]string, 0, len(m.windows))
	for k := range m.windows {
		keys = append(keys, k)
	}
	rank := func(k string) int {
		for i, o := range windowOrder {
			if o == k {
				return i
			}
		}
		return len(windowOrder)
	}
	sort.Slice(keys, func(i, j int) bool {
		if ri, rj := rank(keys[i]), rank(keys[j]); ri != rj {
			return ri < rj
		}
		return keys[i] < keys[j]
	})
	for _, k := range keys {
		w := m.windows[k]
		if !w.resets.IsZero() && !w.resets.After(now) {
			continue // reset since it was reported: the figure is stale
		}
		it := []seg{{w.label + " ", sty{fg: pl.dim}}}
		it = append(it, meterBar(pl, w.used)...)
		it = append(it, seg{" " + strconv.Itoa(max(100-w.used, 0)) + "% left", sty{fg: pl.sub}})
		if left := untilReset(w.resets, now); left != "" {
			it = append(it, seg{" (" + left + ")", sty{fg: pl.dim}})
		}
		out = append(out, it)
	}
	if m.hasCtx {
		it := append([]seg{{"ctx ", sty{fg: pl.dim}}}, meterBar(pl, m.ctx)...)
		out = append(out, append(it, seg{" " + strconv.Itoa(m.ctx) + "%", sty{fg: pl.sub}}))
	}
	if m.model != "" {
		name := m.model
		if n := m.names[name]; n != "" {
			name = n
		}
		if m.effort != "" {
			name += " · " + m.effort
		}
		out = append(out, []seg{{name, sty{fg: pl.sub}}})
	}
	if s := m.cost; s != "" || m.spent != "" {
		if s == "" {
			s = m.spent
		}
		out = append(out, []seg{{s, sty{fg: pl.dim}}})
	}
	return out
}

// meterWidth is the cells of a footer bar.
const meterWidth = 4

// meterBar is a small bar of used percent, its fill coloured as the usage
// badge in tmux's status line: calm, warn at 80%, error at 95%.
func meterBar(pl palette, used int) []seg {
	used = min(max(used, 0), 100)
	fill := (used*meterWidth + 50) / 100
	c := pl.ok
	switch {
	case used >= 95:
		c = pl.err
	case used >= 80:
		c = pl.waiting
	}
	return []seg{{strings.Repeat("▰", fill), sty{fg: c}}, {strings.Repeat("▱", meterWidth-fill), sty{fg: pl.rule}}}
}

// untilReset is the time left to t: "3d", "4h56m", "12m"; "" when unknown.
func untilReset(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := max(t.Sub(now), 0)
	switch {
	case d >= 48*time.Hour:
		return strconv.Itoa(int(d/(24*time.Hour))) + "d"
	case d >= time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
	}
	return strconv.Itoa(int(d/time.Minute)) + "m"
}

// footer is the usage row, "" when nothing is known. Caller holds mu.
func (v *view) footer(w int) string {
	if v.meter.empty() {
		return ""
	}
	items := v.meter.items(v.pal, time.Now())
	for len(items) > 0 {
		segs := []seg{{" ", sty{}}}
		for i, it := range items {
			if i > 0 {
				segs = append(segs, seg{"  ", sty{}})
			}
			segs = append(segs, it...)
		}
		if segWidth(segs) <= w {
			return v.paintLine(segLine{segs: segs}, w)
		}
		items = items[:len(items)-1]
	}
	return ""
}

// setMeter changes the footer and draws it.
func (v *view) setMeter(fn func(*meter)) {
	v.mu.Lock()
	defer v.mu.Unlock()
	fn(&v.meter)
	v.draw()
}

// meterLoop keeps the footer current: every meterEvery and after each
// turn it re-reads the spend reader, which costs one stat while the
// session file is unchanged, and redraws so the time to reset moves on.
func (h *host) meterLoop(sid string) {
	t := time.NewTicker(meterEvery)
	defer t.Stop()
	for {
		s, ok := h.cfg.Spend(sid)
		h.con.setMeter(func(m *meter) {
			if ok {
				m.spend(s)
			}
		})
		select {
		case <-h.bg.Done():
			return
		case <-h.stopC:
			return
		case <-t.C:
		case <-h.kick:
		}
	}
}
