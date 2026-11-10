package main

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/acphost"
)

func TestACPHostKeepsPromptFileWhenStartFails(t *testing.T) {
	home, err := os.MkdirTemp("", "ah") // short: unix socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	t.Setenv("FLEET_HOME", home)
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "none.toml"))
	pf := filepath.Join(home, "p.prompt")
	if err := os.WriteFile(pf, []byte("task"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cmdACPHost([]string{"a1", "--prompt-file", pf, "--", filepath.Join(home, "no-such-agent")}); err == nil {
		t.Fatal("acp-host with a missing agent should fail")
	}
	if _, err := os.Stat(pf); err != nil {
		t.Fatalf("prompt file lost on a failed start: %v", err)
	}
}

// A second acp-host for an agent whose host still answers must refuse
// without consuming the prompt file meant for the running one.
func TestACPHostDuplicateKeepsPromptFile(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "fh") // short: unix socket paths are capped near 108 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("FLEET_HOME", home)
	t.Setenv("FLEET_CONFIG", filepath.Join(home, "none.toml"))
	sock := acphost.SocketPath(home, "a1")
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = bufio.NewReader(c).ReadBytes('\n')
			_, _ = c.Write([]byte(`{"ok":true,"status":"idle"}` + "\n"))
			c.Close()
		}
	}()
	pf := filepath.Join(home, "a1.prompt")
	if err := os.WriteFile(pf, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = cmdACPHost([]string{"a1", "--prompt-file", pf, "--", "true"})
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("err = %v, want already running", err)
	}
	if _, err := os.Stat(pf); err != nil {
		t.Fatalf("prompt file consumed: %v", err)
	}
}
