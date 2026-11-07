package supervisor

import (
	"os"
	"path/filepath"
	"strings"
)

func choicePath(home, name string) string { return filepath.Join(DirOf(home, name), "profile") }

// LoadChoice returns the admiral profile last chosen (DESIGN §6.10), "" if none.
func LoadChoice(home string) string { return LoadChoiceOf(home, Main) }

// LoadChoiceOf is LoadChoice for one admiral.
func LoadChoiceOf(home, name string) string {
	b, err := os.ReadFile(choicePath(home, name))
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(string(b), "\n")
	return strings.TrimSpace(line)
}

// SaveChoice persists the admiral profile choice as one line.
func SaveChoice(home, profile string) error { return SaveChoiceOf(home, Main, profile) }

// SaveChoiceOf is SaveChoice for one admiral.
func SaveChoiceOf(home, name, profile string) error {
	if _, err := mkdirOf(home, name); err != nil {
		return err
	}
	tmp := choicePath(home, name) + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.TrimSpace(profile)+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, choicePath(home, name))
}
