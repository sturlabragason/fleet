// Package spend reads what an agent has already cost from the session files
// its tool writes anyway: Copilot `session.usage_record` events and Claude
// transcript usage. Nothing here calls a model (§7b, ADR-46).
package spend

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/sturlabragason/fleet/internal/tools"
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
	// (Copilot's session.compaction_complete, Claude's compact_boundary). Each
	// one means the turns before it re-read a nearly full context, which is
	// where most spend goes (ADR-71). Claude's idle compaction, run on an
	// agent left waiting before its prompt cache expires, does not count
	// (ADR-79).
	Compactions int `json:"compactions,omitempty"`
	// Context is how full the context window was at the last main-agent
	// call or compaction, in percent (0 = unknown). Like Model it is the
	// latest value, not a sum. Only `fleet spend --json` carries it; fleet
	// info has its own context figure.
	Context int `json:"context_pct,omitempty"`
	// Quota is the Copilot account's premium allowance at the last call
	// that reported it (nil: never reported). Like Model it is the latest
	// value, not a sum.
	Quota *Quota `json:"quota,omitempty"`
}

// Quota is a Copilot premium-request allowance: unlimited, or the percent
// left until it resets (monthly).
type Quota struct {
	Unlimited bool      `json:"unlimited,omitempty"`
	LeftPct   float64   `json:"left_pct"`
	Resets    time.Time `json:"resets,omitzero"`
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
	Copilot Kind = tools.FamilyCopilot
	Claude  Kind = tools.FamilyClaude
	// Pi is a Pi session file (~/.pi/agent/sessions/*/<ts>_<id>.jsonl, ADR-148).
	Pi Kind = tools.FamilyPi
	// ACP is fleet's own transcript of an ACP agent (~/.fleet/acp/<name>.jsonl),
	// for a tool that keeps no usage log fleet can read (agy): one usage
	// entry per turn, from the session/prompt response.
	ACP Kind = "acp"
)

func parserFor(k Kind) (parser, bool) {
	switch k {
	case Copilot:
		return parser{tags: [][]byte{[]byte(`"session.usage_record"`), []byte(`"session.compaction_complete"`), quotaKey}, line: copilotLine}, true
	case Claude:
		return parser{tags: [][]byte{[]byte(`"output_tokens"`), []byte(`"cost-state"`), []byte(`"compact_boundary"`)}, line: claudeLine}, true
	case ACP:
		return parser{tags: [][]byte{[]byte(`"kind":"usage"`)}, line: acpLine}, true
	case Pi:
		return parser{tags: [][]byte{[]byte(`"usage":{`)}, line: piLine}, true
	}
	return parser{}, false
}

// nanoAIU is how many nano AI units make one AI credit.
const nanoAIU = 1e9

// quotaKey and callSuccess find Copilot's quota snapshot: a
// model.model_call_success event, written now and then, whose line also
// holds the whole request, so only the snapshot object is decoded.
var (
	quotaKey    = []byte(`"quotaSnapshots":`)
	callSuccess = []byte(`{"type":"model.model_call_success"`)
)

func copilotQuota(b []byte) (record, bool) {
	i := bytes.Index(b, quotaKey)
	if i < 0 || !bytes.HasPrefix(b, callSuccess) {
		return record{}, false
	}
	var snaps struct {
		Premium *struct {
			Unlimited bool      `json:"isUnlimitedEntitlement"`
			Left      float64   `json:"remainingPercentage"`
			Reset     time.Time `json:"resetDate"`
		} `json:"premium_interactions"`
	}
	if json.NewDecoder(bytes.NewReader(b[i+len(quotaKey):])).Decode(&snaps) != nil || snaps.Premium == nil {
		return record{}, false
	}
	p := snaps.Premium
	return record{sp: Spend{Quota: &Quota{Unlimited: p.Unlimited, LeftPct: p.Left, Resets: p.Reset}}}, true
}

