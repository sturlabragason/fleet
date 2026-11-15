package tmux

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
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

// PaneKeys are the pane options a Snapshot carries for each pane that has
// one set, and GlobalKeys the global options it carries: what the watcher's
// badge and admiral-border sync compare against, so an idle pass reads them
// without a show-options per key (ADR-138).
var (
	PaneKeys   = []string{"@fleet_admiral_name", "@fleet_admiral_color", "@fleet_admiral_unread", "@fleet_admiral_ext"}
	GlobalKeys = []string{"@fleet_reports", "@fleet_reports_by", "@fleet_multi"}
)

var paneFields = strings.Count(paneFmt, "\t") + 1

// rowEnd ends each snapshot row: a newline inside an option value then stays
// in its field instead of splitting the row.
const rowEnd = "\t.\n"

func snapFmt() string {
	var b strings.Builder
	b.WriteString(paneFmt)
	for _, k := range slices.Concat(WindowKeys, PaneKeys, GlobalKeys) {
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
	At       time.Time                    `json:"at"`
	Panes    []PaneInfo                   `json:"panes"`
	Windows  map[string]map[string]string `json:"windows"`             // window id -> key -> value
	PaneOpts map[string]map[string]string `json:"pane_opts,omitempty"` // pane id -> PaneKeys that are set
	Globals  map[string]string            `json:"globals,omitempty"`   // GlobalKeys
}

// Snapshot reads the whole server in one exec.
func (c *Client) Snapshot() (*Snapshot, error) {
	at := time.Now()
	out, err := c.Run("list-panes", "-a", "-F", snapFmt())
	if err != nil {
		return nil, err
	}
	return parseSnapshot(at, out), nil
}

func parseSnapshot(at time.Time, out string) *Snapshot {
	s := &Snapshot{At: at, Windows: map[string]map[string]string{}, PaneOpts: map[string]map[string]string{}}
	seen := map[string]bool{}
	nw, np := paneFields+len(WindowKeys), paneFields+len(WindowKeys)+len(PaneKeys)
	for line := range strings.SplitSeq(out+"\n", rowEnd) {
		f := strings.Split(line, "\t")
		if len(f) < np+len(GlobalKeys) {
			continue
		}
		p, ok := parsePane(f[:paneFields])
		if !ok || seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		s.Panes = append(s.Panes, p)
		if s.Globals == nil {
			s.Globals = make(map[string]string, len(GlobalKeys))
			for i, k := range GlobalKeys {
				s.Globals[k] = f[np+i]
			}
		}
		for i, k := range PaneKeys {
			if v := f[nw+i]; v != "" {
				if s.PaneOpts[p.ID] == nil {
					s.PaneOpts[p.ID] = map[string]string{}
				}
				s.PaneOpts[p.ID][k] = v
			}
		}
		if _, seen := s.Windows[p.WindowID]; !seen {
			w := make(map[string]string, len(WindowKeys))
			for i, k := range WindowKeys {
				w[k] = f[paneFields+i]
			}
			s.Windows[p.WindowID] = w
		}
	}
	return s
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

// PaneOption is a PaneKeys option of a listed pane as the snapshot read it;
// ok is false when s is nil or does not carry it, and the caller asks tmux.
func (s *Snapshot) PaneOption(pane, key string) (string, bool) {
	if s == nil || !slices.Contains(PaneKeys, key) || !slices.ContainsFunc(s.Panes, func(p PaneInfo) bool { return p.ID == pane }) {
		return "", false
	}
	return s.PaneOpts[pane][key], true
}

// Global is a GlobalKeys option as the snapshot read it; ok as PaneOption.
func (s *Snapshot) Global(key string) (string, bool) {
	if s == nil || s.Globals == nil {
		return "", false
	}
	v, ok := s.Globals[key]
	return v, ok
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
