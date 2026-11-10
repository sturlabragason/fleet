package mcp

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func resultText(r map[string]any) string {
	return r["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
}

func inboxCall(id int) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"fleet_inbox","arguments":{}}}`+"\n", id)
}

func initCall(id int) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"initialize","params":{}}`+"\n", id)
}

func setAgentInstructions(t *testing.T, text string) {
	t.Helper()
	old := AgentInstructions
	AgentInstructions = text
	t.Cleanup(func() { AgentInstructions = old })
}

// The hint appears once per change of the served text, never while it is
// unchanged, and fleet_instructions returns the current text (ADR-102).
func TestInstructionsChangedHint(t *testing.T) {
	setAgentInstructions(t, "rules v1")
	s, _ := reloadServer(t)
	c, _ := startServe(t, s)
	c.send(initCall(1))
	c.next()
	c.send(inboxCall(2))
	if txt := resultText(c.next()); strings.Contains(txt, instructionsChangedNote) {
		t.Fatalf("hint while unchanged: %q", txt)
	}
	AgentInstructions = "rules v2"
	c.send(inboxCall(3))
	if txt := resultText(c.next()); !strings.HasSuffix(txt, instructionsChangedNote) {
		t.Fatalf("no hint after change: %q", txt)
	}
	c.send(inboxCall(4))
	if txt := resultText(c.next()); strings.Contains(txt, instructionsChangedNote) {
		t.Fatalf("hint repeated: %q", txt)
	}
	c.send(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"fleet_instructions","arguments":{}}}` + "\n")
	txt := resultText(c.next())
	if !strings.Contains(txt, "rules v2") || !strings.Contains(txt, instructionsHash("rules v2")) {
		t.Fatalf("fleet_instructions: %q", txt)
	}
	if s.served != instructionsHash("rules v2") {
		t.Fatal("served hash not updated, so a later reload would repeat the hint")
	}
	AgentInstructions = "rules v3"
	c.send(inboxCall(6))
	if txt := resultText(c.next()); !strings.HasSuffix(txt, instructionsChangedNote) {
		t.Fatalf("no hint after a second change: %q", txt)
	}
}

// The hash served at initialize survives the re-exec: the new process
// compares against it, not against its own text (ADR-102).
func TestInstructionsHashSurvivesReload(t *testing.T) {
	setAgentInstructions(t, "rules v1")
	s, exe := reloadServer(t)
	execs := make(chan []string, 1)
	s.Probe = func(string) error { return nil }
	s.Exec = func(_ string, _, env []string) error { execs <- env; return nil }
	c, done := startServe(t, s)
	c.send(initCall(1))
	c.next()
	replaceExe(t, exe, "v2")
	c.send(ping(2))
	env := <-execs
	<-done
	want := envServed + "=" + instructionsHash("rules v1")
	found := false
	for _, e := range env {
		found = found || e == want
	}
	if !found {
		t.Fatalf("exec env lacks %s", want)
	}

	// The new binary serves different text and never saw initialize.
	AgentInstructions = "rules v2"
	t.Setenv(envServed, instructionsHash("rules v1"))
	s2, _ := reloadServer(t)
	s2.Reloaded = true
	c2, _ := startServe(t, s2)
	c2.next() // tools/list_changed
	c2.send(inboxCall(3))
	if txt := resultText(c2.next()); !strings.Contains(txt, instructionsChangedNote) {
		t.Fatalf("no hint after reload: %q", txt)
	}
	if os.Getenv(envServed) != "" {
		t.Fatal("served hash left in the environment")
	}
}