func copilotLine(b []byte) (record, bool) {
	if bytes.HasPrefix(b, callSuccess) {
		return copilotQuota(b)
	}
	var ev struct {
		Type string `json:"type"`
		Data struct {
			// session.compaction_complete
			Success    bool  `json:"success"`
			TokenLimit int64 `json:"tokenLimit"`
			PostTokens int64 `json:"postCompactionTokens"`
			// The summarising call, billed but never a usage_record (ADR-79).
			Used *struct {
				InputTokens      int64 `json:"inputTokens"`
				OutputTokens     int64 `json:"outputTokens"`
				CacheReadTokens  int64 `json:"cacheReadTokens"`
				CacheWriteTokens int64 `json:"cacheWriteTokens"`
				CopilotUsage     struct {
					TotalNanoAIU float64 `json:"totalNanoAiu"`
				} `json:"copilotUsage"`
			} `json:"compactionTokensUsed"`
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
		var sp Spend
		if u := ev.Data.Used; u != nil {
			// A failed attempt that made the call still cost it.
			sp = Spend{Input: max(u.InputTokens-u.CacheReadTokens-u.CacheWriteTokens, 0),
				Output: u.OutputTokens, CacheRead: u.CacheReadTokens, CacheWrite: u.CacheWriteTokens,
				AIC: u.CopilotUsage.TotalNanoAIU / nanoAIU, HasAIC: true}
		}
		if ev.Data.Success {
			sp.Compactions, sp.Context = 1, percent(ev.Data.PostTokens, ev.Data.TokenLimit)
		}
		if !sp.Any() && sp.Compactions == 0 {
			return record{}, false
		}
		return record{sp: sp, id: ev.ID}, true
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
	fresh := max(u.InputTokens-u.CacheReadTokens-u.CacheWriteTokens, 0)
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
		Type     string `json:"type"`
		Subtype  string `json:"subtype"`
		UUID     string `json:"uuid"`
		Metadata struct {
			Trigger string `json:"trigger"`
		} `json:"compactMetadata"`
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
	case "system":
		if ev.Subtype != "compact_boundary" || ev.Metadata.Trigger == "idle" {
			return record{}, false
		}
		return record{sp: Spend{Compactions: 1}, id: ev.UUID}, true
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

// acpLine reads a usage entry of an ACP transcript. A record is a turn, not
// a model call: the protocol reports usage per turn.
func acpLine(b []byte) (record, bool) {
	var e struct {
		Kind  string `json:"kind"`
		Usage *struct {
			InputTokens       int64 `json:"inputTokens"`
			OutputTokens      int64 `json:"outputTokens"`
			CachedReadTokens  int64 `json:"cachedReadTokens"`
			CachedWriteTokens int64 `json:"cachedWriteTokens"`
			TotalTokens       int64 `json:"totalTokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(b, &e) != nil || e.Kind != "usage" || e.Usage == nil {
		return record{}, false
	}
	u := e.Usage
	in := u.InputTokens
	// agy counts cached tokens apart from input; an agent whose total shows
	// they are inside input has them taken out, so each is counted once.
	if cached := u.CachedReadTokens + u.CachedWriteTokens; cached > 0 && u.TotalTokens == u.InputTokens+u.OutputTokens && in >= cached {
		in -= cached
	}
	return record{sp: Spend{Calls: 1, Input: in, Output: u.OutputTokens,
		CacheRead: u.CachedReadTokens, CacheWrite: u.CachedWriteTokens}}, true
}

// piLine reads a Pi session entry that carries usage: an assistant message
// (one call), a usage entry (cache warming and the like), or the summary
// call of a compaction or branch summary. Pi keeps each token kind apart,
// so nothing is taken out of input. Its cost in dollars is not kept: Spend
// has no unit for it.
func piLine(b []byte) (record, bool) {
	type usage struct {
		Input      int64 `json:"input"`
		Output     int64 `json:"output"`
		CacheRead  int64 `json:"cacheRead"`
		CacheWrite int64 `json:"cacheWrite"`
	}
	var e struct {
		Type    string `json:"type"`
		ID      string `json:"id"`
		Model   string `json:"model"`
		Usage   *usage `json:"usage"`
		Message struct {
			Role  string `json:"role"`
			Model string `json:"model"`
			Usage *usage `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(b, &e) != nil {
		return record{}, false
	}
	u, sp := e.Usage, Spend{Model: e.Model}
	switch e.Type {
	case "message":
		if e.Message.Role != "assistant" {
			return record{}, false
		}
		u, sp = e.Message.Usage, Spend{Calls: 1, Model: e.Message.Model}
	case "compaction":
		sp.Compactions = 1
	case "usage", "branch_summary":
	default:
		return record{}, false
	}
	if u != nil {
		sp.Input, sp.Output, sp.CacheRead, sp.CacheWrite = u.Input, u.Output, u.CacheRead, u.CacheWrite
	}
	if !sp.Any() && sp.Compactions == 0 {
		return record{}, false
	}
	return record{sp: sp, id: e.ID}, true
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
	for line := range bytes.SplitSeq(buf[:end], []byte("\n")) {
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
		model, ctx, quota := r.sp.Model, out.add.Context, out.add.Quota
		if r.sp.Context > 0 {
			ctx = r.sp.Context
		}
		if r.sp.Quota != nil {
			quota = r.sp.Quota
		}
		out.add.Add(r.sp)
		if model != "" {
			out.add.Model = model
		}
		out.add.Context, out.add.Quota = ctx, quota
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
