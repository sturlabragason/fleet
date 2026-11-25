package pages

import (
	"testing"
	"time"
)

func TestPublished(t *testing.T) {
	list := "decide-a\tprivate\t2026-11-25T11:25:02Z\thttps://x/decide-a/\nother\tpublic\tbad\tu\n"
	cases := []struct {
		name string
		want string
		ok   bool
	}{
		{"decide-a", "2026-11-25T11:25:02Z", true},
		{"other", "0001-01-01T00:00:00Z", true}, // unreadable time: still exists
		{"missing", "0001-01-01T00:00:00Z", false},
	}
	for _, c := range cases {
		got, ok := Published(list, c.name)
		if ok != c.ok || got.Format(time.RFC3339) != c.want {
			t.Errorf("Published(%s) = %v %v, want %s %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

// The log of decide-svc-desc-ingest on 2026-11-25: opened 11:23, republished
// 11:25, opened again, Submit refused at 11:29 for the voided token.
func TestUnanswered(t *testing.T) {
	log := "2026-11-25 11:23:46\tu@x\tallow\t/decide-a/\n" +
		"2026-11-25 11:25:03\tu@x\tallow\t/decide-a/\n" +
		"2026-11-25 11:26:00\tu@x\tallow\t/decide-b/\n" +
		"2026-11-25 11:29:47\tu@x\tDENY\t/_answer/decide-a forbidden-token\n"
	sent := log + "2026-11-25 11:31:00\tu@x\tallow\t/_answer/decide-a sent\n"
	pub := time.Date(2026, 11, 25, 11, 25, 2, 0, time.UTC)
	cases := []struct {
		name, log string
		since     time.Time
		want      string
	}{
		{"opened after publish", log, pub, "u@x 11:25:03"},
		{"answered", sent, pub, ""},
		{"opened before publish only", log, pub.Add(time.Hour), ""},
		{"opened as index.html", "2026-11-25 11:30:00\tw@x\tallow\t/decide-a/index.html\n", pub, "w@x 11:30:00"},
		{"opened again after answer", sent + "2026-11-25 11:40:00\tv@x\tallow\t/decide-a/\n", pub, ""},
	}
	for _, c := range cases {
		who, at, ok := Unanswered(c.log, "decide-a", c.since)
		got := ""
		if ok {
			got = who + " " + at.Format(time.TimeOnly)
		}
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
