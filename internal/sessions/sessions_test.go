package sessions

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/tools"
)

// fixture builds a fake Copilot store shared by two profiles and a fake Claude
// store, all under a temp dir. Never touches the real ~/.copilot or ~/.claude*.
func fixture(t *testing.T) (map[string]tools.Profile, string, string) {
	t.Helper()
	root := t.TempDir()
	cop := filepath.Join(root, "copilot")
	cla := filepath.Join(root, "claude", "projects")
	ps := map[string]tools.Profile{
		"copilot":        {Name: "copilot", SessionPath: cop + "/{id}"},
		"claude-copilot": {Name: "claude-copilot", Args: []string{"--model", "claude-opus-5", "--allow-all"}, SessionPath: cop + "/{id}"},
		"claude":         {Name: "claude", SessionPath: cla + "/*/{id}.jsonl"},
		"fake":           {Name: "fake"},
	}
	now := time.Now()
	at := func(p string, ago time.Duration) { os.Chtimes(p, now.Add(-ago), now.Add(-ago)) }
	cs := func(id, yaml, events string, ago time.Duration) {
		d := filepath.Join(cop, id)
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, "workspace.yaml"), []byte(yaml), 0o644)
		at(filepath.Join(d, "workspace.yaml"), ago)
		if events != "" {
			os.WriteFile(filepath.Join(d, "events.jsonl"), []byte(events), 0o644)
			at(filepath.Join(d, "events.jsonl"), ago)
		}
	}
	start := func(model string) string {
		return `{"type":"session.start","data":{"selectedModel":"` + model + `","context":{"cwd":"/ctx"}}}` + "\n"
	}
	cs("c-named-0001", "id: c-named-0001\ncwd: /src/portal\nbranch: feat/nav\nname: Portal nav sidebar\nsummary: ignored\n",
		start("gpt-5.2")+`{"type":"user.message","data":{"content":"do it"}}`+"\n", time.Minute)
	cs("c-prompt-0002", "id: c-prompt-0002\ncwd: /src/orch\n",
		start("claude-opus-5.5")+`{"type":"assistant.message","data":{"content":"x"}}`+"\n"+`{"type":"user.message","data":{"content":"fix the\nworker  retry"}}`+"\n", 2*time.Hour)
	cs("c-noevent-003", "id: c-noevent-003\ncwd: /tmp/probe\n", "", 3*time.Minute)
	cs("c-noprompt-04", "id: c-noprompt-04\ncwd: /tmp/hook\n", start("gpt-5"), 4*time.Minute)

	cl := func(dir, id, body string, ago time.Duration) {
		os.MkdirAll(filepath.Join(cla, dir), 0o755)
		p := filepath.Join(cla, dir, id+".jsonl")
		os.WriteFile(p, []byte(body), 0o644)
		at(p, ago)
	}
	user := func(content string) string {
		return `{"type":"user","cwd":"/src/ledger","gitBranch":"main","message":{"role":"user","content":` + content + `}}` + "\n"
	}
	cl("-src-ledger", "k-title-0001", user(`"first prompt"`)+`{"type":"assistant","message":{"model":"claude-sonnet-5","content":[]}}`+"\n"+`{"type":"custom-title","customTitle":"Invoice roles"}`+"\n", 30*time.Minute)
	cl("-src-ledger", "k-string-002", `{"type":"summary","summary":""}`+"\n"+user(`"<command-name>/clear</command-name>"`)+user(`"tenant  bundles\nplease"`), 3*24*time.Hour)
	long := strings.Repeat("x", 100<<10)
	cl("-src-ledger", "k-array-0003", `{"type":"system","content":"`+long+`"}`+"\n"+user(`[{"type":"tool_result","content":"r"}]`)+user(`[{"type":"text","text":"grafana orgs"}]`), 5*time.Hour)
	return ps, cop, cla
}

func ids(ss []Session) string {
	var out []string
	for _, s := range ss {
		out = append(out, s.ID)
	}
	return strings.Join(out, " ")
}

