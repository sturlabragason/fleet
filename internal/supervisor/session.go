// Package supervisor keeps the admiral's memory on disk (DESIGN.md §6.5):
// the current session record, notes in decisions.log, handoffs, and the
// context meter read from Copilot's own usage records. The package keeps its
// pre-ADR-26 name (the admiral was called the supervisor) so in-flight
// branches that import it keep compiling.
package supervisor

import (
	"encoding/json"
	"github.com/sturlabragason/fleet/internal/jsonx"
	"os"
	"path/filepath"
	"time"

	"github.com/sturlabragason/fleet/internal/legacy"
)

// BootPrompt starts a fresh admiral after a reset.
const BootPrompt = "You are taking over as fleet admiral. Call fleet_brief, tell the user in 1–3 lines what needs them, then wait."

// Session is the current admiral, stored in <home>/admiral/session.json.
type Session struct {
	SessionID  string    `json:"session_id"`
	Profile    string    `json:"profile"`
	PaneID     string    `json:"pane_id"`
	Started    time.Time `json:"started"`
	Generation int       `json:"generation"`
	Previous   string    `json:"previous,omitempty"`

	// extra keeps keys this build does not know (ADR-62).
	extra jsonx.Extra
}

// Dir is <home>/admiral. A <home>/supervisor dir from before ADR-26 is moved
// there and left as a symlink, so older binaries share the same files.
func Dir(home string) string {
	return legacy.MoveDir(filepath.Join(home, "admiral"), filepath.Join(home, "supervisor"))
}

// mkdir creates Dir(home) (and the legacy symlink) and returns it.
func mkdir(home string) (string, error) {
	return legacy.MkdirAll(filepath.Join(home, "admiral"), filepath.Join(home, "supervisor"))
}

func sessionPath(home string) string { return filepath.Join(Dir(home), "session.json") }

// Load reads session.json; ok is false when it is missing or broken.
func Load(home string) (Session, bool) {
	var s Session
	b, err := os.ReadFile(sessionPath(home))
	if err != nil || json.Unmarshal(b, &s) != nil {
		return Session{}, false
	}
	return s, true
}

// Save writes session.json atomically.
func Save(home string, s Session) error {
	if _, err := mkdir(home); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := sessionPath(home) + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, sessionPath(home))
}

// sessionJSON is Session without its methods, for jsonx.
type sessionJSON Session

func (x *Session) UnmarshalJSON(b []byte) error {
	var v sessionJSON
	e, err := jsonx.Unmarshal(b, &v)
	*x, x.extra = Session(v), e
	return err
}

func (x Session) MarshalJSON() ([]byte, error) { return jsonx.Marshal(sessionJSON(x), x.extra) }
