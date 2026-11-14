package companion

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeBin writes an executable stand-in so LookPath and Stat succeed.
func fakeBin(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFindOrder(t *testing.T) {
	envBin, cfgBin, pathBin := fakeBin(t, "env-x"), fakeBin(t, "cfg-x"), fakeBin(t, "x")
	t.Setenv("PATH", filepath.Dir(pathBin))
	cfg := ""
	Config = func(string) string { return cfg }
	t.Cleanup(func() { Config = nil })
	s := Spec{Name: "x", Env: []string{"FLEET_X"}}

	for _, tc := range []struct {
		env, cfg, want, src string
		err                 error
	}{
		{"", "", pathBin, "PATH", nil},
		{"", "auto", pathBin, "PATH", nil},
		{"", cfgBin, cfgBin, "config", nil},
		{envBin, cfgBin, envBin, "$FLEET_X", nil},
		{"off", cfgBin, "", "$FLEET_X", ErrOff},
		{"", "off", "", "config", ErrOff},
		{"/nope/x", "", "/nope/x", "$FLEET_X", ErrMissing},
	} {
		t.Setenv("FLEET_X", tc.env)
		cfg = tc.cfg
		tool, err := Find(s)
		if !errors.Is(err, tc.err) || tool.Path != tc.want || tool.Source != tc.src {
			t.Errorf("env=%q cfg=%q: got %q %q %v, want %q %q %v", tc.env, tc.cfg, tool.Path, tool.Source, err, tc.want, tc.src, tc.err)
		}
	}

	t.Setenv("FLEET_X", "")
	cfg = ""
	t.Setenv("PATH", t.TempDir())
	if _, err := Find(s); !errors.Is(err, ErrMissing) || !strings.Contains(err.Error(), "x is not installed") {
		t.Errorf("missing: %v", err)
	}
}

func TestBinsFirstFound(t *testing.T) {
	b := fakeBin(t, "second")
	t.Setenv("PATH", filepath.Dir(b))
	tool, err := Find(Spec{Name: "pick", Bins: []string{"first", "second"}})
	if err != nil || tool.Path != b {
		t.Fatalf("got %q %v", tool.Path, err)
	}
}

func TestMinVersionAndCache(t *testing.T) {
	b := fakeBin(t, "v")
	calls := 0
	Runner = func(_ context.Context, _ string, args []string, _ Opts) ([]byte, error) {
		calls++
		return []byte("v version 0.9.2 (abc)\n"), nil
	}
	t.Cleanup(func() { Runner = execRun })
	t.Setenv("FLEET_V", b)
	s := Spec{Name: "v", Env: []string{"FLEET_V"}, Version: []string{"--version"}, Min: "0.10"}
	_, err := Find(s)
	var e *Error
	if !errors.As(err, &e) || e.Err != ErrTooOld || e.Version != "0.9.2" {
		t.Fatalf("too old: %v", err)
	}
	s.Min = "0.9"
	if _, err := Find(s); err != nil {
		t.Fatalf("new enough: %v", err)
	}
	if calls != 1 {
		t.Errorf("version probed %d times, want 1 (cached by path and mtime)", calls)
	}
	if st := Check(s); st.Version != "0.9.2" || st.Err != nil || st.Source != "$FLEET_V" {
		t.Errorf("check: %+v", st)
	}
}

func TestOlder(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{{"0.9", "0.10", true}, {"1.0", "1.0.0", false}, {"2.1.3", "2.1", false}, {"3", "3.0.1", true}} {
		if got := Older(tc.a, tc.b); got != tc.want {
			t.Errorf("Older(%s, %s) = %v", tc.a, tc.b, got)
		}
	}
}

func TestRunAndJSON(t *testing.T) {
	Runner = func(_ context.Context, _ string, args []string, _ Opts) ([]byte, error) {
		switch args[0] {
		case "bad":
			return []byte("not json"), nil
		case "fail":
			return nil, errors.New("exit status 2: no such index")
		}
		return []byte(`{"a":1,"extra":true}`), nil
	}
	t.Cleanup(func() { Runner = execRun })
	tool := Tool{Spec: Spec{Name: "t"}, Path: "/t"}
	var v struct{ A int }
	if err := tool.JSON(context.Background(), Opts{}, &v, "ok"); err != nil || v.A != 1 {
		t.Errorf("json: %v %+v", err, v)
	}
	if err := tool.JSON(context.Background(), Opts{}, &v, "bad"); err == nil || !strings.Contains(err.Error(), "t bad: unreadable output") {
		t.Errorf("bad: %v", err)
	}
	if _, err := tool.Run(context.Background(), Opts{}, "fail"); err == nil || err.Error() != "t fail: exit status 2: no such index" {
		t.Errorf("fail: %v", err)
	}
}

func TestExecRunFirstStderrLine(t *testing.T) {
	b := filepath.Join(t.TempDir(), "sh")
	if err := os.WriteFile(b, []byte("#!/bin/sh\necho out\nprintf '\\n  first problem\\nsecond\\n' >&2\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := execRun(context.Background(), b, nil, Opts{})
	if err == nil || !strings.HasSuffix(err.Error(), ": first problem") || string(out) != "out\n" {
		t.Errorf("got %q %v", out, err)
	}
}

func TestRegistered(t *testing.T) {
	Register(Spec{Name: "zz-test"})
	Register(Spec{Name: "aa-test"})
	var names []string
	for _, s := range Registered() {
		names = append(names, s.Name)
	}
	j := strings.Join(names, ",")
	if !strings.Contains(j, "aa-test") || strings.Index(j, "aa-test") > strings.Index(j, "zz-test") {
		t.Errorf("registered: %s", j)
	}
}
