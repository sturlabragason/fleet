package tmux

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestShellArgvUnwrapsShellCommand(t *testing.T) {
	got, err := shellArgv(`bash -lc 'exec '\''C:\fleet\fleet.exe'\'' acp-host a1 --dir '\''D:\a b'\'' -- x "y z"'`)
	want := []string{`C:\fleet\fleet.exe`, "acp-host", "a1", "--dir", `D:\a b`, "--", "x", "y z"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, %v want %q", got, err, want)
	}
}

func TestHeadlessPaneLifecycle(t *testing.T) {
	old := Headless
	Headless = true
	t.Cleanup(func() { Headless = old })
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	c := &Client{Socket: "fleet-test-headless"}
	if c.Running() {
		t.Fatal("running before any session")
	}
	pane, err := c.Spawn("fleet", "ag-a1", t.TempDir(), []string{"FOO=bar"}, "bash -lc 'exec sleep 30'")
	if err != nil || pane != "%1" {
		t.Fatalf("spawn: %q, %v", pane, err)
	}
	if !c.Running() || !c.HasSession("fleet") {
		t.Fatal("no session after spawn")
	}
	c.SetPaneOption(pane, "@fleet_agent", "a1")
	if v := c.PaneOption(pane, "@fleet_agent"); v != "a1" {
		t.Errorf("option: %q", v)
	}
	ps, err := c.ListPanes("")
	if err != nil || len(ps) != 1 || ps[0].Agent != "a1" || ps[0].Dead || ps[0].Window != "ag-a1" || ps[0].Cmd != "sleep" {
		t.Fatalf("list: %+v, %v", ps, err)
	}
	if _, err := c.Run("send-keys", "-t", pane, "x"); err == nil {
		t.Error("send-keys did not fail headless")
	}
	if err := c.KillPane(pane); err != nil {
		t.Fatal(err)
	}
	if ps, _ := c.ListPanes(""); len(ps) != 0 {
		t.Errorf("pane left after kill: %+v", ps)
	}
	time.Sleep(100 * time.Millisecond) // the killed sleep is reaped by init
	if out, _ := c.Run("display-message", "-p", "#{socket_path}"); !strings.Contains(out, "fleet-headless") {
		t.Errorf("socket path %q", out)
	}
}
