package mcp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/fleet"
)

func spawnSchema(t *testing.T) map[string]any {
	t.Helper()
	for _, d := range toolDefs {
		if d.Name == "fleet_spawn" {
			props, ok := d.InputSchema["properties"].(map[string]any)
			if !ok {
				t.Fatal("fleet_spawn has no properties")
			}
			return props
		}
	}
	t.Fatal("no fleet_spawn tool")
	return nil
}

func TestSpawnSchemaHasTheTaskCard(t *testing.T) {
	props := spawnSchema(t)
	for _, k := range []string{"goal", "check", "check_exit", "result"} {
		p, ok := props[k].(map[string]any)
		if !ok {
			t.Fatalf("fleet_spawn has no %q argument", k)
		}
		if p["description"] == "" {
			t.Fatalf("%q has no description", k)
		}
	}
	if got := props["check_exit"].(map[string]any)["type"]; got != "integer" {
		t.Fatalf("check_exit type %v, want integer", got)
	}
	// The card is optional: the schema still requires only the old three.
	for _, d := range toolDefs {
		if d.Name != "fleet_spawn" {
			continue
		}
		req, _ := json.Marshal(d.InputSchema["required"])
		if string(req) != `["name","profile","dir"]` {
			t.Fatalf("fleet_spawn required %s", req)
		}
	}
}

func TestSpawnArgsBuildTheCard(t *testing.T) {
	var a args
	if err := json.Unmarshal([]byte(`{"name":"x","profile":"p","dir":"/d","goal":"ship it","check":"make test","check_exit":2}`), &a); err != nil {
		t.Fatal(err)
	}
	c := a.card()
	if c == nil || c.Goal != "ship it" || c.Check != "make test" || c.Want != 2 {
		t.Fatalf("card %+v", c)
	}
	if c.Result != fleet.DefaultResult {
		t.Fatalf("result %q, want %q", c.Result, fleet.DefaultResult)
	}
	// No card arguments: no card, so the spawn behaves as it always did.
	var b args
	json.Unmarshal([]byte(`{"name":"x","profile":"p","dir":"/d","prompt":"go"}`), &b)
	if got := b.card(); got != nil {
		t.Fatalf("card from a plain spawn: %+v", got)
	}
}

func TestTaskCardsSectionAndReportCell(t *testing.T) {
	plain := &board.Agent{Name: "plain", Report: &board.Report{State: "done", Summary: "shipped"}}
	// Without a verdict the column is the width it always was.
	long := &board.Agent{Name: "long", Report: &board.Report{State: "done", Summary: strings.Repeat("x", 100)}}
	if got := len([]rune(reportCell(long))); got != 60 {
		t.Fatalf("uncarded report cell %d wide, want 60", got)
	}
	if got := taskCards([]*board.Agent{plain}); got != "" {
		t.Fatalf("section without cards: %q", got)
	}
	if got := reportCell(plain); got != "done: shipped" {
		t.Fatalf("report cell %q", got)
	}
	carded := &board.Agent{Name: "carded", Dir: t.TempDir(),
		Card:   &board.Card{Goal: "ship it", Check: "make test", Result: "result.md", Checked: &board.Check{Code: 1, Want: 0, At: time.Now()}},
		Report: &board.Report{State: "done", Summary: "shipped"}}
	sec := taskCards([]*board.Agent{plain, carded})
	for _, want := range []string{"carded", "ship it", "fail (exit 1, want 0)", "result.md", "not written yet"} {
		if !strings.Contains(sec, want) {
			t.Fatalf("task cards missing %q:\n%s", want, sec)
		}
	}
	if strings.Contains(sec, "plain") {
		t.Fatalf("uncarded agent in the section:\n%s", sec)
	}
	if got := reportCell(carded); !strings.HasPrefix(got, "[done-check fail] ") {
		t.Fatalf("report cell %q", got)
	}
}
