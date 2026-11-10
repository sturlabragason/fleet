package supervisor

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sturlabragason/fleet/internal/theme"
)

// SessionIDFromCommand finds `--session-id <id>` in a start command (fallback
// for admirals started before session.json existed).
func SessionIDFromCommand(cmd string) string {
	f := strings.Fields(strings.NewReplacer("'", " ", `"`, " ").Replace(cmd))
	for i, a := range f {
		if a == "--session-id" && i+1 < len(f) {
			return f[i+1]
		}
		if v, ok := strings.CutPrefix(a, "--session-id="); ok {
			return v
		}
	}
	return ""
}

// CopilotHome is $COPILOT_HOME or ~/.copilot.
func CopilotHome() string {
	if h := os.Getenv("COPILOT_HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".copilot")
}

// tailChunk is the first read window and maxTail the widest one; beyond
// that the fill is unknown. Tests shrink both.
var (
	tailChunk int64 = 256 << 10
	maxTail   int64 = 8 << 20
)

// Fill returns input tokens and the prompt limit from the last
// session.usage_record in the session's events.jsonl. Only the end of the
// file is read; the window widens until a record is found or the file ends.
func Fill(copilotHome, sessionID string) (used, max int, ok bool) {
	if sessionID == "" {
		return 0, 0, false
	}
	f, err := os.Open(filepath.Join(copilotHome, "session-state", sessionID, "events.jsonl"))
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, 0, false
	}
	size := st.Size()
	for n := tailChunk; ; n *= 4 {
		if n > maxTail {
			n = maxTail
		}
		off := size - n
		if off < 0 {
			off = 0
		}
		buf := make([]byte, size-off)
		if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
			return 0, 0, false
		}
		if off > 0 { // drop the partial first line
			if i := bytes.IndexByte(buf, '\n'); i >= 0 {
				buf = buf[i+1:]
			}
		}
		if u, m, found := lastUsage(buf); found {
			return u, m, true
		}
		if off == 0 || n == maxTail {
			return 0, 0, false
		}
	}
}

var usageTag = []byte(`"session.usage_record"`)

func lastUsage(buf []byte) (int, int, bool) {
	lines := bytes.Split(buf, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		if !bytes.Contains(lines[i], usageTag) {
			continue
		}
		var ev struct {
			Type string `json:"type"`
			Data struct {
				Usage struct {
					InputTokens     int `json:"inputTokens"`
					MaxPromptTokens int `json:"maxPromptTokens"`
				} `json:"usage"`
			} `json:"data"`
		}
		if json.Unmarshal(lines[i], &ev) != nil || ev.Type != "session.usage_record" {
			continue
		}
		if u := ev.Data.Usage; u.MaxPromptTokens > 0 {
			return u.InputTokens, u.MaxPromptTokens, true
		}
	}
	return 0, 0, false
}

// Pct is used/max as a whole percent.
func Pct(used, max int) int {
	if max <= 0 {
		return 0
	}
	return used * 100 / max
}

// Threshold is the handoff percent: $FLEET_ADMIRAL_HANDOFF_PCT, else the
// deprecated $FLEET_SUP_HANDOFF_PCT (ADR-26), else 70.
func Threshold() int {
	env := os.Getenv("FLEET_ADMIRAL_HANDOFF_PCT")
	if env == "" {
		env = os.Getenv("FLEET_SUP_HANDOFF_PCT")
	}
	if v, err := strconv.Atoi(env); err == nil && v > 0 && v <= 100 {
		return v
	}
	return 70
}

// HandoffTokens is the context size at which a handoff is due whatever the
// percent: $FLEET_ADMIRAL_HANDOFF_TOKENS, else 80000; 0 turns it off. Past
// about 80k every wake re-reads a long, mostly stale context, and on a big
// window the percent threshold comes far too late (ADR-93).
func HandoffTokens() int {
	if v, err := strconv.Atoi(os.Getenv("FLEET_ADMIRAL_HANDOFF_TOKENS")); err == nil && v >= 0 {
		return v
	}
	return 80000
}

// HandoffDue: the admiral should hand off, at Threshold percent or at
// HandoffTokens, whichever comes first.
func HandoffDue(used, max int) bool {
	if n := HandoffTokens(); n > 0 && used >= n {
		return true
	}
	return max > 0 && Pct(used, max) >= Threshold()
}

// BandFill is Band for a fill, red when HandoffDue, so a red border and the
// MCP warning always coincide.
func BandFill(used, max int) string {
	if HandoffDue(used, max) {
		return theme.Error
	}
	return Band(Pct(used, max))
}

// Band is the meter colour: green below 40, yellow below the threshold, red at
// or above it.
func Band(pct int) string {
	switch {
	case pct >= Threshold():
		return theme.Error
	case pct >= 40:
		return theme.Waiting
	}
	return theme.Working
}
