//go:build extexample

package main

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sturlabragason/fleet/internal/fleet"
)

func TestExampleExtCommand(t *testing.T) {
	if err := run([]string{"example", "a", "b"}); err != nil {
		t.Fatal(err)
	}
	example.mu.Lock()
	defer example.mu.Unlock()
	if !slices.Equal(example.args, []string{"a", "b"}) {
		t.Fatalf("args %v", example.args)
	}
}

func TestExampleExtUsage(t *testing.T) {
	if !strings.Contains(usage, exampleUsage+"  fleet doctor [--json]") {
		t.Fatal("usage lacks the example line before fleet doctor")
	}
}

func TestExampleExtBefore(t *testing.T) {
	home := t.TempDir()
	if err := extBefore(home, "spawn"); err != nil {
		t.Fatal(err)
	}
	exampleRefuse(home)
	if err := extBefore(home, "spawn"); err == nil || !strings.Contains(err.Error(), "refused spawn") {
		t.Fatalf("err %v", err)
	}
}

func TestExampleExtOpen(t *testing.T) {
	f := &fleet.Fleet{Home: t.TempDir()}
	runExtOpen(f)
	example.mu.Lock()
	defer example.mu.Unlock()
	if !slices.Contains(example.opened, f) {
		t.Fatal("Open did not run")
	}
}

func TestExampleExtMCP(t *testing.T) {
	f := &fleet.Fleet{Home: t.TempDir()}
	if err := extMCP(f, "main", ""); err != nil {
		t.Fatal(err)
	}
	exampleRefuse(f.Home)
	if err := extMCP(f, "main", "a1"); err == nil || !strings.Contains(err.Error(), "main/a1") {
		t.Fatalf("err %v", err)
	}
}

func TestExampleExtUI(t *testing.T) {
	f := &fleet.Fleet{Home: t.TempDir()}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	runExtUI(ctx, f)
	for i := 0; i < 100; i++ {
		example.mu.Lock()
		ok := slices.Contains(example.ui, f)
		example.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("UI did not run")
}
