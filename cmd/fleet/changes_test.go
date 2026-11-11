package main

import (
	"fmt"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/keys"
	"github.com/sturlabragason/fleet/internal/tmux"
	"github.com/sturlabragason/fleet/internal/whatsnew"
)

func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("FLEET_HOME", t.TempDir())
	t.Setenv("FLEET_CONFIG", filepath.Join(t.TempDir(), "none.toml"))
}

const testMD = "## 2026-11-05\n" +
	"- Seen entry (abc1234)\n" +
	"- New entry with `fleet up` and enough words to wrap on a narrow popup for sure (ADR-28)\n" +
	"## 2026-11-04\n" +
	"- Older entry\n"

func testPopup(t *testing.T, es []whatsnew.Entry, w, h int) (*changesPopup, *[][]whatsnew.Entry) {
	t.Helper()
	isNew := map[string]bool{}
	for _, e := range es {
		if e.ID() != "abc1234" {
			isNew[e.ID()] = true
		}
	}
	var marked [][]whatsnew.Entry
	p := newChangesPopup("fleet 66a2ece · built 2026-11-05 09:00", es, isNew,
		func(es []whatsnew.Entry) error { marked = append(marked, es); return nil })
	if p.View() != "" {
		t.Fatal("before the first WindowSizeMsg the popup must draw nothing")
	}
	p.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return p, &marked
}

func checkWidth(t *testing.T, p *changesPopup) {
	t.Helper()
	lines := strings.Split(p.View(), "\n")
	if len(lines) > p.h {
		t.Errorf("%d lines for height %d", len(lines), p.h)
	}
	for _, l := range lines {
		if ansi.StringWidth(l) > p.w {
			t.Errorf("line wider than %d: %q", p.w, ansi.Strip(l))
		}
	}
}

