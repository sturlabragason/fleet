//go:build !darwin

package proc

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeProc is one process in a fake /proc.
type fakeProc struct {
	pid, ppid       int
	comm, cmd, maps string
}

func fakeRoot(t *testing.T, ps []fakeProc) {
	t.Helper()
	d := t.TempDir()
	old := root
	root = d
	t.Cleanup(func() { root = old })
	for _, p := range ps {
		dir := filepath.Join(d, fmt.Sprint(p.pid))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		stat := fmt.Sprintf("%d (%s) S %d %s", p.pid, p.comm, p.ppid, strings.Repeat("0 ", 20))
		for name, body := range map[string]string{"stat": stat, "comm": p.comm + "\n", "cmdline": p.cmd, "maps": p.maps} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

const x11 = "7f00-7f10 r-xp 0 08:01 1 /usr/lib/libX11.so.6.4.0\n"

var desktop = []fakeProc{
	{pid: 10, ppid: 1, comm: "bash"},
	{pid: 11, ppid: 10, comm: "oosplash", cmd: "oosplash\x00/w/deck.pptx\x00"},
	{pid: 12, ppid: 11, comm: "soffice.bin", maps: x11},
	{pid: 13, ppid: 10, comm: "eog", maps: "7f00-7f10 r-xp 0 08:01 1 /usr/lib/libwayland-client.so.0\n"},
	{pid: 14, ppid: 10, comm: "chrome", cmd: "chrome\x00--headless=new\x00", maps: x11},
	{pid: 15, ppid: 10, comm: "soffice.bin", cmd: "soffice\x00--headless\x00--convert-to\x00pdf\x00"},
	{pid: 16, ppid: 10, comm: "node", maps: "7f00-7f10 r-xp 0 08:01 1 /usr/lib/libc.so.6\n"},
}

func TestGUI(t *testing.T) {
	fakeRoot(t, desktop)
	for _, tc := range []struct {
		pid  int
		want bool
	}{
		{10, false}, // a shell
		{11, true},  // LibreOffice's launcher, by name
		{12, true},
		{13, true},  // a Wayland viewer xdg-open picked
		{14, false}, // a headless browser
		{15, false}, // a headless conversion
		{16, false},
		{99, false}, // gone
	} {
		if got := GUI(tc.pid); got != tc.want {
			t.Errorf("GUI(%d) = %v, want %v", tc.pid, got, tc.want)
		}
	}
}

func TestTreeLeavesOutDesktopApps(t *testing.T) {
	fakeRoot(t, desktop)
	var got []int
	for _, p := range Tree(10) {
		got = append(got, p.PID)
	}
	if want := []int{10, 14, 15, 16}; !slices.Equal(got, want) {
		t.Errorf("Tree = %v, want %v (no GUI app or its children)", got, want)
	}
}
