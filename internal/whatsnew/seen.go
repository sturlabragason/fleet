package whatsnew

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sturlabragason/fleet/internal/logx"
)

// SeenPath is the file of seen entry ids, one per line.
func SeenPath() string { return filepath.Join(logx.Home(), "whatsnew-seen") }

// LoadSeen returns the seen ids. A missing file means nothing was seen.
func LoadSeen() (map[string]bool, error) {
	b, err := os.ReadFile(SeenPath())
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return map[string]bool{}, err
	}
	seen := map[string]bool{}
	for l := range strings.SplitSeq(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			seen[l] = true
		}
	}
	return seen, nil
}

// MarkSeen adds the entries' ids to the seen file, keeping the ones there.
func MarkSeen(es []Entry) error {
	old, err := LoadSeen()
	if err != nil {
		return err
	}
	b, _ := os.ReadFile(SeenPath())
	var sb strings.Builder
	sb.WriteString(strings.TrimRight(string(b), "\n"))
	if sb.Len() > 0 {
		sb.WriteByte('\n')
	}
	added := 0
	for _, e := range es {
		if id := e.ID(); !old[id] {
			old[id] = true
			sb.WriteString(id + "\n")
			added++
		}
	}
	if added == 0 {
		return nil
	}
	dir := filepath.Dir(SeenPath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".whatsnew-seen-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(sb.String()); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), SeenPath())
}
