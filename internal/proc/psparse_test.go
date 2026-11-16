package proc

import (
	"testing"
	"time"
)

// Lines as macOS 14's ps prints psColumns under LC_ALL=C.
const psSample = `    1     0 Ss   ??        19328  16:13.49 Fri Nov  6 10:04:28 2026     /sbin/launchd
  278  1171 Z    ??            0   0:00.00 Mon Nov 16 07:55:48 2026     <defunct>
  287 77048 S    ??        13040   0:00.49 Mon Nov 16 07:55:51 2026     /Applications/Claude.app/Contents/Frameworks/Claude Helper.app/Contents/MacOS/Claude Helper
 4242  4100 Ss+  ttys003    2048 759:59.38 Mon Nov 16 09:01:02 2026     -zsh
garbage line
`

func TestParsePS(t *testing.T) {
	rows := parsePS(psSample)
	if len(rows) != 4 {
		t.Fatalf("parsed %d rows, want 4: %+v", len(rows), rows)
	}
	tests := []struct {
		row        psRow
		pid, ppid  int
		comm, path string
		rss, ticks uint64
		daemon     bool
		start      string
	}{
		{rows[0], 1, 0, "launchd", "/sbin/launchd", 19328 * 1024, 97349, true, "2026-11-06 10:04:28"},
		{rows[1], 278, 1171, "<defunct>", "<defunct>", 0, 0, false, "2026-11-16 07:55:48"},
		{rows[2], 287, 77048, "Claude Helper", "/Applications/Claude.app/Contents/Frameworks/Claude Helper.app/Contents/MacOS/Claude Helper", 13040 * 1024, 49, false, "2026-11-16 07:55:51"},
		{rows[3], 4242, 4100, "zsh", "-zsh", 2048 * 1024, 4559938, false, "2026-11-16 09:01:02"},
	}
	for _, tc := range tests {
		r := tc.row
		if r.PID != tc.pid || r.PPID != tc.ppid || r.Comm() != tc.comm || r.Path != tc.path || r.RSS != tc.rss ||
			r.Ticks != tc.ticks || r.Daemon() != tc.daemon || r.Start.Format(time.DateTime) != tc.start {
			t.Errorf("row %d: %+v comm %q daemon %v", tc.pid, r, r.Comm(), r.Daemon())
		}
	}
}

func TestParseCPUTime(t *testing.T) {
	for s, want := range map[string]uint64{"0:00.00": 0, "0:00.49": 49, "16:13.49": 97349, "1:02:03.4": 372340, "7": 700} {
		if got, ok := parseCPUTime(s); !ok || got != want {
			t.Errorf("parseCPUTime(%q) = %d, %v; want %d", s, got, ok, want)
		}
	}
	if _, ok := parseCPUTime("x:00.00"); ok {
		t.Error("garbage parsed")
	}
}
