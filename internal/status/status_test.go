package status

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each testdata/<set>/<state>[-<variant>].txt must classify as <state>.
func TestCaptures(t *testing.T) {
	sets := Load(t.TempDir())
	files, _ := filepath.Glob("testdata/*/*.txt")
	if len(files) == 0 {
		t.Fatal("no testdata")
	}
	for _, f := range files {
		b, _ := os.ReadFile(f)
		set := filepath.Base(filepath.Dir(f))
		want := Status(strings.SplitN(strings.TrimSuffix(filepath.Base(f), ".txt"), "-", 2)[0])
		got, _ := sets[set].Detect(string(b))
		if got != want {
			t.Errorf("%s: got %s want %s", f, got, want)
		}
	}
}

func TestUnknownAndOverride(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "patterns.toml"), []byte("[fake]\nidle = ['zzz']\n"), 0o644)
	s := Load(dir)["fake"]
	if st, ok := s.Detect("hello"); ok || st != Unknown {
		t.Errorf("got %s", st)
	}
	if st, _ := s.Detect("zzz"); st != Idle {
		t.Errorf("override: %s", st)
	}
	if len(s.Waiting) == 0 {
		t.Error("override should keep other keys")
	}
}

// waiting_for comes from the prompt line, not the footer under it.
func TestWaitingFor(t *testing.T) {
	cp := Load(t.TempDir())["copilot"]
	for f, want := range map[string]string{
		"waiting-question.txt":             "Copilot needs information. Should agents be allowed to message each other without approval?",
		"waiting-question-idle-footer.txt": "Copilot needs information. Which branch should I merge into?",
		"waiting-approval.txt":             "Do you want to run this command?",
		"error-dead.txt":                   "Pane is dead (status 0, Thu Nov  5 06:58:12 2026)",
	} {
		b, _ := os.ReadFile(filepath.Join("testdata/copilot", f))
		if _, got, _ := cp.Match(string(b)); got != want {
			t.Errorf("%s: got %q want %q", f, got, want)
		}
	}
}

func screen(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata/copilot", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestMenu(t *testing.T) {
	cp := Load(t.TempDir())["copilot"]
	m, ok := cp.Menu(screen(t, "waiting-approval.txt"))
	if !ok || len(m.Options) != 3 || m.Selected != 1 || m.Question != "Do you want to run this command?" {
		t.Fatalf("approval: %v %+v", ok, m)
	}
	if m.Options[1] != "2. Yes, and don't ask again for `make` commands in this session" || Label(m.Options[2]) != "No, and tell Copilot what to do differently (Esc to stop)" {
		t.Errorf("options: %q", m.Options)
	}
	m, ok = cp.Menu(screen(t, "waiting-dirs.txt"))
	if !ok || len(m.Options) != 3 || m.Selected != 2 || m.Question != "Do you want to allow this?" || m.Options[1] != "2. Yes, and add these directories to the allowed list" {
		t.Fatalf("dirs: %v %+v", ok, m)
	}
	for _, f := range []string{"idle.txt", "working.txt", "waiting-pasted.txt"} {
		if m, ok := cp.Menu(screen(t, f)); ok {
			t.Errorf("%s: unexpected menu %+v", f, m)
		}
	}
	// A numbered list in the transcript (scrollbar ┃) or without a cursor is no prompt.
	if _, ok := cp.Menu("  1. first step   ┃\n  2. second   ┃\n ❯ \n"); ok {
		t.Error("transcript list taken for a menu")
	}
	if _, ok := ParseMenu("1. a\n2. b\n"); ok {
		t.Error("list without cursor taken for a menu")
	}
	if got := Cut("abcdef", 4); got != "abc…" {
		t.Errorf("cut %q", got)
	}
}

