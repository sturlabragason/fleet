package prefs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SetTheme writes `theme = "<name>"` under [defaults] in the config file
// (the overview's theme picker, ADR-175). The rest of the file is kept as
// written: an existing theme line is replaced in place, otherwise the key
// goes first under [defaults], and a file without that table gets it at the
// end. A symlinked config is written through the link, atomically.
func SetTheme(name string) error {
	path := Path()
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target
	}
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read config: %w", err)
	}
	mode := fs.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	out := setDefault(string(b), "theme", strconv.Quote(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("config dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config.toml.*")
	if err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	defer os.Remove(tmp.Name()) // gone after the rename; cleans up on error
	if _, err := tmp.WriteString(out); err != nil {
		tmp.Close()
		return fmt.Errorf("write config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// setDefault sets key = val (val already TOML) in the [defaults] table of
// the TOML text s.
func setDefault(s, key, val string) string {
	line := key + " = " + val
	lines := strings.Split(s, "\n")
	sec, head := "", -1
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "[") {
			end := strings.Index(t, "]")
			if end < 0 {
				end = len(t)
			}
			sec = strings.Trim(strings.TrimSpace(strings.TrimLeft(t[:end], "[")), `"`)
			if sec == "defaults" {
				head = i
			}
			continue
		}
		if k, _, ok := strings.Cut(t, "="); ok && sec == "defaults" && strings.Trim(strings.TrimSpace(k), `"`) == key {
			lines[i] = line
			return strings.Join(lines, "\n")
		}
	}
	if head >= 0 {
		return strings.Join(append(lines[:head+1], append([]string{line}, lines[head+1:]...)...), "\n")
	}
	if s != "" && !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	if s != "" {
		s += "\n"
	}
	return s + "[defaults]\n" + line + "\n"
}
