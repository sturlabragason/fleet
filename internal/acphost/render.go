package acphost

import (
	"fmt"
	"strings"
)

// maxLine caps one drawn line: the pane is a small wall tile.
const maxLine = 400

// render is how an entry is drawn in the agent's pane: plain text, short,
// "" for entries that aren't drawn (status changes other than error).
func render(e Entry) string {
	switch e.Kind {
	case KindUser:
		return "> " + clip(e.Text)
	case KindAgent:
		return clip(e.Text)
	case KindThought:
		return "~ " + clip(firstLine(e.Text))
	case KindTool:
		s := "* " + clip(firstLine(e.Title))
		if e.Status != "" {
			s += " (" + e.Status + ")"
		}
		return s
	case KindPermission:
		var b strings.Builder
		b.WriteString("? " + clip(e.Title))
		for i, o := range e.Options {
			fmt.Fprintf(&b, "\n  %d) %s", i+1, o.Name)
		}
		b.WriteString("\n  type a number to answer")
		return b.String()
	case KindAnswer:
		return "  -> " + e.Text
	case KindError:
		return "! " + clip(e.Text)
	case KindSession:
		return "session " + e.SessionID + " " + e.Text
	case KindStatus:
		if e.Status == "error" {
			return "! status error"
		}
	}
	return ""
}

// summary is an entry as one line, for a tile's last line.
func summary(e Entry) string {
	switch e.Kind {
	case KindAgent, KindUser, KindError:
		return clip(lastLine(e.Text))
	case KindPermission:
		return "? " + clip(firstLine(e.Title))
	case KindTool:
		return "* " + clip(firstLine(e.Title))
	}
	return ""
}

func clip(s string) string {
	s = strings.TrimRight(s, "\n ")
	if r := []rune(s); len(r) > maxLine {
		return string(r[:maxLine-1]) + "…"
	}
	return s
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[i+1:])
	}
	return s
}