func TestPending(t *testing.T) {
	cp := Load(t.TempDir())["copilot"]
	st, line, _ := cp.Match(screen(t, "waiting-pasted.txt"))
	if st != Waiting || line != "unsubmitted input: [Paste #1 - 13 lines]" {
		t.Errorf("got %s %q", st, line)
	}
	if _, ok := cp.Unsubmitted(screen(t, "idle.txt")); ok {
		t.Error("idle has no pending input")
	}
	// The chip quoted in an agent's reply (transcript not scrolled, so no
	// scrollbar) is not the input box.
	quoted := strings.Replace(screen(t, "idle.txt"), "\n ~/git/fleet", "\n ● The box shows [Paste #1 - 13 lines] until Enter.\n ~/git/fleet", 1)
	if !strings.Contains(strings.Join(Tail(quoted, Window), "\n"), "[Paste #1") {
		t.Fatal("quoted line not in the window")
	}
	if st, line, _ := cp.Match(quoted); st == Waiting || strings.Contains(line, "unsubmitted") {
		t.Errorf("quoted chip classified %s %q", st, line)
	}
	if _, ok := cp.Unsubmitted(quoted); ok {
		t.Error("quoted chip taken for pending input")
	}
}

func TestCleanScreen(t *testing.T) {
	for _, f := range []string{"waiting-pasted.txt", "waiting-approval.txt", "waiting-dirs.txt", "working.txt"} {
		raw := screen(t, f)
		got := CleanScreen(raw)
		if len(got) >= len(raw) {
			t.Errorf("%s: nothing removed", f)
		}
		prevBlank := false
		for _, l := range strings.Split(got, "\n") {
			if l != strings.TrimRight(l, " ") {
				t.Errorf("%s: trailing spaces %q", f, l)
			}
			if l != "" && strings.Trim(l, boxChars+" ") == "" {
				t.Errorf("%s: chrome line %q", f, l)
			}
			if l == "" && prevBlank {
				t.Errorf("%s: double blank line", f)
			}
			prevBlank = l == ""
			if strings.HasSuffix(l, "┃") || strings.HasSuffix(l, "│") || strings.HasPrefix(l, "│") {
				t.Errorf("%s: border left %q", f, l)
			}
		}
		if f == "waiting-approval.txt" && !strings.Contains(got, "\n❯ 1. Yes\n") {
			t.Errorf("approval options lost:\n%s", got)
		}
		if f == "waiting-pasted.txt" && !strings.Contains(got, "\n[Paste #1 - 13 lines]\n") {
			t.Errorf("paste chip lost:\n%s", got)
		}
	}
}

func TestLastMessage(t *testing.T) {
	p := filepath.Join(t.TempDir(), "events.jsonl")
	os.WriteFile(p, []byte(`{"type":"assistant.message","data":{"content":"first"}}
{"type":"assistant.message","data":{"content":"main answer","toolRequests":[]}}
{"type":"assistant.message","data":{"content":"","toolRequests":[{"name":"bash"}]}}
{"type":"assistant.message","data":{"content":"sub","agentId":"a1"}}
{"type":"assistant.message","data":{"content":"sub2","parentToolCallId":"toolu_1"}}
{"type":"tool.execution_start","data":{"toolName":"bash"}}
`), 0o644)
	if got, ok, err := LastMessage(p); err != nil || !ok || got != "main answer" {
		t.Errorf("got %q %v %v", got, ok, err)
	}
	old := LastMessageTail
	LastMessageTail = 120 // only the tail: the main answers are cut off
	defer func() { LastMessageTail = old }()
	if got, ok, _ := LastMessage(p); ok {
		t.Errorf("tail read found %q", got)
	}
	if _, _, err := LastMessage(p + ".missing"); err == nil {
		t.Error("missing file: want error")
	}
}

