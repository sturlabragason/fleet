//go:build extexample

package mcp

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestExampleExtTool(t *testing.T) {
	s, _, _ := memServer(t)
	if !slices.Contains(ToolNames(), "fleet_example") {
		t.Fatal("ToolNames lacks the extension's tool")
	}
	if out, isErr := callText(t, s, "fleet_example", map[string]any{"name": "hi"}); isErr || !strings.HasPrefix(out, "example: hi") {
		t.Fatalf("fleet_example: %q %v", out, isErr)
	}
	if !identityBound["fleet_example"] || CLIParity["example"] != "fleet_example" {
		t.Fatal("identity-bound or parity entry missing")
	}
	if !strings.Contains(Instructions, exampleRow) {
		t.Fatal("instructions lack the tool's row")
	}
}

func TestExampleExtGate(t *testing.T) {
	s, _, _ := memServer(t)
	if _, isErr := callText(t, s, "fleet_example", map[string]any{"name": "example-refused"}); isErr {
		t.Fatal("gate refused on a server it is off for")
	}
	exampleOn(s)
	if out, isErr := callText(t, s, "fleet_example", map[string]any{"name": "example-refused"}); !isErr || !strings.HasPrefix(out, "example: refused") {
		t.Fatalf("gate: %q %v", out, isErr)
	}
}

func TestExampleExtOnCall(t *testing.T) {
	s, _, _ := memServer(t)
	st := exampleOn(s)
	callText(t, s, "fleet_profiles", nil)
	for i := 0; i < 100 && st.calls.Load() == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if st.calls.Load() != 1 {
		t.Fatalf("OnCall ran %d times", st.calls.Load())
	}
}

func TestExampleExtSuffix(t *testing.T) {
	s, _, _ := memServer(t)
	if out, _ := callText(t, s, "fleet_profiles", nil); strings.Contains(out, "[example suffix]") {
		t.Fatal("suffix on a server it is off for")
	}
	exampleOn(s)
	if out, _ := callText(t, s, "fleet_profiles", nil); !strings.HasSuffix(out, "\n[example suffix]") {
		t.Fatalf("no suffix: %q", out)
	}
}

func TestExampleExtBrief(t *testing.T) {
	s, _, _ := memServer(t)
	exampleOn(s)
	if out, _ := callText(t, s, "fleet_brief", nil); !strings.Contains(out, "## Example\nexample brief") {
		t.Fatalf("brief: %q", out)
	}
}

func TestExampleExtAfter(t *testing.T) {
	s, _, _ := memServer(t)
	if out, _ := callText(t, s, "fleet_example", map[string]any{"name": "after"}); strings.Contains(out, "[example after") {
		t.Fatal("after note on a server it is off for")
	}
	exampleOn(s)
	if out, _ := callText(t, s, "fleet_example", map[string]any{"name": "after"}); !strings.HasPrefix(out, "example: after\n[example after: example: after]\n[example suffix]") {
		t.Fatalf("after: %q", out)
	}
	if out, _ := callText(t, s, "fleet_example", map[string]any{"name": "example-refused"}); strings.Contains(out, "[example after") {
		t.Fatalf("after note on a refused call: %q", out)
	}
}
