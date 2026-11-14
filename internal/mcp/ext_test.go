package mcp

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
)

// withExts swaps the registry and what Register changes for one test.
func withExts(t *testing.T, es ...Extension) {
	oldE, oldT, oldD := exts, extTools, toolDefs
	oldI, oldP, oldB := Instructions, maps.Clone(CLIParity), maps.Clone(identityBound)
	exts, extTools, toolDefs = nil, map[string]int{}, slices.Clone(toolDefs)
	t.Cleanup(func() {
		exts, extTools, toolDefs = oldE, oldT, oldD
		Instructions, CLIParity, identityBound = oldI, oldP, oldB
	})
	for _, e := range es {
		Register(e)
	}
}

func TestExtensionsCompose(t *testing.T) {
	var gates []string
	call := func(s *Server, name string, a args) (string, error) { return name, nil }
	withExts(t,
		Extension{Name: "a", Tools: []tool{{Name: "fleet_xa"}}, Call: call,
			Gate:   func(s *Server, n string, a args) error { gates = append(gates, "a"); return nil },
			Suffix: func(*Server) string { return "[a]" }, Brief: func(*Server) string { return "A" },
			Parity: map[string]string{"xa": "fleet_xa"}, IdentityBound: []string{"fleet_xa"},
			Instructions: func(s string) string { return s + "+a" }},
		Extension{Name: "b", Tools: []tool{{Name: "fleet_xb"}}, Call: call,
			Gate: func(s *Server, n string, a args) error {
				gates = append(gates, "b")
				if n == "fleet_xb" {
					return errors.New("no")
				}
				return nil
			},
			Suffix: func(*Server) string { return "[b]" }, Brief: func(*Server) string { return "B" },
			Instructions: func(s string) string { return s + "+b" }},
	)
	names := ToolNames()
	if !slices.Contains(names, "fleet_xa") || !slices.Contains(names, "fleet_xb") {
		t.Fatalf("ToolNames %v lacks extension tools", names[len(names)-2:])
	}
	if out, ok, _ := extCall(nil, "fleet_xb", args{}); !ok || out != "fleet_xb" {
		t.Fatalf("extCall: %q %v", out, ok)
	}
	if _, ok, _ := extCall(nil, "fleet_status", args{}); ok {
		t.Fatal("extCall claimed a built-in tool")
	}
	if err := extGate(nil, "fleet_xb", args{}); err == nil || strings.Join(gates, "") != "ab" {
		t.Fatalf("gates %v err %v", gates, err)
	}
	if extSuffix(nil) != "[a][b]" || extBrief(nil) != "AB" {
		t.Fatalf("suffix %q brief %q", extSuffix(nil), extBrief(nil))
	}
	if !strings.HasSuffix(Instructions, "+a+b") || CLIParity["xa"] != "fleet_xa" || !identityBound["fleet_xa"] {
		t.Fatal("instructions, parity or identity not applied")
	}
	for _, e := range []Extension{
		{Name: "a"},
		{Name: "c", Tools: []tool{{Name: "fleet_status"}}, Call: call},
		{Name: "w", Tools: []tool{{Name: "fleet_wait"}}, Call: call}, // a built-in from another file
		{Name: "d", Tools: []tool{{Name: "fleet_d"}}},
		{Name: "e", Parity: map[string]string{"spawn": "fleet_spawn"}},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Register(%s) did not panic", e.Name)
				}
			}()
			Register(e)
		}()
	}
}

func TestToolNamesUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, n := range ToolNames() {
		if seen[n] {
			t.Errorf("tool %s defined twice", n)
		}
		seen[n] = true
	}
}
