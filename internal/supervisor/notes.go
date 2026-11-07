package supervisor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// now is replaceable in tests.
var now = time.Now

func notesPath(home string) string  { return filepath.Join(home, "decisions.log") }
func pinnedPath(home string) string { return filepath.Join(home, "pinned.log") }

// AppendNote adds one line to decisions.log: `2026-11-04T21:40 [agent] text`.
func AppendNote(home, agent, text string) error { return AppendNoteBy(home, Main, agent, text) }

// AppendNoteBy is AppendNote written by one admiral: a line of any admiral
// but main reads `2026-11-07T21:40 @print [agent] text` (DESIGN §6.12).
func AppendNoteBy(home, admiral, agent, text string) error {
	line, err := noteLine(admiral, agent, text)
	if err != nil {
		return err
	}
	return appendLine(home, notesPath(home), line)
}

// AppendPinned adds a standing rule: the same line to decisions.log, which
// keeps the full history, and to pinned.log, which fleet_brief shows whole
// (ADR-62). The brief shows only the last 30 notes, so a rule the user set
// days ago would otherwise drop out of a new admiral's memory. Unpinning is
// deleting the line from pinned.log.
func AppendPinned(home, agent, text string) error { return AppendPinnedBy(home, Main, agent, text) }

// AppendPinnedBy is AppendPinned written by one admiral.
func AppendPinnedBy(home, admiral, agent, text string) error {
	line, err := noteLine(admiral, agent, text)
	if err != nil {
		return err
	}
	if err := appendLine(home, notesPath(home), line); err != nil {
		return err
	}
	return appendLine(home, pinnedPath(home), line)
}

func noteLine(admiral, agent, text string) (string, error) {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return "", fmt.Errorf("note text is empty")
	}
	line := now().Format("2006-01-02T15:04") + " "
	if !isMain(admiral) {
		line += "@" + admiral + " "
	}
	if agent = strings.TrimSpace(agent); agent != "" {
		line += "[" + agent + "] "
	}
	return line + text, nil
}

func appendLine(home, path, line string) error {
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line + "\n")
	return err
}

// TailNotes returns the last n lines of decisions.log (nil if none).
func TailNotes(home string, n int) []string {
	lines := readLines(notesPath(home))
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// Pinned returns every line of pinned.log, oldest first (nil if none).
func Pinned(home string) []string { return readLines(pinnedPath(home)) }

func readLines(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	s := strings.TrimRight(string(b), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// NoteWriter is the admiral a decisions.log or pinned.log line was written
// by: the `@name` after the timestamp, main when there is none.
func NoteWriter(line string) string {
	f := strings.Fields(line)
	if len(f) > 1 && strings.HasPrefix(f[1], "@") && len(f[1]) > 1 {
		return f[1][1:]
	}
	return Main
}
