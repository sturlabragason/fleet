package browser

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func stub(signal string) Detector { return func(context.Context) string { return signal } }

func TestDecide(t *testing.T) {
	never := []string{"personal", "gjald", "legal", "tax", "health"}
	cases := []struct {
		name    string
		agent   Agent
		manual  bool
		signal  string
		open    bool
		because string
	}{
		{"default allow", Agent{Name: "fleet-docs", Route: "fleet", Profile: "claude"}, false, "", true, ""},
		{"manual present blocks", Agent{Name: "fleet-docs"}, true, "", false, "presenting: fleet present on"},
		{"detected screen share blocks", Agent{Name: "fleet-docs"}, false, "screen share (gnome-shell-screencast)", false, "presenting: screen share"},
		{"detected call blocks", Agent{Name: "fleet-docs"}, false, "call (teams-for-linux)", false, "presenting: call"},
		{"never-open by name", Agent{Name: "gjald-letter"}, false, "", false, "never-open: gjald"},
		{"never-open by route", Agent{Name: "letter", Route: "homelab-personal"}, false, "", false, "never-open: personal"},
		{"never-open by profile", Agent{Name: "x", Profile: "claude-health"}, false, "", false, "never-open: health"},
		{"word start only", Agent{Name: "syntax-fix", Route: "illegal-chars"}, false, "", true, ""},
		{"presenting wins over never-open", Agent{Name: "tax-2026"}, true, "", false, "presenting:"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			if err := SetPresent(home, c.manual); err != nil {
				t.Fatal(err)
			}
			v := Decide(t.Context(), home, c.agent, never, stub(c.signal))
			if v.Open != c.open || !strings.HasPrefix(v.Reason, c.because) {
				t.Errorf("Decide = %+v, want open=%v reason %q…", v, c.open, c.because)
			}
		})
	}
}

func TestPresentOffKeepsAutoDetect(t *testing.T) {
	home := t.TempDir()
	if err := SetPresent(home, true); err != nil {
		t.Fatal(err)
	}
	if err := SetPresent(home, false); err != nil {
		t.Fatal(err)
	}
	if got := Presenting(t.Context(), home, stub("")); got != "" {
		t.Errorf("after off, no signal: %q, want not presenting", got)
	}
	if got := Presenting(t.Context(), home, stub("call (zoom)")); got != "call (zoom)" {
		t.Errorf("after off, a call: %q, want it detected", got)
	}
	if err := SetPresent(home, false); err != nil {
		t.Errorf("off twice: %v", err)
	}
}

// node builds one pw-dump node object.
func node(state, class string, props string) string {
	return `{"id":1,"type":"PipeWire:Interface:Node","info":{"state":"` + state + `","props":{"media.class":"` + class + `"` + props + `}}}`
}

func TestSignal(t *testing.T) {
	// The webcam and audio nodes are as pw-dump printed them on the
	// user's laptop with no share or call; the share and call nodes carry
	// the names GNOME, xdpw and the apps give their streams.
	webcam := node("suspended", "Video/Source", `,"node.name":"v4l2_input.pci-0000_00_14.0-usb-0_9_1.0"`)
	sink := node("running", "Audio/Sink", `,"node.name":"alsa_output.pci-0000_00_1f.3-platform-skl_hda_dsp_generic.HiFi__Speaker__sink"`)
	cases := []struct {
		name  string
		nodes []string
		want  string
	}{
		{"idle laptop", []string{webcam, sink}, ""},
		{"gnome screen share", []string{webcam, node("running", "Video/Source", `,"node.name":"gnome-shell-screencast"`)}, "screen share (gnome-shell-screencast)"},
		{"wlroots share, paused", []string{node("idle", "Video/Source", `,"node.name":"xdpw_stream"`)}, "screen share (xdpw_stream)"},
		{"teams call mic", []string{node("running", "Stream/Input/Audio", `,"application.name":"teams-for-linux","node.name":"teams-for-linux"`)}, "call (teams-for-linux)"},
		{"meet in a browser", []string{node("running", "Stream/Input/Audio", `,"application.name":"Brave","application.process.binary":"brave"`)}, "call (Brave brave)"},
		{"teams running, no call", []string{node("idle", "Stream/Input/Audio", `,"application.name":"teams-for-linux"`)}, ""},
		{"music player recording is no call", []string{node("running", "Stream/Input/Audio", `,"application.name":"audacity"`)}, ""},
		{"not json", []string{"oops"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Signal([]byte("[" + strings.Join(c.nodes, ",") + "]")); got != c.want {
				t.Errorf("Signal = %q, want %q", got, c.want)
			}
		})
	}
}

