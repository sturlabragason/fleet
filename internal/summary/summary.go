// Package summary turns an agent's screen and last message into a few lines
// the admiral and the overview can read instead of raw output (DESIGN §6.2,
// ADR-6). It is deliberately cheap: a summary is made only when it is asked
// for, the answer is cached under the hash of the screen it was made from, a
// per-agent rate limit holds back repeat calls, and every failure is silent —
// no summary is never worse than a blocked fleet.
package summary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sturlabragason/fleet/internal/logx"
)

// MaxLines is how many lines a summary may have.
const MaxLines = 5

// DefaultMinInterval is the shortest gap between two model calls for one
// agent. Inside it a changed screen reuses the last summary.
const DefaultMinInterval = 30 * time.Second

// NoRateLimit is MinInterval for min_interval = "0": every request that is
// not a cache hit calls the model.
const NoRateLimit time.Duration = -1

// ErrNoSummary is returned when there is nothing to show and nothing cached.
var ErrNoSummary = errors.New("no summary available")

// Model is a backend that answers one prompt. Tests use a fake one; the real
// one is CLI, which shells out to an installed agent CLI (model.go).
type Model interface {
	Summarize(ctx context.Context, prompt string) (string, error)
}

// Input is what the summariser is given about one agent.
type Input struct {
	Agent       string // agent name
	Screen      string // cleaned pane text, newest last
	LastMessage string // the agent's last reply, when the profile has one
	Status      string // board status (working, waiting, ...)
	Task        string // the agent's task line, when known
}

// Result is a summary and where it came from.
type Result struct {
	Text   string    // at most MaxLines lines
	Hash   string    // hash of the input it was made from
	At     time.Time // when the model was called
	Model  string    // model that wrote it
	Cached bool      // served from the cache: no model call was made
	Stale  bool      // the screen moved on, but the rate limit held the call
}

// Summarizer caches by input hash and rate-limits per agent.
type Summarizer struct {
	// Model is the backend. Nil means every call fails open.
	Model Model
	// Dir is the cache directory (~/.fleet/peek-cache). "" disables the
	// on-disk cache; the in-memory rate limit still applies.
	Dir string
	// MinInterval is the shortest gap between model calls for one agent;
	// 0 means DefaultMinInterval, NoRateLimit means none.
	MinInterval time.Duration
	// Now is the clock (tests replace it).
	Now func() time.Time

	mu      sync.Mutex
	lastRun map[string]time.Time
}

func (s *Summarizer) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Summarizer) minInterval() time.Duration {
	switch {
	case s.MinInterval < 0:
		return 0
	case s.MinInterval > 0:
		return s.MinInterval
	}
	return DefaultMinInterval
}

