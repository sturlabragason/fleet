package recall

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/companion"
	"github.com/sturlabragason/fleet/internal/sessions"
)

// The cass adapter (ADR-119): everything fleet knows about cass is in this
// file. cass (github.com/Dicklesworthstone/coding_agent_session_search) is
// an optional local binary that indexes the session files of Copilot CLI,
// Claude Code and other tools; fleet only runs `cass search --json`,
// `cass status --json` and, on request, `cass index`. Nothing leaves the
// machine.

// CassHint says how to get session search when cass is missing.
const CassHint = "Session search is off: cass is not installed. Install it (a local binary, the index stays on this machine) from https://github.com/Dicklesworthstone/coding_agent_session_search/releases, put `cass` on PATH, then run `cass index` once."

// ErrNoCass is returned when no cass binary is found.
var ErrNoCass = errors.New("cass is not installed")

// cassTimeout bounds one cass search or status call; both take well
// under a second on an index of a few thousand sessions.
const cassTimeout = 20 * time.Second

// refreshTimeout bounds `fleet recall --refresh`'s incremental index.
const refreshTimeout = 5 * time.Minute

// cass is the companion Spec (ADR-126): $FLEET_CASS, then
// [integrations.cass] path, then PATH; "off" turns session search off.
var cass = companion.Register(companion.Spec{
	Name:    "cass",
	Env:     []string{"FLEET_CASS"},
	Version: []string{"--version"},
	Feature: "session search (fleet recall, fleet day)",
	Hint:    "install cass from https://github.com/Dicklesworthstone/coding_agent_session_search/releases, then run `cass index` once",
})

func runCass(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	t, err := companion.Find(cass)
	if err != nil {
		return nil, ErrNoCass
	}
	return t.Run(ctx, companion.Opts{Timeout: timeout}, args...)
}

type cassHit struct {
	Title      string          `json:"title"`
	Snippet    string          `json:"snippet"`
	Content    string          `json:"content"`
	SourcePath string          `json:"source_path"`
	Agent      string          `json:"agent"`
	Workspace  string          `json:"workspace"`
	CreatedAt  json.RawMessage `json:"created_at"`
}

// cassSearch runs `cass search` and returns one hit per session, best
// first, at most limit of them.
func cassSearch(ctx context.Context, query string, days, limit int) ([]Hit, error) {
	args := []string{"search", query, "--json", "--limit", strconv.Itoa(limit * 4), "--max-content-length", "300"}
	if days > 0 {
		args = append(args, "--days", strconv.Itoa(days))
	}
	out, err := runCass(ctx, cassTimeout, args...)
	if err != nil {
		return nil, err
	}
	var res struct {
		Hits []cassHit `json:"hits"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, fmt.Errorf("cass search: unreadable output: %w", err)
	}
	seen := map[string]bool{}
	var hits []Hit
	for _, h := range res.Hits {
		if seen[h.SourcePath] {
			continue
		}
		seen[h.SourcePath] = true
		text := h.Snippet
		if strings.TrimSpace(text) == "" {
			text = h.Content
		}
		ws := h.Workspace
		if ws == "" {
			ws = copilotCwd(h.SourcePath)
		}
		hits = append(hits, Hit{
			Kind:  "session",
			At:    parseCassTime(h.CreatedAt),
			Tool:  h.Agent,
			Dir:   ws,
			Title: firstLine(h.Title),
			Text:  oneLine(text),
			Path:  h.SourcePath,
		})
		if len(hits) == limit {
			break
		}
	}
	return hits, nil
}

// IndexAge is how old the cass index is; ok is false when cass is missing
// or does not say.
func IndexAge(ctx context.Context) (time.Duration, bool) {
	out, err := runCass(ctx, cassTimeout, "status", "--json")
	if err != nil {
		return 0, false
	}
	var st struct {
		Index struct {
			Age *float64 `json:"age_seconds"`
		} `json:"index"`
	}
	if json.Unmarshal(out, &st) != nil || st.Index.Age == nil {
		return 0, false
	}
	return time.Duration(*st.Index.Age) * time.Second, true
}

// Refresh runs an incremental `cass index` at low CPU and IO priority.
func Refresh(ctx context.Context) error {
	t, err := companion.Find(cass)
	if err != nil {
		return ErrNoCass
	}
	_, err = t.Run(ctx, companion.Opts{Timeout: refreshTimeout, Low: true}, "index", "--json", "--no-progress-events")
	return err
}

// parseCassTime reads created_at: epoch milliseconds or an RFC 3339 string.
func parseCassTime(raw json.RawMessage) time.Time {
	var ms int64
	if json.Unmarshal(raw, &ms) == nil && ms > 0 {
		return time.UnixMilli(ms)
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t.Local()
		}
	}
	return time.Time{}
}

// copilotCwd reads the cwd of a Copilot CLI session from the
// workspace.yaml beside its events.jsonl; cass 0.11 leaves the workspace
// of Copilot CLI search hits empty.
func copilotCwd(sourcePath string) string {
	if filepath.Base(sourcePath) != "events.jsonl" {
		return ""
	}
	return sessions.CopilotCwd(filepath.Dir(sourcePath))
}

// sessionID is the tool's session id in a session file path: the
// directory name for Copilot CLI, the file name for Claude Code.
func sessionID(path string) string {
	if filepath.Base(path) == "events.jsonl" {
		return filepath.Base(filepath.Dir(path))
	}
	return strings.TrimSuffix(filepath.Base(path), ".jsonl")
}
