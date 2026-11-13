package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fleet_route set saves a route and logs the change; the brief lists it by
// name and trigger; fleet_spawn route=NAME fills what the call leaves out
// and explicit arguments win (ADR-120).
func TestRouteSetBriefAndSpawn(t *testing.T) {
	f, s := spawnHome(t, "")
	set := map[string]any{"action": "set", "name": "inbox", "fields": map[string]any{
		"triggers": []any{"check my mail", "draft replies"}, "dir": f.Home, "profile": "m", "effort": "light",
		"may": []any{"merge-local"}, "interactive": true, "skill": "inbox", "prompt": "Draft only.\n\n{task}",
	}}
	if msg, isErr := callText(t, s, "fleet_route", set); isErr || !strings.HasPrefix(msg, "saved route inbox: ") {
		t.Fatalf("set: %q", msg)
	}
	msg, isErr := callText(t, s, "fleet_route", map[string]any{"action": "set", "name": "inbox",
		"fields": map[string]any{"effort": "standard"}, "reason": "user: inbox work needs standard"})
	if isErr || msg != "saved route inbox: effort light → standard (user: inbox work needs standard)" {
		t.Fatalf("correction: %q", msg)
	}
	log, _ := os.ReadFile(filepath.Join(f.Home, "decisions.log"))
	if !strings.Contains(string(log), "route inbox: effort light → standard (user: inbox work needs standard)\n") {
		t.Fatalf("decisions.log:\n%s", log)
	}
	if msg, isErr := callText(t, s, "fleet_route", map[string]any{"action": "set", "name": "inbox", "fields": map[string]any{"effort": "max"}}); !isErr {
		t.Fatalf("bad effort accepted: %q", msg)
	}

	br, _ := callText(t, s, "fleet_brief", nil)
	if !strings.Contains(br, "## Routes") || !strings.Contains(br, "- inbox: check my mail, draft replies\n") || strings.Contains(br, "Draft only") {
		t.Fatalf("brief:\n%s", br)
	}
	if out, _ := callText(t, s, "fleet_route", map[string]any{"action": "show", "name": "inbox"}); !strings.Contains(out, "learned_from: user: inbox work needs standard") {
		t.Fatalf("show: %q", out)
	}

	msg, isErr = callText(t, s, "fleet_spawn", map[string]any{"name": "x", "route": "inbox", "prompt": "answer Bo", "may": []any{"push-main"}})
	if isErr {
		t.Fatal(msg)
	}
	a := f.Store.Load().Agents["x"]
	if a == nil || a.Profile != "m" || a.Dir != f.Home || a.Effort != "standard" || !a.Interactive {
		t.Fatalf("agent %+v", a)
	}
	if a.Goal != "Use the inbox skill for this task.\n\nDraft only.\n\nanswer Bo" {
		t.Errorf("prompt %q", a.Goal)
	}
	if a.Card == nil || len(a.Card.May) != 1 || a.Card.May[0] != "push-main" {
		t.Errorf("explicit may must win: %+v", a.Card)
	}

	if msg, isErr := callText(t, s, "fleet_spawn", map[string]any{"name": "z", "route": "inbox", "prompt": "p", "interactive": false}); isErr {
		t.Fatal(msg)
	}
	if f.Store.Load().Agents["z"].Interactive {
		t.Error("explicit interactive=false must win over the route")
	}
	if msg, isErr := callText(t, s, "fleet_spawn", map[string]any{"name": "y", "route": "nope"}); !isErr || !strings.Contains(msg, "routes: inbox") {
		t.Fatalf("unknown route: %q", msg)
	}
	if msg, _ := callText(t, s, "fleet_route", map[string]any{"action": "rm", "name": "inbox"}); msg != "removed route inbox" {
		t.Fatalf("rm: %q", msg)
	}
	if br, _ := callText(t, s, "fleet_brief", nil); strings.Contains(br, "## Routes") {
		t.Fatal("empty routes still in the brief")
	}
}
