package summary

import (
	"fmt"
	"strings"
)

// ScreenChars caps how much pane text is sent to the model. A summary is
// about the last screens, not the whole scrollback, and the cap is what keeps
// a call cheap.
const ScreenChars = 6000

// MessageChars caps the agent's last message and its task line.
const MessageChars = 2000

const instructions = `You are reading the terminal screen of one AI coding agent in a fleet.
Write at most 5 short lines of plain text. No markdown, no numbering, no
bullets, no preamble. Cover, one per line and only when there is something to
say: what the agent is doing right now; what it is waiting for (a prompt, a
question, another agent, nothing); any error or failure visible on the screen.
Never invent progress that is not on the screen. Do not quote the screen back.
Do not use any tools: answer directly.`

// Prompt builds the model prompt for one agent.
func Prompt(in Input) string {
	var b strings.Builder
	b.WriteString(instructions)
	fmt.Fprintf(&b, "\n\nAgent: %s", in.Agent)
	if in.Status != "" {
		fmt.Fprintf(&b, "\nScraped status: %s", in.Status)
	}
	if t := strings.TrimSpace(in.Task); t != "" {
		fmt.Fprintf(&b, "\nTask: %s", tail(t, MessageChars))
	}
	if m := strings.TrimSpace(in.LastMessage); m != "" {
		fmt.Fprintf(&b, "\n\nIts last message:\n%s", tail(m, MessageChars))
	}
	fmt.Fprintf(&b, "\n\nIts screen (newest last):\n%s\n", tail(strings.TrimSpace(in.Screen), ScreenChars))
	return b.String()
}

// tail keeps the last n characters, which is the recent part of a screen.
func tail(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return "…" + string(r[len(r)-n+1:])
}