func TestInputBox(t *testing.T) {
	sets := Load(t.TempDir())
	cp := sets["copilot"]
	for name, want := range map[string]string{"idle.txt": "", "idle-autopilot.txt": "", "waiting-pasted.txt": "[Paste #1 - 13 lines]"} {
		if got, ok := cp.InputBox(screen(t, name)); !ok || got != want {
			t.Errorf("%s: got %q %v, want %q", name, got, ok, want)
		}
	}
	// Half-typed text wrapped over two box lines.
	typed := strings.Replace(screen(t, "idle.txt"), "\n┃\n", "\n┃ can we review active fleet\n┃ session please ? wha\n", 1)
	if got, ok := cp.InputBox(typed); !ok || got != "can we review active fleet session please ? wha" {
		t.Errorf("typed: got %q %v", got, ok)
	}
	if _, ok := cp.InputBox(screen(t, "working.txt")); !ok {
		t.Error("working screen: box not found")
	}
	// Copilot's other layout: ❯ between rules, wrapped onto an indented row.
	rule := strings.Repeat("─", 40)
	alt := " ~/x [⎇ main]\n" + rule + "\n❯ [fleet] report from a: blocked: need a decision on the\n  queue format. Full text: call fleet_reports.\n" + rule + "\n Interactive · Manual Approval · @ files · # issues · space hold to record\n"
	if got, ok := cp.InputBox(alt); !ok || got != "[fleet] report from a: blocked: need a decision on the queue format. Full text: call fleet_reports." {
		t.Errorf("❯ layout: got %q %v", got, ok)
	}
	if got, ok := cp.InputBox(strings.Replace(alt, "❯ [fleet] report from a: blocked: need a decision on the\n  queue format. Full text: call fleet_reports.", "❯", 1)); !ok || got != "" {
		t.Errorf("❯ layout empty: got %q %v", got, ok)
	}
	cl := sets["claude"]
	claude := "> earlier question\n\n● answer\n\n────────\n> on the acme sum\n  mary it needs\n────────\n  ? for shortcuts\n"
	if got, ok := cl.InputBox(claude); !ok || got != "on the acme sum mary it needs" {
		t.Errorf("claude: got %q %v", got, ok)
	}
	if got, ok := cl.InputBox(strings.Replace(claude, "> on the acme sum\n  mary it needs", ">", 1)); !ok || got != "" {
		t.Errorf("claude empty: got %q %v", got, ok)
	}
	if _, ok := (Set{}).InputBox("> hi\n"); ok {
		t.Error("a set without an input pattern must not claim a box")
	}
	// Claude's empty box is "❯" plus a non-breaking space, and an empty box
	// shows a placeholder hint: both read as empty, so a wake can be typed.
	for _, s := range []string{"\u00a0", ` Try "refactor <filepath>"`} {
		if got, ok := cl.InputBox("────────\n❯" + s + "\n────────\n  ? for shortcuts\n"); !ok || got != "" {
			t.Errorf("claude %q: got %q %v", s, got, ok)
		}
	}
}

// The window scan (ADR-40) reads the whole window, so a tool's own words
// must not look like its spinner or its error banner.
func TestWindowScanIgnoresTranscript(t *testing.T) {
	sets := Load(t.TempDir())
	footer := map[string]string{
		"claude": "  ⏵⏵ auto mode on (shift+tab to cycle)",
		"agy":    "  ctx ░░░░░░ 0%",
		"vibe":   "  0/202k tokens (0%)",
	}
	for _, tc := range []struct{ set, above string }{
		{"claude", "⎿  Error: ls: /nope: No such file or directory"},
		{"claude", "  * Refactoring…"},
		{"claude", "● I hit an error: the path was wrong, so I fixed it."},
		{"agy", "  ⎿ Error: command not found: fo"},
		{"vibe", "  ⎣ Error: ls: /nope: No such file or directory"},
	} {
		screen := tc.above + "\n────────\n>\u00a0\n────────\n" + footer[tc.set] + "\n"
		got, _, _ := sets[tc.set].Match(screen)
		if got != Idle {
			t.Errorf("%s %q: got %s, want idle", tc.set, tc.above, got)
		}
	}
}

// Every idle capture of a set with input patterns must show an empty prompt
// box, so the report wake (ADR-36) knows it may type; an "-typed" capture
// must read the text back instead. A tool busy, crashed or showing an
// approval box often has no box on screen, so those are not required to.
func TestInputBoxCaptures(t *testing.T) {
	sets := Load(t.TempDir())
	files, _ := filepath.Glob("testdata/*/*.txt")
	for _, f := range files {
		set := filepath.Base(filepath.Dir(f))
		s := sets[set]
		name := strings.TrimSuffix(filepath.Base(f), ".txt")
		if len(s.Input) == 0 || !strings.HasPrefix(name, "idle") {
			continue
		}
		b, _ := os.ReadFile(f)
		got, ok := s.InputBox(string(b))
		if !ok {
			t.Errorf("%s: no prompt box found", f)
			continue
		}
		if typed := strings.HasSuffix(name, "-typed"); typed != (got != "") {
			t.Errorf("%s: box %q, typed=%v", f, got, typed)
		}
	}
}
