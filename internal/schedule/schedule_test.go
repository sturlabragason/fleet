package schedule

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGateCheck(t *testing.T) {
	day := time.Date(2026, 11, 30, 8, 0, 0, 0, time.Local)
	week := 7 * 24 * time.Hour
	cases := []struct {
		name string
		g    Gate
		want string // prefix of the reason, "" = runs
	}{
		{"on time", Gate{Now: day, Due: day, Interval: week, Timer: true}, ""},
		{"overlap", Gate{Now: day, Timer: true, LastOpen: "dream-1123"}, "overlap"},
		{"manual overlap", Gate{Now: day, LastOpen: "dream-1123"}, "overlap"},
		{"manual at night", Gate{Now: day.Add(15 * time.Hour), Running: 9, QuotaLow: "x"}, ""},
		{"quiet", Gate{Now: day.Add(15 * time.Hour), Due: day, Interval: week, Timer: true}, "quiet"},
		{"early morning quiet", Gate{Now: day.Add(-2 * time.Hour), Timer: true}, "quiet"},
		{"catch-up in window", Gate{Now: day.Add(72 * time.Hour), Due: day, Interval: week, Timer: true}, ""},
		{"catch-up too late", Gate{Now: day.Add(96 * time.Hour), Due: day, Interval: week, Timer: true}, "missed"},
		{"cap", Gate{Now: day, Due: day, Interval: week, Timer: true, Running: MaxRunning}, "2 scheduled"},
		{"quota", Gate{Now: day, Due: day, Interval: week, Timer: true, QuotaLow: "p weekly"}, "quota low"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.g.Check()
			if (c.want == "") != (got == "") || len(got) < len(c.want) || got[:len(c.want)] != c.want {
				t.Fatalf("Check() = %q, want prefix %q", got, c.want)
			}
		})
	}
}

// Captured from systemd 258: systemd-analyze calendar --iterations=2 'Mon 08:00'.
const calendarOut = `  Original form: Mon 08:00
Normalized form: Mon *-*-* 08:00:00
    Next elapse: Mon 2026-11-30 08:00:00 CET
       (in UTC): Mon 2026-11-30 07:00:00 UTC
       From now: 2 days left
   Iteration #2: Mon 2026-12-07 08:00:00 CET
       (in UTC): Mon 2026-12-07 07:00:00 UTC
       From now: 1 week 2 days left
`

// The same on a host whose zone is UTC: no "(in UTC)" lines.
const calendarOutUTC = `  Original form: Mon 07:00
Normalized form: Mon *-*-* 07:00:00
    Next elapse: Mon 2026-11-30 07:00:00 UTC
       From now: 2 days left
   Iteration #2: Mon 2026-12-07 07:00:00 UTC
       From now: 1 week 2 days left
`

func TestParseElapses(t *testing.T) {
	want := []time.Time{time.Date(2026, 11, 30, 7, 0, 0, 0, time.UTC), time.Date(2026, 12, 7, 7, 0, 0, 0, time.UTC)}
	for name, out := range map[string]string{"local zone": calendarOut, "UTC host": calendarOutUTC} {
		got := parseElapses(out)
		if len(got) != 2 || !got[0].Equal(want[0]) || !got[1].Equal(want[1]) {
			t.Errorf("%s: parseElapses = %v, want %v", name, got, want)
		}
	}
}

func TestProblem(t *testing.T) {
	prev := time.Date(2026, 11, 23, 7, 0, 0, 0, time.UTC)
	times := Times{Prev: prev, Next: prev.Add(7 * 24 * time.Hour), Interval: 7 * 24 * time.Hour}
	set := prev.Add(-24 * time.Hour)
	now := prev.Add(3 * time.Hour)
	cases := []struct {
		name string
		s    Status
		want bool
	}{
		{"ran", Status{Timer: "active", Set: set, Times: times, Last: &Entry{At: prev, Outcome: Started}}, false},
		{"no line", Status{Timer: "active", Set: set, Times: times}, true},
		{"old line only", Status{Timer: "active", Set: set, Times: times, Last: &Entry{At: prev.Add(-7 * 24 * time.Hour), Outcome: Started}}, true},
		{"set after due", Status{Timer: "active", Set: prev.Add(time.Hour), Times: times}, false},
		{"within grace", Status{Timer: "active", Set: set, Times: times}, false},
		{"skipped", Status{Timer: "active", Set: set, Times: times, Last: &Entry{At: prev, Outcome: Skipped, Reason: "quiet hours"}}, true},
		{"timer gone", Status{Timer: "missing", Times: times}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n := now
			if c.name == "within grace" {
				n = prev.Add(10 * time.Minute)
			}
			if got := c.s.Problem(n); (got != "") != c.want {
				t.Fatalf("Problem() = %q, want problem %v", got, c.want)
			}
		})
	}
}

