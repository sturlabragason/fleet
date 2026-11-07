package supervisor

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	keepHandoffs = 20
	maxHandoff   = 4 << 10
)

func handoffPath(home, name string) string { return filepath.Join(DirOf(home, name), "handoff.md") }

// WriteHandoff writes admiral/handoff.md, moving the previous one to
// admiral/handoffs/<timestamp>.md and keeping the last 20 of those.
func WriteHandoff(home, text string) error { return WriteHandoffOf(home, Main, text) }

// WriteHandoffOf is WriteHandoff for one admiral: a reset of print never
// hands it main's plans.
func WriteHandoffOf(home, name, text string) error {
	dir, err := mkdirOf(home, name)
	if err != nil {
		return err
	}
	old := filepath.Join(dir, "handoffs")
	if err := os.MkdirAll(old, 0o755); err != nil {
		return err
	}
	if st, err := os.Stat(handoffPath(home, name)); err == nil {
		// <mtime>-<n>.md: lexical order is time order, also on collisions.
		base := st.ModTime().Format("20060102T150405.000000000")
		dst := ""
		for i := 0; dst == "" || exists(dst); i++ {
			dst = filepath.Join(old, fmt.Sprintf("%s-%03d.md", base, i))
		}
		if err := os.Rename(handoffPath(home, name), dst); err != nil {
			return err
		}
		prune(old)
	}
	return os.WriteFile(handoffPath(home, name), []byte(strings.TrimRight(text, "\n")+"\n"), 0o644)
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func prune(dir string) {
	es, _ := os.ReadDir(dir)
	var names []string
	for _, e := range es {
		if strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for len(names) > keepHandoffs {
		os.Remove(filepath.Join(dir, names[0]))
		names = names[1:]
	}
}

// ReadHandoff returns the latest handoff (capped at 4 KB) and its time.
func ReadHandoff(home string) (string, time.Time, bool) { return ReadHandoffOf(home, Main) }

// ReadHandoffOf is ReadHandoff for one admiral.
func ReadHandoffOf(home, name string) (string, time.Time, bool) {
	st, err := os.Stat(handoffPath(home, name))
	if err != nil {
		return "", time.Time{}, false
	}
	b, err := os.ReadFile(handoffPath(home, name))
	if err != nil {
		return "", time.Time{}, false
	}
	s := string(b)
	if len(s) > maxHandoff {
		s = s[:maxHandoff] + "\n…[truncated]"
	}
	return strings.TrimRight(s, "\n"), st.ModTime(), true
}