func TestChangesPopupRows(t *testing.T) {
	isolate(t)
	es := whatsnew.Parse(testMD)
	p, marked := testPopup(t, es, 200, 30)
	lines := strings.Split(ansi.Strip(p.View()), "\n")
	if lines[0] != " fleet 66a2ece · built 2026-11-05 09:00 · 2 new" || lines[1] != "" {
		t.Fatalf("header: %q", lines[:2])
	}
	want := []string{
		" 2026-11-05     Seen entry (abc1234)",
		"             ✦  New entry with fleet up and enough words to wrap on a narrow popup for sure (ADR-28)",
		"",
		" 2026-11-04  ✦  Older entry",
	}
	if got := lines[2:]; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("rows:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if strings.Contains(p.View(), "`") {
		t.Error("backticks must be stripped")
	}
	if *marked != nil {
		t.Fatal("marked while open")
	}
	p.markAll()
	if len(*marked) != 1 || len((*marked)[0]) != 3 {
		t.Errorf("markAll must mark every entry seen: %v", *marked)
	}
	checkWidth(t, p)
}

func TestChangesPopupWrap(t *testing.T) {
	isolate(t)
	es := whatsnew.Parse(testMD)
	p, _ := testPopup(t, es, 50, 30) // text column 50-2-15 = 33
	lines := strings.Split(ansi.Strip(p.View()), "\n")
	var entry []string
	for _, l := range lines[3:] {
		if l == "" {
			break
		}
		entry = append(entry, l)
	}
	if len(entry) < 3 {
		t.Fatalf("no wrap: %q", entry)
	}
	for _, l := range entry[1:] {
		if !strings.HasPrefix(l, strings.Repeat(" ", 1+wnIndent)) || l[1+wnIndent] == ' ' {
			t.Errorf("continuation not under the text: %q", l)
		}
	}
	if last := entry[len(entry)-1]; !strings.HasSuffix(last, "(ADR-28)") || strings.Count(strings.Join(entry, "\n"), "(ADR-28)") != 1 {
		t.Errorf("refs split or missing: %q", entry)
	}
	checkWidth(t, p)
	// The text column stops at 100 cells on a wide popup.
	p.Update(tea.WindowSizeMsg{Width: 400, Height: 30})
	long := whatsnew.Entry{Date: "2026-11-05", Text: strings.Repeat("word ", 40)}
	for _, l := range wrapEntry(long, min(400-wnIndent, wnMaxText)) {
		if ansi.StringWidth(l) > wnMaxText {
			t.Errorf("text wider than %d: %d", wnMaxText, ansi.StringWidth(l))
		}
	}
	// Refs that don't fit go on their own line, unbroken.
	e := whatsnew.Entry{Date: "2026-11-05", Text: "abcdefghij", Refs: []string{"66a2ece", "ADR-27"}}
	got := wrapEntry(e, 12)
	if len(got) != 2 || ansi.Strip(got[1]) != "(66a2ece, ADR-27)" {
		t.Errorf("refs line: %q", got)
	}
}

func TestChangesPopupScroll(t *testing.T) {
	isolate(t)
	var md strings.Builder
	for d := 28; d >= 1; d-- {
		fmt.Fprintf(&md, "## 2026-10-%02d\n- entry %d\n", d, d)
	}
	es := whatsnew.Parse(md.String())
	p, _ := testPopup(t, es, 80, 12)
	v := ansi.Strip(p.View())
	if !strings.HasSuffix(v, "↓ more") || !strings.Contains(v, "entry 28") {
		t.Fatalf("top: %q", v)
	}
	p.Update(tea.KeyMsg{Type: tea.KeyUp})
	if p.top != 0 {
		t.Errorf("up at the top: %d", p.top)
	}
	p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if p.top != 1 {
		t.Errorf("j: %d", p.top)
	}
	p.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown})
	if p.top != 4 {
		t.Errorf("wheel: %d", p.top)
	}
	p.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if p.top != 4+p.page() {
		t.Errorf("pgdown: %d", p.top)
	}
	p.Update(tea.KeyMsg{Type: tea.KeyEnd})
	v = ansi.Strip(p.View())
	if p.top != len(p.lines)-p.bodyH() || strings.Contains(v, "more") || !strings.HasSuffix(v, "entry 1") {
		t.Errorf("end: top %d %q", p.top, v)
	}
	p.Update(tea.KeyMsg{Type: tea.KeyDown})
	if p.top != len(p.lines)-p.bodyH() {
		t.Errorf("down past the end: %d", p.top)
	}
	p.Update(tea.KeyMsg{Type: tea.KeyHome})
	if p.top != 0 {
		t.Errorf("home: %d", p.top)
	}
	checkWidth(t, p)
	p.Update(tea.WindowSizeMsg{Width: 20, Height: 3})
	checkWidth(t, p)
}

func TestChangesPopupQuitAndEmpty(t *testing.T) {
	isolate(t)
	for _, k := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("q")}, {Type: tea.KeyRunes, Runes: []rune("g")},
		{Type: tea.KeyEsc}, {Type: tea.KeyCtrlC}, {Type: tea.KeyRunes, Runes: []rune("g"), Alt: true},
	} {
		p, _ := testPopup(t, nil, 80, 10)
		if _, cmd := p.Update(k); cmd == nil {
			t.Errorf("%s must quit", k)
		} else if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("%s: not quit", k)
		}
	}
	p, _ := testPopup(t, nil, 80, 10)
	if _, cmd := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")}); cmd != nil {
		t.Error("G must not quit")
	}
	if v := ansi.Strip(p.View()); !strings.Contains(v, "no entries") || strings.Contains(v, "new") {
		t.Errorf("empty: %q", v)
	}
}

func TestChangesText(t *testing.T) {
	isolate(t)
	es := whatsnew.Parse(testMD)
	got := whatsnew.Text("fleet x", es, map[string]bool{es[1].ID(): true})
	want := "fleet x · 1 new\n" +
		"2026-11-05     Seen entry  (abc1234)\n" +
		"            ✦  New entry with fleet up and enough words to wrap on a narrow popup for sure  (ADR-28)\n" +
		"2026-11-04     Older entry\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if got := whatsnew.Text("fleet x", nil, nil); got != "fleet x\nno entries\n" {
		t.Errorf("empty: %q", got)
	}
}

func TestChangesBindings(t *testing.T) {
	var bind, click, sr []string
	for _, c := range tmuxCommands(&fleet.Fleet{Exe: "/opt/my fleet/fleet"}) {
		switch {
		case len(c) > 3 && c[0] == "bind-key" && c[2] == keys.ChangesTmuxKey:
			bind = c
		case len(c) > 3 && c[0] == "bind-key" && c[2] == "MouseDown1Status":
			click = c
		case len(c) == 4 && c[2] == "status-right":
			sr = c
		}
	}
	if bind == nil || click == nil || sr == nil {
		t.Fatalf("bind %v click %v status-right %v", bind, click, sr)
	}
	if got := strings.Join(bind[3:], " "); got != "display-popup -E -b rounded -w 90% -h 85% -T "+changesTitle+" '/opt/my fleet/fleet' changes --popup" {
		t.Errorf("M-g: %q", got)
	}
	if !strings.Contains(sr[3], "#[range=user|changes]Alt-g what's new#[norange]") {
		t.Errorf("status-right lacks the clickable range: %q", sr[3])
	}
	if click[3] != "if-shell" || click[5] != "#{==:#{mouse_status_range},changes}" || !strings.Contains(click[6], "changes --popup") {
		t.Errorf("click binding %q", click)
	}
	// The full tmux hint fits the status line, so Alt-g what's new shows.
	if h := keys.Hint(keys.ScopeTmux, 1000); ansi.StringWidth(h) > statusRightLen-1 || keys.Hint(keys.ScopeTmux, statusRightLen-1) != h {
		t.Errorf("full hint %q is %d wide, status-right holds %d", h, ansi.StringWidth(h), statusRightLen-1)
	}
	// A narrow hint without the item gets no range.
	if got := clickableChanges("Alt-z zoom · Alt-? all keys"); strings.Contains(got, "range") {
		t.Errorf("range without the hint: %q", got)
	}
}

// A click elsewhere on the status line must keep doing what tmux does by
// default, and tmux must accept the binding.
func TestStatusClickKeepsTmuxDefault(t *testing.T) {
	c := &tmux.Client{Socket: fmt.Sprintf("fleet-test-%d", rand.Int63()), Config: "/dev/null"}
	t.Cleanup(func() { c.KillServer() })
	if _, err := c.Run("new-session", "-d", "-s", "f", "-n", "main", "cat"); err != nil {
		t.Skip("tmux unavailable")
	}
	def, err := c.Run("list-keys", "-T", "root", "MouseDown1Status")
	if err != nil {
		t.Fatal(err)
	}
	_, want, _ := strings.Cut(strings.TrimSpace(def), "MouseDown1Status ")
	if want != tmuxDefaultStatusClick {
		t.Errorf("tmux's default status click is %q, the else branch is %q", want, tmuxDefaultStatusClick)
	}
	configure(&fleet.Fleet{T: c, Exe: "/opt/my fleet/fleet"})
	got, _ := c.Run("list-keys", "-T", "root", "MouseDown1Status")
	if !strings.Contains(got, "if-shell") || !strings.HasSuffix(strings.TrimSpace(got), `"`+tmuxDefaultStatusClick+`"`) {
		t.Errorf("bound: %q", got)
	}
	if !strings.Contains(got, "what") || !strings.Contains(got, "new in fleet · q closes") {
		t.Errorf("click binding lacks the popup title: %q", got)
	}
	// The popup command string parses: running it fails only for want of a client.
	if _, err := c.Run("if-shell", "-F", "1", changesStatusClick("'/opt/my fleet/fleet'")[6]); err != nil && (strings.Contains(err.Error(), "syntax") || strings.Contains(err.Error(), "unknown")) {
		t.Errorf("popup command string: %v", err)
	}
}
