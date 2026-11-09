// Package spend reads what an agent has already cost from the session files
// its tool writes anyway: Copilot `session.usage_record` events and Claude
// transcript usage. Nothing here calls a model (§7b, ADR-46).
package spend

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Spend is one agent's accumulated usage. A cached token is counted once, in
// its own field, so Total is the sum of all four.
type Spend struct {
	Calls      int     `json:"calls"`
	Input      int64   `json:"input_tokens"`
	Output     int64   `json:"output_tokens"`
	CacheRead  int64   `json:"cache_read_tokens"`
	CacheWrite int64   `json:"cache_write_tokens"`
	AIC        float64 `json:"aic,omitempty"`     // Copilot AI credits
	HasAIC     bool    `json:"has_aic,omitempty"` // the tool reports credits
	// Uncredited is the part of Total that came from a tool reporting no
	// credits. It keeps a mixed fleet's total honest: credits and tokens are
	// different units and cannot be added together.
	Uncredited int64  `json:"uncredited_tokens,omitempty"`
	Model      string `json:"model,omitempty"` // the model of the last call
	// Compactions is how often the tool summarised a full context to go on
	// (Copilot only). Each one means the turns before it re-read a nearly
	// full context, which is where most spend goes (ADR-71).
	Compactions int `json:"compactions,omitempty"`
	// Context is how full the context window was at the last main-agent
	// call or compaction, in percent (0 = unknown). Like Model it is the
	// latest value, not a sum. Only `fleet spend --json` carries it; fleet
	// info has its own context figure.
	Context int `json:"context_pct,omitempty"`
}

// Total is every token the agent was billed for.
func (s Spend) Total() int64 { return s.Input + s.Output + s.CacheRead + s.CacheWrite }

// Any reports whether anything was spent at all.
func (s Spend) Any() bool { return s.Calls > 0 || s.Total() > 0 }

// Add accumulates another spend (used for the fleet-wide total).
func (s *Spend) Add(o Spend) {
	s.Calls += o.Calls
	s.Input += o.Input
	s.Output += o.Output
	s.CacheRead += o.CacheRead
	s.CacheWrite += o.CacheWrite
	s.AIC += o.AIC
	s.Uncredited += o.Uncredited
	s.HasAIC = s.HasAIC || o.HasAIC
	s.Compactions += o.Compactions
}

// Short is the one-column form: credits where the tool reports them, tokens
// where it does not, and both when a total covers tools of each kind, then
// "⟲N" for N compactions ("" when nothing has been spent yet).
func (s Spend) Short() string {
	var out string
	switch {
	case s.HasAIC:
		out = Credits(s.AIC) + " AIC"
		if s.Uncredited > 0 {
			out += " + " + Tokens(s.Uncredited) + " tok"
		}
	case s.Any():
		out = Tokens(s.Total()) + " tok"
	default:
		return ""
	}
	if s.Compactions > 0 {
		out += fmt.Sprintf(" ⟲%d", s.Compactions)
	}
	return out
}

// Long is the `fleet info` form: the column plus what is behind it.
func (s Spend) Long() string {
	if !s.Any() {
		return ""
	}
	out := s.Short() + " ("
	if s.Calls > 0 { // a tool that keeps only a running total has no call count
		call := "calls"
		if s.Calls == 1 {
			call = "call"
		}
		out += fmt.Sprintf("%d %s, ", s.Calls, call)
	}
	out += Tokens(s.Total()) + " tokens"
	if s.CacheRead > 0 {
		out += fmt.Sprintf(", %s cached", Tokens(s.CacheRead))
	}
	if s.Compactions > 0 {
		out += fmt.Sprintf(", compacted %d×", s.Compactions)
	}
	return out + ")"
}

// Credits formats AI credits with one decimal below 100 and none above, so
// the column stays narrow.
func Credits(c float64) string {
	if c >= 100 {
		return fmt.Sprintf("%.0f", c)
	}
	return fmt.Sprintf("%.1f", c)
}

