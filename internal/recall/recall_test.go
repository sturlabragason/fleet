package recall

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/testenv"
)

// The fixtures are written in Copenhagen time (+01:00 in November); pin
// time.Local so local clock times print the same on any CI host.
func TestMain(m *testing.M) {
	time.Local = time.FixedZone("CET", 3600)
	os.Exit(testenv.Main(m))
}

// fleetHome writes an inbox with two reports and a decisions.log.
func fleetHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	box := filepath.Join(home, "mail", "admiral")
	os.MkdirAll(box, 0o755)
	inbox := `{"id":"1","from":"survey","to":"admiral","kind":"report","text":"done: memory survey published\nverdict: integrate cass","t":"2026-11-12T21:42:00+01:00"}
{"id":"2","from":"other","to":"admiral","kind":"report","text":"question: which palette?","t":"2026-11-13T08:00:00+01:00"}
{"id":"3","from":"x","to":"admiral","kind":"request","text":"access cass please","t":"2026-11-13T08:00:00+01:00"}
`
	os.WriteFile(filepath.Join(box, "inbox.jsonl"), []byte(inbox), 0o644)
	// The same report in a second box is listed once.
	os.MkdirAll(filepath.Join(home, "mail", "lead"), 0o755)
	os.WriteFile(filepath.Join(home, "mail", "lead", "inbox.jsonl"), []byte(strings.SplitAfter(inbox, "\n")[0]), 0o644)
	os.WriteFile(filepath.Join(home, "decisions.log"), []byte("2026-11-12T21:38 [admiral] decision: hold the build until the memory survey reports\nnot a note line\n"), 0o644)
	return home
}

// fakeCass installs a cass that answers search, status and sessions from
// files, and returns the dir with a Copilot-style session in it.
func fakeCass(t *testing.T, search, sessions string) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "search.json"), []byte(search), 0o644)
	os.WriteFile(filepath.Join(dir, "sessions.json"), []byte(sessions), 0o644)
	script := "#!/bin/sh\ncase \"$1\" in\nsearch) cat " + dir + "/search.json;;\nstatus) echo '{\"index\":{\"age_seconds\":120}}';;\nsessions) cat " + dir + "/sessions.json;;\nesac\n"
	os.WriteFile(filepath.Join(dir, "cass"), []byte(script), 0o755)
	t.Setenv("FLEET_CASS", filepath.Join(dir, "cass"))
	return dir
}

func TestSearchWithoutCass(t *testing.T) {
	t.Setenv("FLEET_CASS", "off")
	r := Search(context.Background(), "Memory Survey", Opts{Home: fleetHome(t)})
	if len(r.Fleet) != 2 || r.Fleet[0].Kind != "report" || r.Fleet[0].Who != "survey" || r.Fleet[1].Who != "admiral" {
		t.Fatalf("fleet hits: %+v", r.Fleet)
	}
	if out := r.Format(); !strings.Contains(out, "cass is not installed") || !strings.Contains(out, "memory survey published") {
		t.Fatalf("format:\n%s", out)
	}
	// A matching detail line is shown under the headline.
	r = Search(context.Background(), "verdict", Opts{Home: fleetHome(t)})
	if len(r.Fleet) != 1 || r.Fleet[0].Text != "verdict: integrate cass" {
		t.Fatalf("detail hit: %+v", r.Fleet)
	}
}

