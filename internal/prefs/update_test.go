package prefs

import (
	"testing"
	"time"
)

func TestLoadUpdate(t *testing.T) {
	write(t, "[defaults]\nmax_working = 2\n")
	if u, err := LoadUpdate(); err != nil || u != DefaultUpdate() || !u.Auto {
		t.Fatalf("unset must be on by default: %+v %v", u, err)
	}
	write(t, "[update]\nauto = false\ninterval = \"1h\"\n")
	if u, err := LoadUpdate(); err != nil || u.Auto || u.Every != time.Hour {
		t.Fatalf("opt-out: %+v %v", u, err)
	}
	write(t, "[update]\ninterval = \"1s\"\n")
	if u, _ := LoadUpdate(); u.Every != MinUpdateEvery || !u.Auto {
		t.Fatalf("short interval must be raised: %+v", u)
	}
	write(t, "[update]\nauto = maybe\n")
	if _, err := LoadUpdate(); err == nil {
		t.Fatal("bad auto accepted")
	}
	write(t, "[update]\nwait_quiet = false\nbuild_command = \"  make build  \"\n")
	if u, err := LoadUpdate(); err != nil || u.WaitQuiet || u.BuildCommand != "make build" {
		t.Fatalf("wait_quiet / build_command: %+v %v", u, err)
	}
	write(t, "[update]\nwait_quiet = 1\n")
	if _, err := LoadUpdate(); err == nil {
		t.Fatal("bad wait_quiet accepted")
	}
	write(t, "[update]\nbuild_command = [\"make\", \"build\"]\n")
	if _, err := LoadUpdate(); err == nil {
		t.Fatal("bad build_command accepted")
	}
}