// Tokens formats a token count as 913, 2.3k, 41k or 1.2M.
func Tokens(n int64) string {
	switch {
	case n >= 10_000_000:
		return fmt.Sprintf("%dM", n/1_000_000)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 10_000:
		return fmt.Sprintf("%dk", n/1000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprintf("%d", n)
}

// A record is what one line of a session file says.
type record struct {
	sp Spend
	// id dedupes: Claude writes one transcript line per content block, so a
	// reply with text and a tool call is two lines carrying the same message
	// id and the same usage. Only the first of a run is counted.
	id string
	// snapshot marks a running total the tool keeps for itself (Claude's
	// `cost-state`). It replaces the token fields instead of adding to them.
	snapshot bool
}

// A parser reads one session-file format. tags prefilter: a line matching
// none of them is skipped unparsed, which is nearly all of them.
type parser struct {
	tags [][]byte
	line func(b []byte) (record, bool)
}

// Kind names a session-file format. Profiles map onto it by their status
// pattern set, so a copilot variant needs no code here.
type Kind string

const (
	Copilot Kind = "copilot"
	Claude  Kind = "claude"
)

func parserFor(k Kind) (parser, bool) {
	switch k {
	case Copilot:
		return parser{tags: [][]byte{[]byte(`"session.usage_record"`), []byte(`"session.compaction_complete"`)}, line: copilotLine}, true
	case Claude:
		return parser{tags: [][]byte{[]byte(`"output_tokens"`), []byte(`"cost-state"`)}, line: claudeLine}, true
	}
	return parser{}, false
}

// nanoAIU is how many nano AI units make one AI credit.
const nanoAIU = 1e9

func copilotLine(b []byte) (record, bool) {
	var ev struct {
		Type string `json:"type"`
		Data struct {
			// session.compaction_complete
			Success    bool  `json:"success"`
			TokenLimit int64 `json:"tokenLimit"`
			PostTokens int64 `json:"postCompactionTokens"`
			// session.usage_record
			Usage struct {
				Model            string `json:"model"`
				InputTokens      int64  `json:"inputTokens"`
				OutputTokens     int64  `json:"outputTokens"`
				CacheReadTokens  int64  `json:"cacheReadTokens"`
				CacheWriteTokens int64  `json:"cacheWriteTokens"`
				MaxPromptTokens  int64  `json:"maxPromptTokens"`
				InteractionType  string `json:"interactionType"`
				CopilotUsage     struct {
					TotalNanoAIU float64 `json:"totalNanoAiu"`
				} `json:"copilotUsage"`
			} `json:"usage"`
		} `json:"data"`
		ID string `json:"id"` // event id: one per call, so never a duplicate
	}
	if json.Unmarshal(b, &ev) != nil {
		return record{}, false
	}
	switch ev.Type {
	case "session.compaction_complete":
		if !ev.Data.Success {
			return record{}, false
		}
		return record{sp: Spend{Compactions: 1, Context: percent(ev.Data.PostTokens, ev.Data.TokenLimit)}}, true
	case "session.usage_record":
	default:
		return record{}, false
	}
	u := ev.Data.Usage
	// A subagent call runs in its own context, so only the agent's own
	// calls say how full its context is.
	ctx := 0
	if u.InteractionType != "conversation-subagent" {
		ctx = percent(u.InputTokens, u.MaxPromptTokens)
	}
	// inputTokens already contains the cached ones; keep each in one field.
	fresh := u.InputTokens - u.CacheReadTokens - u.CacheWriteTokens
	if fresh < 0 {
		fresh = 0
	}
	return record{sp: Spend{
		Calls: 1, Input: fresh, Output: u.OutputTokens,
		CacheRead: u.CacheReadTokens, CacheWrite: u.CacheWriteTokens,
		AIC: u.CopilotUsage.TotalNanoAIU / nanoAIU, HasAIC: true, Model: u.Model,
		Context: ctx,
	}, id: ev.ID}, true
}

// percent is n of max in whole percent, 0 when max is unknown.
func percent(n, max int64) int {
	if max <= 0 || n <= 0 {
		return 0
	}
	return int(min(n*100/max, 100))
}

func claudeLine(b []byte) (record, bool) {
	var ev struct {
		Type    string `json:"type"`
		Message struct {
			ID    string `json:"id"`
			Model string `json:"model"`
			Usage struct {
				InputTokens         int64 `json:"input_tokens"`
				OutputTokens        int64 `json:"output_tokens"`
				CacheReadTokens     int64 `json:"cache_read_input_tokens"`
				CacheCreationTokens int64 `json:"cache_creation_input_tokens"`
			} `json:"usage"`
		} `json:"message"`
		// cost-state is the running total newer Claude Code keeps per model.
		ModelUsage map[string]struct {
			InputTokens         int64 `json:"inputTokens"`
			OutputTokens        int64 `json:"outputTokens"`
			CacheReadTokens     int64 `json:"cacheReadInputTokens"`
			CacheCreationTokens int64 `json:"cacheCreationInputTokens"`
		} `json:"modelUsage"`
	}
	if json.Unmarshal(b, &ev) != nil {
		return record{}, false
	}
	switch ev.Type {
	case "cost-state":
		if len(ev.ModelUsage) == 0 {
			return record{}, false
		}
		var sp Spend
		for model, u := range ev.ModelUsage {
			sp.Input += u.InputTokens
			sp.Output += u.OutputTokens
			sp.CacheRead += u.CacheReadTokens
			sp.CacheWrite += u.CacheCreationTokens
			sp.Model = model
		}
		return record{sp: sp, snapshot: true}, true
	case "assistant":
		if ev.Message.Model == "<synthetic>" {
			return record{}, false // an API error, not a billed call
		}
		u := ev.Message.Usage
		return record{sp: Spend{
			Calls: 1, Input: u.InputTokens, Output: u.OutputTokens,
			CacheRead: u.CacheReadTokens, CacheWrite: u.CacheCreationTokens,
			Model: ev.Message.Model,
		}, id: ev.Message.ID}, true
	}
	return record{}, false
}

// result is what one pass over a buffer found.
type result struct {
	add      Spend  // records to add to the running total
	snap     Spend  // the newest running total the tool keeps itself
	hasSnap  bool   // a snapshot was seen in this buffer
	atSnap   Spend  // records of this buffer counted up to that snapshot
	lastID   string // the last record id, carried into the next read
	consumed int64  // bytes accounted for: everything up to the last newline
}

// scan accumulates the usage records in buf. lastID is the id of the last
// record counted so far, so a duplicate split across two reads is still
// skipped. A half-written last line is left for the next read.
func scan(p parser, buf []byte, lastID string) result {
	end := bytes.LastIndexByte(buf, '\n')
	if end < 0 {
		return result{lastID: lastID}
	}
	out := result{lastID: lastID, consumed: int64(end + 1)}
	for _, line := range bytes.Split(buf[:end], []byte("\n")) {
		if !anyTag(p.tags, line) {
			continue
		}
		r, ok := p.line(line)
		if !ok {
			continue
		}
		if r.snapshot {
			out.snap, out.hasSnap, out.atSnap = r.sp, true, out.add
			continue
		}
		if r.id != "" && r.id == out.lastID {
			continue // another content block of a reply already counted
		}
		if r.id != "" {
			out.lastID = r.id
		}
		model, ctx := r.sp.Model, out.add.Context
		if r.sp.Context > 0 {
			ctx = r.sp.Context
		}
		out.add.Add(r.sp)
		if model != "" {
			out.add.Model = model
		}
		out.add.Context = ctx
	}
	return out
}

func anyTag(tags [][]byte, line []byte) bool {
	for _, t := range tags {
		if bytes.Contains(line, t) {
			return true
		}
	}
	return false
}
