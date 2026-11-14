package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/team"
)

// fleet ls --json carries the board's subagents, with and without teams, and
// stays a bare array with the schema number on each agent (ADR-126).
func TestLsJSONSubagents(t *testing.T) {
	now := time.Now()
	as := []*board.Agent{
		{Name: "a1", Profile: "copilot", Status: "working", StatusSince: now, Subagents: []status.Subagent{{ID: "t1", Name: "fleet-builder", State: status.SubRunning, Started: now}}},
		{Name: "a2", Profile: "fake", Status: "idle", StatusSince: now},
	}
	for _, teams := range []map[string]team.Info{nil, {"a1": {Team: "alpha", Color: "#89b4fa"}}} {
		var js bytes.Buffer
		writeLs(&js, as, teams, true)
		if strings.Count(js.String(), `"subagents": [`) != 1 || !strings.Contains(js.String(), `"name": "fleet-builder"`) || !strings.Contains(js.String(), `"state": "running"`) {
			t.Errorf("json:\n%s", js.String())
		}
		var got []map[string]any
		if err := json.Unmarshal(js.Bytes(), &got); err != nil || len(got) != 2 {
			t.Fatalf("not an array of two: %v\n%s", err, js.String())
		}
		for _, a := range got {
			if a["schema"] != float64(board.Schema) {
				t.Errorf("%v: schema %v", a["name"], a["schema"])
			}
		}
	}
}
