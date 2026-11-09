package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/mail"
)

// fleet_wait (ADR-72): block inside fleet until a new report or message
// arrives, instead of sleep-polling with full-context model turns.

const (
	// WaitDefault is how long fleet_wait and `fleet wait` block by default.
	WaitDefault = 300 * time.Second
	// WaitMax caps a wait; the server answers nothing else while it blocks.
	WaitMax = 600 * time.Second
	// WaitNoProgress caps fleet_wait for a client that sent no progress
	// token: Copilot CLI cancels an MCP call after 180 s unless progress
	// notifications keep resetting that timer (measured, ADR-72).
	WaitNoProgress = 170 * time.Second
)

var (
	// waitTick is how often a wait stats the inbox file (tests shorten it).
	waitTick = time.Second
	// progressEvery is how often a wait reports progress to the client.
	progressEvery = 30 * time.Second
)

var waitToolDef = tool{"fleet_wait", "Block until a new report or message arrives for you, or timeout_s passes (default 300, max 600; 170 if your client sends no progress token). Use it instead of sleep or calling fleet_reports again and again. Returns only headlines (who, state, first line) and marks nothing read: fleet_reports, fleet_squad_reports or fleet_inbox give the full text. Call it on its own: other fleet tool calls queue behind it.", obj(nil, map[string]any{
	"agents":    strList("only wake for these agents (default: anyone)"),
	"timeout_s": map[string]any{"type": "integer", "description": "seconds to wait (default 300, max 600)"}})}

func init() {
	toolDefs = append(toolDefs, waitToolDef)
	agentToolDefs = append(agentToolDefs, waitToolDef)
	identityBound["fleet_wait"] = true
}

// WaitOpts says what Wait watches.
type WaitOpts struct {
	Box     string        // mailbox to watch
	From    []string      // only messages from these senders; empty = anyone
	Timeout time.Duration // WaitDefault when zero, capped at WaitMax
	// Progress, when set, is called every progressEvery with the time
	// waited so far.
	Progress func(waited time.Duration)
}

// WaitTimeout turns a requested wait into the one used: the default for
// zero, WaitMax at most; negative is an error.
func WaitTimeout(d time.Duration) (time.Duration, error) {
	switch {
	case d < 0:
		return 0, fmt.Errorf("timeout must not be negative")
	case d == 0:
		return WaitDefault, nil
	case d > WaitMax:
		return WaitMax, nil
	}
	return d, nil
}

// Wait blocks until o.Box holds a message it did not hold when Wait began
// (from one of o.From, if set), ctx ends or the timeout passes, and returns
// the new messages, oldest first: none on timeout. It stats the inbox file
// every waitTick and reads it only when the file changed. Nothing is marked
// read.
func Wait(ctx context.Context, st *mail.Store, o WaitOpts) []mail.Msg {
	d, err := WaitTimeout(o.Timeout)
	if err != nil {
		d = WaitDefault
	}
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	from := map[string]bool{}
	for _, n := range o.From {
		from[n] = true
	}
	// The stamp is taken before the baseline read: a message landing in
	// between is in the baseline and the changed stamp only costs a re-read.
	prev := st.StampOf(o.Box)
	seen := map[string]bool{}
	for _, m := range st.Box(o.Box) {
		seen[m.ID] = true
	}
	news := func() []mail.Msg {
		cur := st.StampOf(o.Box)
		if cur.Size == prev.Size && cur.Mod.Equal(prev.Mod) {
			return nil
		}
		prev = cur
		var out []mail.Msg
		for _, m := range st.Box(o.Box) {
			if !seen[m.ID] && (len(from) == 0 || from[m.From]) {
				out = append(out, m)
			}
		}
		return out
	}
	t := time.NewTicker(waitTick)
	defer t.Stop()
	start, lastProgress := time.Now(), time.Now()
	for {
		select {
		case <-ctx.Done():
			return news() // one last look: a message from the final tick counts
		case now := <-t.C:
			if o.Progress != nil && now.Sub(lastProgress) >= progressEvery {
				lastProgress = now
				o.Progress(now.Sub(start))
			}
		}
		if out := news(); len(out) > 0 {
			return out
		}
	}
}

// waitHead caps each headline in a wait result.
const waitHead = 120

// RenderWait is the short result of a wait: one headline per new message
// and where to read the full text, or the timeout line.
func RenderWait(ms []mail.Msg, waited time.Duration, readWith string) string {
	waited = waited.Round(time.Second)
	if len(ms) == 0 {
		return fmt.Sprintf("timeout after %s, nothing new", waited)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d new after %s (headlines only, nothing marked read; %s):\n", len(ms), waited, readWith)
	for _, m := range ms {
		head, _, _ := strings.Cut(m.Text, "\n")
		if m.Kind != mail.KindReport { // a report's text already starts with its state
			kind := m.Kind
			if kind == mail.KindMsg {
				kind = "message"
			}
			head = kind + ": " + head
		}
		fmt.Fprintf(&b, "- %s %s\n", m.From, ansi.Truncate(head, waitHead, "…"))
	}
	return strings.TrimRight(b.String(), "\n")
}

// wait is fleet_wait on box for the admiral, a leader or an agent.
func (s *Server) wait(box, readWith string, a args) (string, error) {
	d, err := WaitTimeout(time.Duration(a.TimeoutS) * time.Second)
	if err != nil {
		return "", err
	}
	if err := CheckSenders(s.F, a.Agents); err != nil {
		return "", err
	}
	o := WaitOpts{Box: box, From: a.Agents, Timeout: d}
	if p := s.progress; p != nil {
		n := 0
		o.Progress = func(waited time.Duration) {
			n++
			p(n, fmt.Sprintf("waited %s, nothing new yet", waited.Round(time.Second)))
		}
	} else if d > WaitNoProgress {
		o.Timeout = WaitNoProgress
	}
	start := time.Now()
	ms := Wait(context.Background(), s.F.Mail(), o)
	return RenderWait(ms, time.Since(start), readWith), nil
}

// CheckSenders refuses an agents filter naming an agent that is not on the
// board: a typo would otherwise wait out the whole timeout. Admiral
// mailboxes pass, they are not on the board.
func CheckSenders(f *fleet.Fleet, names []string) error {
	b := f.Store.Load()
	for _, n := range names {
		if _, ok := b.Agents[n]; !ok && !mail.IsAdmiralBox(n) {
			return fmt.Errorf("no agent %q", n)
		}
	}
	return nil
}

// Where each caller reads the full text of what fleet_wait announced.
const (
	waitReadAdmiral = "fleet_reports reads reports, fleet_inbox other messages"
	waitReadLeader  = "fleet_squad_reports shows reports, fleet_inbox reads them"
	waitReadAgent   = "fleet_inbox reads them"
)

// agentWait is fleet_wait on an agent's own mailbox.
func (s *Server) agentWait(me string, a args) (string, error) {
	hint := waitReadAgent
	if s.F.IsLeader(me) {
		hint = waitReadLeader
	}
	return s.wait(me, hint, a)
}
