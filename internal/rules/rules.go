// Package rules keeps the user's standing rules for every agent as bullets
// under one heading in a worker playbook layer: the user's <fleet home>/
// worker.md, or a repo's .fleet/worker.md (ADR-131). The playbook prepends
// those layers to every spawn's first prompt (ADR-22), so a rule stated once
// reaches every later agent. Everything else in the file is the user's and
// is kept byte for byte.
package rules

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/sturlabragason/fleet/internal/logx"
)

// Heading opens the section the rules live in.
const Heading = "## Standing user rules"

// MaxLen caps one rule in characters: every rule is paid for on every spawn.
const MaxLen = 300

// UserFile is the user's playbook layer.
func UserFile(home string) string { return filepath.Join(home, "worker.md") }

// RepoFile is the playbook layer of the repo holding dir: .fleet/worker.md at
// the first directory up from dir with a .git entry (a worktree's .git file
// counts, so a rule lands in the worktree's checkout of the file).
func RepoFile(dir string) (string, error) {
	d, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return filepath.Join(d, ".fleet", "worker.md"), nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", fmt.Errorf("%s is not in a git repo", dir)
		}
		d = parent
	}
}

// Normalize folds a rule to one trimmed line and refuses an empty or long one.
func Normalize(text string) (string, error) {
	r := strings.Join(strings.Fields(text), " ")
	if r == "" {
		return "", errors.New("rule text is empty")
	}
	if n := utf8.RuneCountInString(r); n > MaxLen {
		return "", fmt.Errorf("rule is %d characters, the limit is %d: a rule is paid for in tokens on every spawn, so say it shorter", n, MaxLen)
	}
	return r, nil
}

// Add appends a rule to the section in path, creating the file and the
// heading when missing. A rule already there (same text, any case) is not
// added again: added is false. rule is the normalised text.
func Add(path, text string) (rule string, added bool, err error) {
	rule, err = Normalize(text)
	if err != nil {
		return "", false, err
	}
	content, err := read(path)
	if err != nil {
		return "", false, err
	}
	lines, h, bullets := section(content)
	for _, i := range bullets {
		if strings.EqualFold(bulletText(lines[i]), rule) {
			return rule, false, nil
		}
	}
	switch {
	case h < 0:
		base := strings.TrimRight(content, "\n")
		if base != "" {
			base += "\n\n"
		}
		content = base + Heading + "\n\n- " + rule + "\n"
	case len(bullets) == 0:
		content = strings.Join(insert(lines, h+1, "", "- "+rule), "\n")
	default:
		content = strings.Join(insert(lines, bullets[len(bullets)-1]+1, "- "+rule), "\n")
	}
	if err := write(path, content); err != nil {
		return "", false, err
	}
	logx.For("rules").Info("added", "path", path, "rule", rule)
	return rule, true, nil
}

// List returns the rules in path, in file order.
func List(path string) ([]string, error) {
	content, err := read(path)
	if err != nil {
		return nil, err
	}
	lines, _, bullets := section(content)
	out := make([]string, 0, len(bullets))
	for _, i := range bullets {
		out = append(out, bulletText(lines[i]))
	}
	return out, nil
}

// Remove deletes rule n (1-based, as List numbers them) and returns its text.
func Remove(path string, n int) (string, error) {
	content, err := read(path)
	if err != nil {
		return "", err
	}
	lines, _, bullets := section(content)
	if n < 1 || n > len(bullets) {
		return "", fmt.Errorf("no rule %d in %s (%d rules)", n, path, len(bullets))
	}
	i := bullets[n-1]
	rule := bulletText(lines[i])
	if err := write(path, strings.Join(append(lines[:i:i], lines[i+1:]...), "\n")); err != nil {
		return "", err
	}
	logx.For("rules").Info("removed", "path", path, "rule", rule)
	return rule, nil
}

// section splits content into lines and finds the heading's line (-1 if
// none) and the bullet lines of its section, which ends at the next heading.
func section(content string) (lines []string, h int, bullets []int) {
	lines = strings.Split(content, "\n")
	h = -1
	for i, l := range lines {
		if strings.TrimRight(l, " \t\r") == Heading {
			h = i
			break
		}
	}
	if h < 0 {
		return lines, h, nil
	}
	for i := h + 1; i < len(lines) && !strings.HasPrefix(lines[i], "#"); i++ {
		if strings.HasPrefix(lines[i], "- ") {
			bullets = append(bullets, i)
		}
	}
	return lines, h, bullets
}

func bulletText(line string) string { return strings.TrimSpace(strings.TrimPrefix(line, "- ")) }

func insert(lines []string, at int, add ...string) []string {
	out := append([]string{}, lines[:at]...)
	out = append(out, add...)
	return append(out, lines[at:]...)
}

func read(path string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return string(b), err
}

// write replaces path atomically: a temp file in the same directory, then a
// rename. ponytail: no lock, so two writers at the same instant can lose one
// rule; add a flock if rules are ever written by more than one person at once.
func write(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".fleet-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