func TestStatusHandsOverAfterPresenting(t *testing.T) {
	home := t.TempDir()
	if err := Record(home, Blocked{Time: time.Now(), Agent: "a1", URL: "https://example.test/page", Reason: "presenting: call (zoom)"}); err != nil {
		t.Fatal(err)
	}
	out, err := Status(t.Context(), home, stub("call (zoom)"))
	if err != nil || strings.Contains(out, "example.test") || !strings.Contains(out, "1 blocked") {
		t.Fatalf("while presenting: %q, %v; want the count and no URL", out, err)
	}
	out, err = Status(t.Context(), home, stub(""))
	if err != nil || !strings.Contains(out, "a1 https://example.test/page") {
		t.Fatalf("after presenting: %q, %v; want the URL handed over", out, err)
	}
	if p, _ := Pending(home); len(p) != 0 {
		t.Errorf("handed-over opens still pending: %v", p)
	}
}

// An open blocked after the log was first read is still handed over.
func TestHandoverKeepsLateBlock(t *testing.T) {
	home := t.TempDir()
	if err := Record(home, Blocked{Agent: "a1", URL: "https://one.test"}); err != nil {
		t.Fatal(err)
	}
	late := func(ctx context.Context) string {
		if err := Record(home, Blocked{Agent: "a2", URL: "https://two.test"}); err != nil {
			t.Error(err)
		}
		return ""
	}
	out, err := Status(t.Context(), home, late)
	if err != nil || !strings.Contains(out, "one.test") || !strings.Contains(out, "two.test") {
		t.Fatalf("Status = %q, %v; want both blocked opens, none lost", out, err)
	}
}

func TestRealEnv(t *testing.T) {
	home := t.TempDir()
	got := RealEnv([]string{"A=1", "PATH=" + Dir(home) + ":/usr/bin", "BROWSER=x"}, home)
	if !slices.Equal(got, []string{"A=1", "PATH=/usr/bin"}) {
		t.Errorf("RealEnv = %v", got)
	}
}

func TestInstallAndEnv(t *testing.T) {
	home := t.TempDir()
	if err := Install(home, "/opt/my fleet/fleet"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(Dir(home), "xdg-open"))
	if err != nil || !strings.Contains(string(b), `exec '/opt/my fleet/fleet' browser-guard xdg-open "$@"`) {
		t.Fatalf("xdg-open shim = %q, %v", b, err)
	}
	env := Env([]string{"A=1", "PATH=/usr/bin", "BROWSER=firefox"}, home)
	want := []string{"A=1", "PATH=" + Dir(home) + ":/usr/bin", "BROWSER=" + filepath.Join(Dir(home), "xdg-open")}
	if !slices.Equal(env, want) {
		t.Errorf("Env = %v, want %v", env, want)
	}
}

func TestRealSkipsGuardAndFallsBack(t *testing.T) {
	home := t.TempDir()
	if err := Install(home, "/x/fleet"); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "xdg-open"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := Dir(home) + string(os.PathListSeparator) + bin
	argv, err := Real("xdg-open", []string{"https://a.test"}, path, home)
	if err != nil || argv[0] != filepath.Join(bin, "xdg-open") {
		t.Errorf("xdg-open → %v, %v; want the real one, not the shim", argv, err)
	}
	argv, err = Real("sensible-browser", []string{"--new-window", "https://a.test"}, path, home)
	if err != nil || !slices.Equal(argv, []string{filepath.Join(bin, "xdg-open"), "https://a.test"}) {
		t.Errorf("missing sensible-browser → %v, %v; want xdg-open with the URL", argv, err)
	}
	if _, err := Real("gio", []string{"open", "x"}, path, home); err == nil {
		t.Error("missing gio fell back to xdg-open with gio's arguments")
	}
}

func TestGuardedAndURL(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		guarded bool
		url     string
	}{
		{"xdg-open", []string{"https://a.test"}, true, "https://a.test"},
		{"gio", []string{"open", "https://a.test"}, true, "https://a.test"},
		{"gio", []string{"info", "/tmp"}, false, "info"},
		{"firefox", []string{"--new-tab", "https://a.test"}, true, "https://a.test"},
	}
	for _, c := range cases {
		if g, u := Guarded(c.name, c.args), URL(c.name, c.args); g != c.guarded || u != c.url {
			t.Errorf("%s %v: guarded %v url %q, want %v %q", c.name, c.args, g, u, c.guarded, c.url)
		}
	}
}