func TestList(t *testing.T) {
	ps, cop, _ := fixture(t)
	old := Procs
	Procs = func() []Proc {
		return []Proc{{PID: 77, Comm: "claude", Argv: []string{"claude", "--resume", "k-title-0001"}}}
	}
	t.Cleanup(func() { Procs = old })

	ss, err := List(ps, Opts{})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(ss); got != "c-named-0001 k-title-0001 c-prompt-0002 k-array-0003 k-string-002" {
		t.Fatalf("order/filter: %s", got)
	}
	by := map[string]Session{}
	for _, s := range ss {
		by[s.ID] = s
	}
	want := map[string][2]string{ // title, profile
		"c-named-0001":  {"Portal nav sidebar", "copilot"},
		"c-prompt-0002": {"fix the worker retry", "claude-copilot"},
		"k-title-0001":  {"Invoice roles", "claude"},
		"k-string-002":  {"tenant bundles please", "claude"},
		"k-array-0003":  {"grafana orgs", "claude"},
	}
	for id, w := range want {
		if s := by[id]; s.Title != w[0] || s.Profile != w[1] {
			t.Errorf("%s: title %q profile %q, want %v", id, s.Title, s.Profile, w)
		}
	}
	if s := by["c-named-0001"]; s.Dir != "/src/portal" || s.Branch != "feat/nav" || s.Model != "gpt-5.2" || s.Store != cop+"/{id}" {
		t.Errorf("copilot fields %+v", s)
	}
	if s := by["k-title-0001"]; s.Dir != "/src/ledger" || s.Branch != "main" || s.Model != "claude-sonnet-5" || !s.InUse || s.PID != 77 {
		t.Errorf("claude fields %+v", s)
	}

	all, _ := List(ps, Opts{All: true, Board: map[string]bool{"c-named-0001": true}})
	if len(all) != 7 || all[0].ID != "c-named-0001" || !all[0].OnBoard {
		t.Errorf("all: %s", ids(all))
	}
	if hidden, _ := List(ps, Opts{Board: map[string]bool{"c-named-0001": true}}); strings.Contains(ids(hidden), "c-named-0001") {
		t.Error("on-board session not hidden")
	}
	if got, _ := List(ps, Opts{Words: []string{"PORTAL", "nav"}}); ids(got) != "c-named-0001" {
		t.Errorf("words: %s", ids(got))
	}
	if got, _ := List(ps, Opts{Words: []string{"ledger"}, Limit: 2}); ids(got) != "k-title-0001 k-array-0003" {
		t.Errorf("limit: %s", ids(got))
	}
	if got, _ := List(ps, Opts{Words: []string{"ledger"}, SkipBusy: true}); strings.Contains(ids(got), "k-title-0001") {
		t.Errorf("busy kept: %s", ids(got))
	}
	if got, _ := List(ps, Opts{Store: cop + "/{id}"}); ids(got) != "c-named-0001 c-prompt-0002" {
		t.Errorf("store: %s", ids(got))
	}
}

func TestLookup(t *testing.T) {
	ps, _, _ := fixture(t)
	if got := Lookup(ps, "k-title-0001", ""); len(got) != 1 || got[0].Title != "Invoice roles" {
		t.Errorf("exact %+v", got)
	}
	if got := Lookup(ps, "c-prompt", ""); len(got) != 1 || got[0].ID != "c-prompt-0002" {
		t.Errorf("prefix %+v", got)
	}
	if got := Lookup(ps, "k-", ""); len(got) != 0 {
		t.Errorf("short prefix %+v", got)
	}
	if got := Lookup(ps, "k-title-0001", "/nope/{id}"); len(got) != 0 {
		t.Errorf("store filter %+v", got)
	}
}

