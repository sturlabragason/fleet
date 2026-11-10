package tmux

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testClient returns a client on a throwaway socket, killed at cleanup.
func testClient(t *testing.T) *Client {
	t.Helper()
	c := &Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { _ = c.KillServer() })
	return c
}

func TestSpawnPasteCapture(t *testing.T) {
	c := testClient(t)
	p, err := c.Spawn("s", "w1", "", nil, "cat")
	if err != nil || !strings.HasPrefix(p, "%") {
		t.Fatalf("spawn: %q %v", p, err)
	}
	if err := c.SetPaneOption(p, "@fleet_agent", "a1"); err != nil {
		t.Fatal(err)
	}
	if err := c.Paste(p, "hello fleet", true); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		out, _ := c.Capture(p, 0)
		if strings.Count(out, "hello fleet") >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("capture: %q", out)
		}
		time.Sleep(50 * time.Millisecond)
	}
	ps, err := c.ListPanes("")
	if err != nil || len(ps) != 1 || ps[0].Agent != "a1" || ps[0].Window != "w1" {
		t.Fatalf("list: %+v %v", ps, err)
	}
	p2, _ := c.Spawn("s", "w2", "", nil, "cat")
	if _, err := c.Run("join-pane", "-d", "-s", p2, "-t", p); err != nil {
		t.Fatal(err)
	}
	ps, _ = c.ListPanes(p)
	if len(ps) != 2 {
		t.Fatalf("join: %+v", ps)
	}
}

func TestListPanesDeadStatus(t *testing.T) {
	c := testClient(t)
	p, err := c.Spawn("s", "w", "", nil, "sleep 0.2; exit 3")
	if err != nil {
		t.Fatal(err)
	}
	c.SetPaneOption(p, "remain-on-exit", "on")
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		ps, _ := c.ListPanes("")
		if len(ps) == 1 && ps[0].Dead {
			if ps[0].DeadStatus != "3" || ps[0].DeadSignal != "" {
				t.Fatalf("dead fields: %+v", ps[0])
			}
			return
		}
	}
	t.Fatal("pane never died")
}

// TestCaptureDropsGhostText: Claude Code draws its next-prompt suggestion
// faint in the empty input box. Read as typed text, it kept every report
// wake out of the admiral pane and froze an idle agent at "working" until
// fleet called it stuck. The bytes are copied from real `capture-pane -e`
// lines (Claude's prompt, Copilot's transcript bar), text scrubbed. Faint text with normal text after it is content and stays.
func TestCaptureDropsGhostText(t *testing.T) {
	ghost := "\x1b[39m❯ \x1b[2mgo with the first option\x1b[0m"
	bar := "\x1b[2m\x1b[38;2;145;152;161m│\x1b[0m \x1b[3m\x1b[38;2;145;152;161mrunning ffprobe\x1b[0m"
	color := "\x1b[38;2;145;152;161mnot faint\x1b[0m"
	c := testClient(t)
	p, err := c.Spawn("s", "w1", "", nil, "printf '%s\\n%s\\n%s\\n' '"+ghost+"' '"+bar+"' '"+color+"'; sleep 30")
	if err != nil {
		t.Fatal(err)
	}
	var got string
	for i := 0; i < 50 && !strings.Contains(got, "not faint"); i++ {
		time.Sleep(50 * time.Millisecond)
		got, _ = c.Capture(p, 0)
	}
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "❯" || lines[1] != "│ running ffprobe" || lines[2] != "not faint" {
		t.Fatalf("capture = %q", got)
	}
	if strings.Contains(got, "\x1b") {
		t.Errorf("escapes left in %q", got)
	}
}

// Without a UTF-8 locale the tmux client prints tabs in -F output as '_' (#21).
func TestListPanesWithoutUTF8Locale(t *testing.T) {
	for _, k := range []string{"LANG", "LC_ALL", "LC_CTYPE", "TMUX"} {
		t.Setenv(k, "") // restores the original value at cleanup
		os.Unsetenv(k)
	}
	c := testClient(t)
	p, err := c.Spawn("s", "w", "", nil, "cat")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetPaneOption(p, "@fleet_agent", "a1"); err != nil {
		t.Fatal(err)
	}
	ps, err := c.ListPanes("")
	if err != nil || len(ps) != 1 || ps[0].ID != p || ps[0].Agent != "a1" {
		t.Fatalf("list: %+v %v", ps, err)
	}
}

// tmux 3.8 refuses send-keys while the best attached client is read-only;
// SendKeys then writes the keys' bytes with paste-buffer instead.
func TestSendKeysWithReadOnlyClient(t *testing.T) {
	c := testClient(t)
	p, err := c.Spawn("s", "w", "", nil, "cat")
	if err != nil {
		t.Fatal(err)
	}
	ro := exec.Command("tmux", append(c.base(), "-C", "attach", "-r", "-t", "s")...)
	in, _ := ro.StdinPipe()
	if err := ro.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { in.Close(); ro.Wait() })
	deadline := time.Now().Add(3 * time.Second)
	for {
		out, _ := c.Run("list-clients", "-F", "#{client_readonly}")
		if strings.Contains(out, "1") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("read-only client never attached")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := c.SendKeys(p, "-l", "--", "abcx"); err != nil {
		t.Fatal(err)
	}
	if err := c.SendKeys(p, "BSpace", "Enter"); err != nil {
		t.Fatal(err)
	}
	for {
		out, _ := c.Capture(p, 0)
		if strings.Count(out, "abc\n") >= 2 || strings.Count(out, "abc") >= 2 {
			break
		}
		if time.Now().After(deadline.Add(2 * time.Second)) {
			t.Fatalf("capture: %q", out)
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Older tmux (before 3.8) does not refuse send-keys here at all.
	if _, raw := c.Run("send-keys", "-t", p, "C-c"); raw != nil {
		if err := c.SendKeys(p, "C-c"); err == nil {
			t.Fatal("a key with no byte form must still fail")
		}
	}
}

// A TMUX_TMPDIR that was removed must not let tmux fall back to the user's
// default socket dir, where -L fleet is the real fleet server (ADR-81).
func TestRunRefusesGoneTmpdir(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", filepath.Join(t.TempDir(), "gone"))
	c := New("fleet-test-gone")
	if _, err := c.Run("set-option", "-gu", "@fleet_reports"); err != ErrTmpdirGone {
		t.Fatalf("err = %v, want ErrTmpdirGone", err)
	}
	if c.HasSession("fleet") {
		t.Error("HasSession through a gone TMUX_TMPDIR")
	}
}