// Hash identifies the agent state a summary was made from.
func Hash(in Input) string {
	h := sha256.New()
	for _, part := range []string{in.Screen, in.LastMessage, in.Status} {
		fmt.Fprintf(h, "%d\x00%s\x00", len(part), part)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Summarize returns a summary of one agent. A cached summary for the same
// hash never calls the model. Within MinInterval of the last call, a changed
// screen reuses the cached text (marked stale) rather than paying again. Any
// model failure falls back to the cached text, or to ErrNoSummary: callers
// show raw lines instead and fleet carries on.
func (s *Summarizer) Summarize(ctx context.Context, in Input) (Result, error) {
	if in.Agent == "" {
		return Result{}, fmt.Errorf("summary: agent name is required")
	}
	hash := Hash(in)
	// rec may be an attempt-only record: it then carries the time of a
	// failed call and no text.
	rec, haveRec := s.load(in.Agent)
	cached, haveCache := rec, haveRec && rec.Text != ""
	if haveCache && cached.Hash == hash {
		cached.Cached = true
		return cached, nil
	}
	if strings.TrimSpace(in.Screen) == "" && strings.TrimSpace(in.LastMessage) == "" {
		return Result{}, ErrNoSummary
	}

	now := s.now()
	s.mu.Lock()
	// The last call time comes from the cache file as well as from memory,
	// so a second `fleet peek --summary` in its own process is rate-limited
	// too, not just repeat calls inside one long-lived MCP server.
	last := s.lastRun[in.Agent]
	if haveRec && rec.At.After(last) {
		last = rec.At
	}
	if !last.IsZero() && now.Sub(last) < s.minInterval() {
		s.mu.Unlock()
		return s.failOpen(cached, haveCache, ErrNoSummary)
	}
	if s.lastRun == nil {
		s.lastRun = map[string]time.Time{}
	}
	s.lastRun[in.Agent] = now
	s.mu.Unlock()

	model := s.model()
	if model == nil {
		return s.failed(in.Agent, now, cached, haveCache, fmt.Errorf("summary: no model configured"))
	}
	text, err := model.Summarize(ctx, Prompt(in))
	if err != nil {
		logx.For("summary").Warn("model call failed", "agent", in.Agent, "err", err)
		return s.failed(in.Agent, now, cached, haveCache, err)
	}
	if text = Clean(text); text == "" {
		return s.failed(in.Agent, now, cached, haveCache, ErrNoSummary)
	}
	r := Result{Text: text, Hash: hash, At: now, Model: modelName(model)}
	s.store(in.Agent, r)
	return r, nil
}

func (s *Summarizer) failOpen(cached Result, haveCache bool, err error) (Result, error) {
	if haveCache && cached.Text != "" {
		cached.Cached, cached.Stale = true, true
		return cached, nil
	}
	return Result{}, err
}

// failed records the attempt before failing open, so a broken or slow model
// is not retried at full price by the next `fleet peek --summary` process.
func (s *Summarizer) failed(agent string, now time.Time, cached Result, haveCache bool, err error) (Result, error) {
	attempt := cached
	attempt.At = now
	if !haveCache {
		attempt.Hash, attempt.Text = "-", ""
	}
	s.store(agent, attempt)
	return s.failOpen(cached, haveCache, err)
}

// model reads Model under the lock, so a caller may set it while another
// goroutine summarises.
func (s *Summarizer) model() Model {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Model
}

func modelName(m Model) string {
	if n, ok := m.(interface{ Name() string }); ok {
		return n.Name()
	}
	return ""
}

// Clean trims a model reply to at most MaxLines non-empty lines.
func Clean(s string) string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, line)
		if len(out) == MaxLines {
			break
		}
	}
	return strings.Join(out, "\n")
}

func (s *Summarizer) path(agent string) string {
	if s.Dir == "" || agent == "" || strings.ContainsAny(agent, `/\`) {
		return ""
	}
	return filepath.Join(s.Dir, agent+".txt")
}

// load reads the cached summary of one agent. The file is
// "hash: X", "at: RFC3339", "model: M", a blank line, then the text.
func (s *Summarizer) load(agent string) (Result, bool) {
	p := s.path(agent)
	if p == "" {
		return Result{}, false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return Result{}, false
	}
	head, text, ok := strings.Cut(string(b), "\n\n")
	if !ok {
		return Result{}, false
	}
	r := Result{Text: strings.TrimRight(text, "\n")}
	for _, line := range strings.Split(head, "\n") {
		k, v, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		switch k {
		case "hash":
			r.Hash = v
		case "model":
			r.Model = v
		case "at":
			r.At, _ = time.Parse(time.RFC3339, v)
		}
	}
	// An attempt-only record (hash "-") carries the time of a failed call so
	// the rate limit survives it, but it holds no summary.
	return r, r.Hash != ""
}

func (s *Summarizer) store(agent string, r Result) {
	p := s.path(agent)
	if p == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		logx.For("summary").Warn("cache dir", "err", err)
		return
	}
	body := fmt.Sprintf("hash: %s\nat: %s\nmodel: %s\n\n%s\n", r.Hash, r.At.Format(time.RFC3339), r.Model, r.Text)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		logx.For("summary").Warn("cache write", "agent", agent, "err", err)
	}
}
