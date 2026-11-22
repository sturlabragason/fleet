package main

import (
	"path/filepath"
	"testing"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/prefs"
)

func TestNewUpdater(t *testing.T) {
	t.Setenv("FLEET_CONFIG", filepath.Join(t.TempDir(), "no-config.toml"))
	f, err := fleet.New(nil, t.TempDir(), "/tmp/fleet")
	if err != nil {
		t.Fatal(err)
	}

	uWait := newUpdater(f, prefs.Update{Auto: true, WaitQuiet: true, BuildCommand: "make build"})
	if uWait.Quiet == nil {
		t.Fatal("WaitQuiet=true must wire Quiet")
	}
	if uWait.BuildCommand != "make build" {
		t.Fatalf("BuildCommand = %q", uWait.BuildCommand)
	}

	uNow := newUpdater(f, prefs.Update{Auto: true, WaitQuiet: false})
	if uNow.Quiet != nil {
		t.Fatal("WaitQuiet=false must leave Quiet nil")
	}
}