func TestSearchJoinsCassSessions(t *testing.T) {
	dir := fakeCass(t, "", "")
	sess := filepath.Join(dir, "4dd41a22-7811", "events.jsonl")
	os.MkdirAll(filepath.Dir(sess), 0o755)
	os.WriteFile(filepath.Join(filepath.Dir(sess), "workspace.yaml"), []byte("id: x\ncwd: /src/ai-fleet.wt/memory-survey\n"), 0o644)
	search := `{"hits":[
{"title":"# Fleet worker playbook (standard)","snippet":"Claude deletes transcripts after 30 days","source_path":"` + sess + `","agent":"copilot_cli","workspace":"","created_at":1794516012023},
{"title":"# Fleet worker playbook","snippet":"second hit, same session","source_path":"` + sess + `","agent":"copilot_cli","workspace":"","created_at":1794516012024},
{"title":"Deploy a VM","snippet":"cass search","source_path":"/c/abc.jsonl","agent":"claude_code","workspace":"/src/infra","created_at":"2026-11-01T10:00:00Z"}]}`
	os.WriteFile(filepath.Join(dir, "search.json"), []byte(search), 0o644)

	r := Search(context.Background(), "transcripts", Opts{Home: t.TempDir(), Agents: map[string]string{"abc": "infra-agent"}})
	if r.CassErr != "" || len(r.Sessions) != 2 {
		t.Fatalf("sessions: %+v err %q", r.Sessions, r.CassErr)
	}
	a, b := r.Sessions[0], r.Sessions[1]
	if a.Who != "memory-survey" || a.Dir != "/src/ai-fleet.wt/memory-survey" || a.Title != "" || a.ID != "4dd41a22-7811" || a.At.IsZero() {
		t.Errorf("copilot hit: %+v", a)
	}
	if b.Who != "infra-agent" || b.Title != "Deploy a VM" || b.At.Year() != 2026 {
		t.Errorf("claude hit: %+v", b)
	}
	out := r.Format()
	for _, want := range []string{"memory-survey · ai-fleet · copilot_cli · 4dd41a22", "index 2m0s old", "fleet adopt"} {
		if !strings.Contains(out, want) {
			t.Errorf("format lacks %q:\n%s", want, out)
		}
	}
}

func TestDay(t *testing.T) {
	now := time.Date(2026, 11, 13, 9, 0, 0, 0, time.Local)
	sessions := `{"sessions":[
{"path":"/s/1.jsonl","workspace":"/src/garden","agent":"claude_code","title":"Plan the garden beds","modified":"2026-11-13T07:30:00Z","message_count":12},
{"path":"/s/2.jsonl","workspace":"/src/ai-fleet.wt/survey","agent":"copilot_cli","title":"# Fleet worker playbook","modified":"2026-11-13T07:30:00Z","message_count":40},
{"path":"/s/3.jsonl","workspace":"/src/x","agent":"copilot_cli","title":"Reply with ok","modified":"2026-11-13T07:30:00Z","message_count":2},
{"path":"/s/4.jsonl","workspace":"/tmp/probe","agent":"copilot_cli","title":"Run date","modified":"2026-11-13T07:30:00Z","message_count":9}]}`
	fakeCass(t, "", sessions)
	home := fleetHome(t)
	out := Day(context.Background(), DayOpts{
		Home: home, Since: now.Add(-24 * time.Hour), Now: now,
		Agents: []Agent{{Name: "fp", Status: "waiting", Dir: "/src/shop"}, {Name: "w", Status: "working", Dir: "/src/shop"}},
		Unread: []Msg{{From: "other", Text: "question: which palette?", T: now.Add(-time.Hour)}},
	})
	waiting, rest, _ := strings.Cut(out, "## Done since")
	for _, want := range []string{"08:00 other: question: which palette?", "fp is waiting (shop)", "1 unread reports"} {
		if !strings.Contains(waiting, want) {
			t.Errorf("waiting lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(waiting, "w is") {
		t.Errorf("a working agent waits on no one:\n%s", out)
	}
	// The survey's dir comes from its cass session.
	if !strings.Contains(rest, "ai-fleet (1)\n  Thu 21:42 survey: memory survey published") {
		t.Errorf("done section:\n%s", out)
	}
	_, outside, _ := strings.Cut(out, "## Sessions outside fleet")
	if !strings.Contains(outside, "garden (1)") || strings.Contains(outside, "Reply with ok") || strings.Contains(outside, "Run date") || strings.Contains(outside, "playbook") {
		t.Errorf("outside fleet:\n%s", outside)
	}
}

func TestDayWithoutCass(t *testing.T) {
	t.Setenv("FLEET_CASS", "off")
	out := Day(context.Background(), DayOpts{Home: fleetHome(t), Since: time.Now().Add(-time.Hour)})
	if !strings.Contains(out, "## Waiting on you\nnothing") || !strings.Contains(out, CassHint) {
		t.Errorf("digest:\n%s", out)
	}
}