func TestHolder(t *testing.T) {
	d := t.TempDir()
	os.WriteFile(filepath.Join(d, "inuse.999999999.lock"), nil, 0o644) // dead pid
	if pid, _ := Holder("abc", []string{d}, nil); pid != 0 {
		t.Errorf("dead lock pid %d", pid)
	}
	os.WriteFile(filepath.Join(d, "inuse."+strconv.Itoa(os.Getpid())+".lock"), nil, 0o644)
	if pid, _ := Holder("abc", []string{d}, nil); pid != os.Getpid() {
		t.Errorf("live lock pid %d", pid)
	}
	id := "abcd1234-0000"
	not := []Proc{
		{PID: 1, Comm: "x", Argv: []string{"x"}},
		{PID: 3, Comm: "tmux: server", Argv: []string{"tmux", "new-session", "-d", "claude", "--session-id", id}},
		{PID: 4, Comm: "less", Argv: []string{"less", id + ".jsonl"}},
		{PID: 5, Comm: "bash", Argv: []string{"bash", "-c", "claude --resume " + id}},
	}
	if pid, cmd := Holder(id, nil, not); pid != 0 {
		t.Errorf("false holder %d %s", pid, cmd)
	}
	hit := append(not, Proc{PID: 2, Comm: "claude", Argv: []string{"claude", "--resume", id}})
	if pid, cmd := Holder(id, nil, hit); pid != 2 || cmd != "claude --resume "+id {
		t.Errorf("proc %d %s", pid, cmd)
	}
	if pid, _ := Holder(id, nil, []Proc{{PID: 6, Comm: "copilot", Argv: []string{"copilot", "--resume=" + id}}}); pid != 6 {
		t.Errorf("--flag=id pid %d", pid)
	}
}

// Dot entries, files in a folder store and non-ID names are not sessions.
func TestCandidatesSkipJunk(t *testing.T) {
	root := t.TempDir()
	ps := map[string]tools.Profile{"copilot": {Name: "copilot", SessionPath: root + "/{id}"}}
	for _, d := range []string{".session-operation-locks", "short", "good-session-01"} {
		os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	os.WriteFile(filepath.Join(root, "file-not-dir-01"), nil, 0o644)
	ss, _ := List(ps, Opts{All: true})
	if got := ids(ss); got != "good-session-01" {
		t.Errorf("got %q", got)
	}
}

func TestAncestors(t *testing.T) {
	pp := map[int]int{500: 400, 400: 1, 1: 0, 7: 7}
	got := Ancestors(500, func(p int) int { return pp[p] })
	if len(got) != 3 || !got[500] || !got[400] || !got[1] {
		t.Errorf("got %v", got)
	}
	if loop := Ancestors(7, func(p int) int { return pp[p] }); len(loop) != 1 {
		t.Errorf("self-parent loop %v", loop)
	}
	if !Ancestors(os.Getpid(), ProcPPid)[os.Getppid()] {
		t.Error("real parent not found")
	}
}

func TestAgeSuggest(t *testing.T) {
	for d, w := range map[time.Duration]string{5 * time.Minute: "5m", 3 * time.Hour: "3h", 50 * time.Hour: "2d"} {
		if Age(d) != w {
			t.Errorf("Age(%v)=%s", d, Age(d))
		}
	}
	ps := []tools.Profile{{Name: "a", Args: []string{"--x"}}, {Name: "b", Args: []string{"--model=gpt-5"}}}
	if suggest(ps, "gpt-4.1") != "b" || suggest(ps, "claude-x") != "a" {
		t.Error("suggest")
	}
	if OneLine("a\n b   c", 4) != "a b…" {
		t.Errorf("oneline %q", OneLine("a\n b   c", 4))
	}
}

func TestCopilotCwd(t *testing.T) {
	for _, tc := range []struct{ yaml, want string }{
		{"id: x\ncwd: /a/b\nbranch: main\n", "/a/b"},
		{"cwd: \"/a b/c\"\n", "/a b/c"},
		{"cwd:/x\n", "/x"},
		{"summary: |\n  cwd: /not\n", ""},
	} {
		d := t.TempDir()
		if err := os.WriteFile(filepath.Join(d, "workspace.yaml"), []byte(tc.yaml), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := CopilotCwd(d); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.yaml, got, tc.want)
		}
	}
	if got := CopilotCwd(t.TempDir()); got != "" {
		t.Errorf("missing file: got %q", got)
	}
}
