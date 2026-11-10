package tmux

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// WindowKeys are the window fields a Snapshot carries: the main window's
// size and layout and every window option the wall reads on its cached
// apply, plus the target and pins the overview shows (ADR-98).
var WindowKeys = []string{
	"window_width", "window_height", "window_layout",
	"@fleet_wall", "@fleet_sup_side", "@fleet_chips", "@fleet_target", "@fleet_next_out",
	"@fleet_layout", "@fleet_emphasis", "@fleet_emphasis_pane", "@fleet_emphasis_at",
	"@fleet_sup_rows", "@fleet_sup_seen",
}

var paneFields = strings.Count(paneFmt, "\t") + 1

func snapFmt() string {
	var b strings.Builder
	b.WriteString(paneFmt)
	for _, k := range WindowKeys {
		b.WriteString("\t#{" + k + "}")
	}
	// Run trims its output: an end marker keeps the last row's empty
	// trailing fields.
	b.WriteString("\t.")
	return b.String()
}

// Snapshot is every pane of the server with its @fleet_* options, and the
// WindowKeys of every window that has a pane, read with one list-panes -a.
// The watcher takes one per refresh and shares it in a file, so the
// overview reads tmux state without running tmux itself (ADR-98).
type Snapshot struct {
	At      time.Time                    `json:"at"`
	Panes   []PaneInfo                   `json:"panes"`
	Windows map[string]map[string]string `json:"windows"` // window id -> key -> value
}

// Snapshot reads the whole server in one exec.
func (c *Client) Snapshot() (*Snapshot, error) {
	at := time.Now()
	out, err := c.Run("list-panes", "-a", "-F", snapFmt())
	if err != nil {
		return nil, err
	}
	s := &Snapshot{At: at, Windows: map[string]map[string]string{}}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) < paneFields+len(WindowKeys)+1 {
			continue
		}
		p, ok := parsePane(f[:paneFields])
		if !ok {
			continue
		}
		s.Panes = append(s.Panes, p)
		if _, seen := s.Windows[p.WindowID]; !seen {
			w := make(map[string]string, len(WindowKeys))
			for i, k := range WindowKeys {
				w[k] = f[paneFields+i]
			}
			s.Windows[p.WindowID] = w
		}
	}
	return s, nil
}

// PanesOf is ListPanes over the snapshot: target "" is every pane, "sess:"
// every pane of a session, "sess:name" the panes of the window called name,
// in window order.
func (s *Snapshot) PanesOf(target string) []PaneInfo {
	if target == "" {
		return s.Panes
	}
	sess, win, _ := strings.Cut(target, ":")
	var out []PaneInfo
	for _, p := range s.Panes {
		if p.Session == sess && (win == "" || p.Window == win) {
			out = append(out, p)
		}
	}
	return out
}

// Window returns the WindowKeys of the window "sess:name".
func (s *Snapshot) Window(target string) (map[string]string, bool) {
	sess, win, _ := strings.Cut(target, ":")
	for _, p := range s.Panes {
		if p.Session == sess && p.Window == win {
			w, ok := s.Windows[p.WindowID]
			return w, ok
		}
	}
	return nil, false
}

// WriteSnapshot stores s at path (atomic rename: a reader never sees half).
func WriteSnapshot(path string, s *Snapshot) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ReadSnapshot loads the snapshot at path.
func ReadSnapshot(path string) (*Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}