func TestLogAndOpenAgents(t *testing.T) {
	home := t.TempDir()
	at := time.Date(2026, 11, 30, 8, 0, 0, 0, time.UTC)
	for _, e := range []Entry{
		{Route: "dream", At: at, Outcome: Started, Agent: "dream-1130"},
		{Route: "retro", At: at, Outcome: Skipped, Reason: "quiet hours"},
		{Route: "skill-doctor", At: at, Outcome: Started, Agent: "skill-doctor-1130"},
	} {
		if err := Append(home, e); err != nil {
			t.Fatal(err)
		}
	}
	es, err := Read(home)
	if err != nil || len(es) != 3 {
		t.Fatalf("Read = %v, %v", es, err)
	}
	if l, ok := LastStarted(es, "dream"); !ok || l.Agent != "dream-1130" {
		t.Fatalf("LastStarted = %v %v", l, ok)
	}
	if n := OpenAgents(es, func(n string) bool { return n == "dream-1130" }); n != 1 {
		t.Fatalf("OpenAgents = %d, want 1", n)
	}
}

func TestQuotaLow(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 11, 27, 14, 30, 0, 0, time.UTC)
	if err := os.MkdirAll(filepath.Join(home, "logs"), 0o700); err != nil {
		t.Fatal(err)
	}
	tick := `{"at":"2026-11-27T14:01:10Z","profiles":[{"profile":"claude-a","quota":{"available":true,"windows":[{"kind":"5h","remaining_pct":9},{"kind":"weekly","remaining_pct":58}]}},{"profile":"claude-b","quota":{"available":true,"windows":[{"kind":"weekly","remaining_pct":58}]}}]}`
	if err := os.WriteFile(filepath.Join(home, "logs", "burn.jsonl"), []byte("{}\n"+tick+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		profile string
		now     time.Time
		low     bool
	}{
		{"claude-a", now, true},
		{"claude-b", now, false},
		{"untracked", now, false},
		{"claude-a", now.Add(3 * time.Hour), false}, // stale tick
	}
	for _, c := range cases {
		if got := QuotaLow(home, c.profile, 15, c.now); (got != "") != c.low {
			t.Errorf("QuotaLow(%s, %v) = %q, want low %v", c.profile, c.now, got, c.low)
		}
	}
}

func TestServiceTextQuotes(t *testing.T) {
	got := serviceText("dream", `/opt/my fleet/50%/fleet`, `/srv/a b/.fleet`)
	for _, want := range []string{`Environment="FLEET_HOME=/srv/a b/.fleet"`, `"/opt/my fleet/50%%/fleet"`, `exec "$$0" schedule run --timer dream`} {
		if !strings.Contains(got, want) {
			t.Errorf("service text lacks %q:\n%s", want, got)
		}
	}
}

// A weekday calendar seen on Monday morning: the previous due time is
// Friday, past the weekend gap that is longer than the next one.
func TestWhenWeekdays(t *testing.T) {
	if _, err := exec.LookPath("systemd-analyze"); err != nil {
		t.Skip("no systemd-analyze")
	}
	now := time.Date(2026, 11, 30, 8, 0, 0, 0, time.UTC)
	w, err := When(t.Context(), "Mon..Fri 09:00 UTC", now)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC); !w.Prev.Equal(want) {
		t.Fatalf("Prev = %v, want %v", w.Prev, want)
	}
}
