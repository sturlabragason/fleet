package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/fleet"
)

func TestExtensionsCompose(t *testing.T) {
	oldE, oldC, oldU := exts, extCmds, usage
	exts, extCmds = nil, map[string]func([]string) error{}
	t.Cleanup(func() { exts, extCmds, usage = oldE, oldC, oldU })
	var opened []string
	nop := func([]string) error { return nil }
	register(extension{Name: "a", Commands: map[string]func([]string) error{"xa": nop},
		Usage:  func(u string) string { return u + "+a" },
		Before: func(_, cmd string) error { return nil },
		Open:   func(*fleet.Fleet) { opened = append(opened, "a") },
		MCP:    func(*fleet.Fleet, string, string) error { return nil }})
	register(extension{Name: "b", Commands: map[string]func([]string) error{"xb": nop},
		Usage:  func(u string) string { return u + "+b" },
		Before: func(_, cmd string) error { return errors.New("b refuses " + cmd) },
		Open:   func(*fleet.Fleet) { opened = append(opened, "b") },
		MCP:    func(*fleet.Fleet, string, string) error { return errors.New("b: no") }})
	if extCmds["xa"] == nil || extCmds["xb"] == nil || !strings.HasSuffix(usage, "+a+b") {
		t.Fatal("commands or usage not merged")
	}
	if err := extBefore("/h", "spawn"); err == nil || err.Error() != "b refuses spawn" {
		t.Fatalf("before %v", err)
	}
	runExtOpen(&fleet.Fleet{})
	if strings.Join(opened, "") != "ab" {
		t.Fatalf("opened %v", opened)
	}
	if err := extMCP(&fleet.Fleet{}, "main", ""); err == nil {
		t.Fatal("b's MCP refusal lost")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("a command registered twice did not panic")
		}
	}()
	register(extension{Name: "c", Commands: map[string]func([]string) error{"xa": nop}})
}
