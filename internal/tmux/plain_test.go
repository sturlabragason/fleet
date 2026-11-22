package tmux

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/status"
)

// TestPlainCursorOnPlaceholder: Claude's empty box with queued messages shows
// "Press up to edit queued messages" with the cursor as an inverse cell on
// the "P" and the rest faint. Read as typed "P", it refused fleet_send to an
// idle agent. The fixtures are `capture-pane -p -e -J` output of Claude's
// box bytes in a fleet-test tmux; typed text must still read as typed.
func TestPlainCursorOnPlaceholder(t *testing.T) {
	claude := status.Load(t.TempDir())["claude"]
	for _, tc := range []struct{ file, want string }{
		{"claude-placeholder-queued.ansi", ""},
		{"claude-typed.ansi", "Please check the logs"},
		{"claude-typed-suggestion.ansi", "P"}, // typed "P", cursor on the faint suggestion
	} {
		t.Run(tc.file, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join("testdata", tc.file))
			if err != nil {
				t.Fatal(err)
			}
			got, ok := claude.InputBox(Plain(string(b)))
			if !ok || got != tc.want {
				t.Errorf("InputBox = %q %v, want %q", got, ok, tc.want)
			}
		})
	}
}

// TestPlainInverseCell: only a lone inverse cell before a trailing faint run
// is a cursor; a longer inverse run (a badge) and colour arguments holding a
// 7 or 2 are content.
func TestPlainInverseCell(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"\x1b[7m❯\x1b[0;2mghost", ""},                      // multi-byte cursor cell
		{"ok \x1b[7mBADGE\x1b[0;2mhint", "ok BADGE"},        // inverse run, not a cursor
		{"\x1b[38;5;7mP\x1b[0;2mhint", "P"},                 // 7 is a colour index
		{"\x1b[48;2;7;7;7mP\x1b[0;2mhint", "P"},             // truecolour 7s
		{"\x1b[7mP\x1b[0;2mress\x1b[0m done", "Press done"}, // normal text after: kept
	} {
		if got := strings.TrimSpace(Plain(tc.in)); got != tc.want {
			t.Errorf("Plain(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
