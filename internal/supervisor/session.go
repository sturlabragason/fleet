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
	// Dir is the dir the session started in. Claude finds a session to
	// resume by its project dir, so a resume runs there (ADR-93).
	Dir string `json:"dir,omitempty"`

	// extra keeps keys this build does not know (ADR-62).
	extra jsonx.Extra
}

// Dir is <home>/admiral. A <home>/supervisor dir from before ADR-26 is moved
// there and left as a symlink, so older binaries share the same files.
func Dir(home string) string { return DirOf(home, Main) }

// DirOf is the directory of one admiral: main keeps <home>/admiral, any
// other admiral has <home>/admirals/<name> (DESIGN §6.12).
func DirOf(home, name string) string {
	if isMain(name) {
		return legacy.MoveDir(filepath.Join(home, "admiral"), filepath.Join(home, "supervisor"))
	}
	return filepath.Join(home, "admirals", name)
}

// mkdir creates Dir(home) (and the legacy symlink) and returns it.
func mkdir(home string) (string, error) { return mkdirOf(home, Main) }

func mkdirOf(home, name string) (string, error) {
	if isMain(name) {
		return legacy.MkdirAll(filepath.Join(home, "admiral"), filepath.Join(home, "supervisor"))
	}
	d := DirOf(home, name)
	return d, os.MkdirAll(d, 0o755)
}

func sessionPath(home, name string) string { return filepath.Join(DirOf(home, name), "session.json") }

// Load reads session.json; ok is false when it is missing or broken.
func Load(home string) (Session, bool) { return LoadOf(home, Main) }

// LoadOf is Load for one admiral.
func LoadOf(home, name string) (Session, bool) {
	var s Session
	b, err := os.ReadFile(sessionPath(home, name))
	if err != nil || json.Unmarshal(b, &s) != nil {
		return Session{}, false
	}
	return s, true
}

// Save writes session.json atomically.
func Save(home string, s Session) error { return SaveOf(home, Main, s) }

// SaveOf is Save for one admiral.
func SaveOf(home, name string, s Session) error {
	if _, err := mkdirOf(home, name); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := sessionPath(home, name) + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, sessionPath(home, name))
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
