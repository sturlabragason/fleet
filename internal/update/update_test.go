package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"v1.2.4", "v1.2.3", true},
		{"v1.10.0", "v1.9.9", true},
		{"v2.0.0", "v1.99.99", true},
		{"v1.2.3", "v1.2.3", false},
		{"v1.2.3", "v1.3.0", false},
		{"v1.3.0", "v1.2.3-4-gabc-dirty", false},
		{"1.3.0", "v1.2.0", false},
		{"v1.3.0-snapshot-abc", "v1.2.0", false},
		{"v1.66.1", "v1.65.1+custom.6ef063c", true},
		{"v1.66.1", "v1.66.1+custom.6ef063c", false},
		{"v1.66.1+a", "v1.66.1+b", false},
		{"v1.66.1", "v1.65.1+", false},
		{"v1.66.1", "v1.65.1+a..b", false},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestAsset(t *testing.T) {
	if a := Asset("windows-arm64"); a != "fleet-windows-arm64.exe" {
		t.Errorf("windows asset = %q", a)
	}
	if a := Asset("darwin-arm64"); a != "fleet-darwin-arm64" {
		t.Errorf("darwin asset = %q", a)
	}
}

func TestEligible(t *testing.T) {
	if ok, _ := Eligible("v1.2.3", Channel); !ok {
		t.Fatal("release build must be eligible")
	}
	for _, c := range [][2]string{
		{"v1.2.3", ""},                  // make build at a tag
		{"v1.2.3-4-gabc1234-dirty", ""}, // make build
		{"v1.2.3-4-gabc1234", Channel},  // not a tag
		{"", Channel},                   // no version
	} {
		if ok, why := Eligible(c[0], c[1]); ok || why == "" {
			t.Errorf("Eligible(%q, %q) = %v %q, want a reason to skip", c[0], c[1], ok, why)
		}
	}
	if ok, why := EligibleFor("v1.65.1+custom.abc", "", ""); ok || why == "" {
		t.Errorf("source build without build_command must not be eligible: %v %q", ok, why)
	}
	if ok, why := EligibleFor("v1.65.1+custom.abc", "", "make build"); !ok {
		t.Errorf("source build with build_command must be eligible: %q", why)
	}
	if ok, why := EligibleFor("v1.65.1+custom.abc", Channel, ""); !ok {
		t.Errorf("release build with +build metadata must be eligible: %q", why)
	}
	if ok, why := EligibleFor("v1.65.1-4-gabc", "", "make build"); ok || why == "" {
		t.Errorf("git-describe dev build with build_command must not be eligible: %v %q", ok, why)
	}
}

// release serves /latest, SHA256SUMS and the asset for one tag.
type release struct {
	tag, body, sum string
	sig            string // SHA256SUMS.sig; "" serves none
	downloads      atomic.Int32
	unauthorized   atomic.Int32 // requests without the Authorization header
}

func (r *release) serve(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		if q.Header.Get("Authorization") != "Bearer t0k" {
			r.unauthorized.Add(1)
		}
		switch q.URL.Path {
		case "/download/" + r.tag + "/SHA256SUMS.sig":
			if r.sig == "" {
				http.NotFound(w, q)
				return
			}
			w.Write([]byte(r.sig))
		case "/latest":
			http.Redirect(w, q, "/tag/"+r.tag, http.StatusFound)
		case "/download/" + r.tag + "/SHA256SUMS":
			w.Write([]byte(r.sum + "  fleet-linux-amd64\n0000000000000000000000000000000000000000000000000000000000000000  fleet-darwin-arm64\n"))
		case "/download/" + r.tag + "/fleet-linux-amd64":
			r.downloads.Add(1)
			w.Write([]byte(r.body))
		default:
			http.NotFound(w, q)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func sum(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

func setup(t *testing.T, r *release) (*Updater, string) {
	// Tests set the channel hooks themselves; a build that fills them is restored after.
	vs, au := VerifySums, Authorize
	VerifySums, Authorize = nil, nil
	t.Cleanup(func() { VerifySums, Authorize = vs, au })
	dir := t.TempDir()
	exe := filepath.Join(dir, "bin", "fleet")
	os.MkdirAll(filepath.Dir(exe), 0o755)
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	u := &Updater{
		Home: filepath.Join(dir, "home"), Exe: exe, Current: "v1.0.0",
		BaseURL: r.serve(t).URL, Target: "linux-amd64",
		Verify: func(string) error { return nil },
		Probe:  func(string, string) error { return nil },
		Now:    func() time.Time { return now },
	}
	return u, exe
}

func content(t *testing.T, p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestStepUpgradesWhenQuiet(t *testing.T) {
	r := &release{tag: "v1.1.0", body: "new"}
	r.sum = sum(r.body)
	u, exe := setup(t, r)
	quiet := false
	u.Quiet = func() bool { return quiet }
	if u.Step(context.Background()) {
		t.Fatal("swapped while an agent was working")
	}
	if content(t, exe) != "old" || Load(u.Home).Staged != "v1.1.0" {
		t.Fatalf("want staged, old binary in place: %+v", Load(u.Home))
	}
	quiet = true
	if !u.Step(context.Background()) {
		t.Fatalf("not applied: %+v", Load(u.Home))
	}
	if content(t, exe) != "new" {
		t.Fatal("binary not replaced")
	}
	if fi, _ := os.Stat(exe); runtime.GOOS != "windows" && fi.Mode().Perm()&0o100 == 0 {
		t.Fatal("new binary not executable")
	}
	s := Load(u.Home)
	if s.Applied == nil || s.Applied.From != "v1.0.0" || s.Applied.To != "v1.1.0" || s.Staged != "" {
		t.Fatalf("state: %+v", s)
	}
	if r.downloads.Load() != 1 {
		t.Fatalf("downloads = %d, want 1", r.downloads.Load())
	}
	if prev := filepath.Join(Dir(u.Home), "fleet-v1.0.0.prev"); content(t, prev) != "old" {
		t.Fatal("old binary not kept for rollback")
	}
	n := Announce(u.Home, "v1.1.0", "", u.Now())
	if !strings.Contains(n, "v1.0.0 → v1.1.0") {
		t.Fatalf("announce: %q", n)
	}
	if Announce(u.Home, "v1.1.0", "", u.Now()) != "" {
		t.Fatal("announced twice to the same admiral")
	}
	if Announce(u.Home, "v1.0.0", "two", u.Now()) != "" {
		t.Fatal("announced to a server still on the old build")
	}
}

func TestFailedVerifyKeepsOldBinary(t *testing.T) {
	r := &release{tag: "v1.1.0", body: "tampered", sum: sum("new")}
	u, exe := setup(t, r)
	if u.Step(context.Background()) {
		t.Fatal("applied a binary whose checksum did not match")
	}
	s := Load(u.Home)
	if content(t, exe) != "old" || s.Failed != "v1.1.0" || !strings.Contains(s.Err, "checksum mismatch") || s.Staged != "" {
		t.Fatalf("state: %+v", s)
	}
	// The next due check skips the failed release instead of retrying it.
	later := u.Now().Add(DefaultEvery)
	u.Now = func() time.Time { return later }
	u.Step(context.Background())
	if r.downloads.Load() != 1 {
		t.Fatalf("failed release re-downloaded within %s", RetryFailed)
	}
	// A failed probe or attestation is refused the same way.
	r2 := &release{tag: "v1.2.0", body: "new"}
	r2.sum = sum(r2.body)
	u2, exe2 := setup(t, r2)
	u2.Verify = func(string) error { return errors.New("attestation failed") }
	if u2.Step(context.Background()) || content(t, exe2) != "old" {
		t.Fatal("applied a binary that failed verification")
	}
	if ents, _ := os.ReadDir(Dir(u2.Home)); len(ents) != 1 {
		t.Fatalf("left files behind: %v", ents)
	}
}

// A build with its own channel checks signed sums instead of the
// attestation and sends its credentials on every request.
func TestSignedSumsAndAuthorize(t *testing.T) {
	sign := func(sums, sig []byte) error {
		if string(sig) != "good:"+sum(string(sums)) {
			return errors.New("bad signature")
		}
		return nil
	}
	auth := func(r *http.Request) error { r.Header.Set("Authorization", "Bearer t0k"); return nil }
	sums := func(body string) string {
		return sum(body) + "  fleet-linux-amd64\n0000000000000000000000000000000000000000000000000000000000000000  fleet-darwin-arm64\n"
	}

	r := &release{tag: "v1.1.0", body: "new"}
	r.sum = sum(r.body)
	r.sig = "good:" + sum(sums(r.body))
	u, exe := setup(t, r)
	VerifySums, Authorize = sign, auth
	u.Verify = nil // the signed sums replace the attestation
	u.Quiet = func() bool { return true }
	if !u.Step(context.Background()) || content(t, exe) != "new" {
		t.Fatalf("signed release not applied: %+v", Load(u.Home))
	}
	if n := r.unauthorized.Load(); n != 0 {
		t.Fatalf("%d requests without credentials", n)
	}

	for name, sig := range map[string]string{"bad": "good:" + sum("other"), "missing": ""} {
		r := &release{tag: "v1.1.0", body: "new", sig: sig}
		r.sum = sum(r.body)
		u, exe := setup(t, r)
		VerifySums, Authorize = sign, auth
		u.Verify = nil
		u.Quiet = func() bool { return true }
		if u.Step(context.Background()) || content(t, exe) != "old" || r.downloads.Load() != 0 {
			t.Fatalf("%s signature: applied or downloaded: %+v", name, Load(u.Home))
		}
	}
}

func TestNoUpgradeWhenCurrent(t *testing.T) {
	r := &release{tag: "v1.0.0", body: "same"}
	r.sum = sum(r.body)
	u, exe := setup(t, r)
	if u.Step(context.Background()) || content(t, exe) != "old" || r.downloads.Load() != 0 {
		t.Fatal("upgraded to the running release")
	}
	if s := Load(u.Home); s.Latest != "v1.0.0" || s.CheckedAt.IsZero() {
		t.Fatalf("check not recorded: %+v", s)
	}
}

func TestSumFor(t *testing.T) {
	h := sum("x")
	if got, err := sumFor([]byte(h+" *fleet-linux-arm64\n"), "fleet-linux-arm64"); err != nil || got != h {
		t.Fatalf("binary-mode line: %q %v", got, err)
	}
	if _, err := sumFor([]byte(h+"  fleet-linux-arm64.tar.gz\n"), "fleet-linux-arm64"); err == nil {
		t.Fatal("matched another asset")
	}
}

func TestVersionMatches(t *testing.T) {
	for _, c := range []struct {
		out, want string
		match     bool
	}{
		{"fleet v1.66.1", "v1.66.1", true},
		{"fleet v1.66.1 (abcdef1 · built 2026-11-22)", "v1.66.1", true},
		{"fleet v1.66.1+custom.6ef063c", "v1.66.1", true},
		{"fleet v1.66.1+custom.6ef063c (abcdef1)", "v1.66.1", true},
		{"fleet v1.66.1+", "v1.66.1", false},
		{"fleet v1.66.1+a..b", "v1.66.1", false},
		{"fleet v1.66.10", "v1.66.1", false},
		{"fleet v1.66.1-rc1", "v1.66.1", false},
		{"v1.66.1", "v1.66.1", false},
	} {
		if got := versionMatches(c.out, c.want); got != c.match {
			t.Errorf("versionMatches(%q, %q) = %v, want %v", c.out, c.want, got, c.match)
		}
	}
}

func TestAnnounceWithBuildMetadata(t *testing.T) {
	r := &release{tag: "v1.66.1", body: "new"}
	r.sum = sum(r.body)
	u, _ := setup(t, r)
	u.Current = "v1.65.1+custom.old"
	if !u.Step(t.Context()) {
		t.Fatalf("upgrade failed: %+v", Load(u.Home))
	}
	if n := Announce(u.Home, "v1.65.1+custom.old", "main", u.Now()); n != "" {
		t.Fatalf("announced to old build: %q", n)
	}
	n := Announce(u.Home, "v1.66.1+custom.new", "main", u.Now())
	if !strings.Contains(n, "v1.65.1+custom.old → v1.66.1") {
		t.Fatalf("did not announce to +build release: %q", n)
	}
	if n2 := Announce(u.Home, "v1.66.1+custom.new", "main", u.Now()); n2 != "" {
		t.Fatalf("announced twice: %q", n2)
	}
}

func TestBuildCommand(t *testing.T) {
	r := &release{tag: "v1.1.0", body: "unused"}
	r.sum = sum(r.body)
	u, exe := setup(t, r)
	u.Current = "v1.0.0+custom.old"

	// Even if VerifySums is set, build_command builds locally instead of downloading assets.
	var verifyCalled atomic.Bool
	VerifySums = func(_, _ []byte) error { verifyCalled.Store(true); return errors.New("should not run") }

	// Default public base does not leak Authorize tokens when BuildCommand is set.
	if u2 := (&Updater{BuildCommand: "true"}); u2.hasCustomBase() {
		t.Fatal("empty base reported as custom")
	}
	t.Setenv("FLEET_UPDATE_URL", "")
	if u2 := (&Updater{BaseURL: u.BaseURL, BuildCommand: "true"}); !u2.hasCustomBase() {
		t.Fatal("BaseURL should count as custom base")
	}

	outMarker := filepath.Join(t.TempDir(), "built-env.txt")
	if runtime.GOOS == "windows" {
		u.BuildCommand = `echo built-%FLEET_UPDATE_VERSION%> "%FLEET_UPDATE_OUT%"`
		if _, err := exec.LookPath("sh"); err == nil { // sysx.ShellCommand prefers sh (Git Bash on CI)
			u.BuildCommand = `echo "built-$FLEET_UPDATE_VERSION" > "$FLEET_UPDATE_OUT"`
		}
		u.Probe = func(path, want string) error {
			b, err := os.ReadFile(path)
			if err != nil || !strings.Contains(string(b), "built-"+want) {
				return errors.New("unexpected output")
			}
			return nil
		}
	} else {
		u.BuildCommand = `printf '%s|%s|%s\n' "$FLEET_UPDATE_VERSION" "$FLEET_UPDATE_CURRENT" "$(pwd -P)" > "` + outMarker + `"` +
			` && printf '#!/bin/sh\nif [ "$1" = "version" ]; then echo "fleet %s+custom.new"; elif [ "$1" = "mcp" ] && [ "$2" = "--probe" ]; then echo "ok"; else exit 1; fi\n' "$FLEET_UPDATE_VERSION" > "$FLEET_UPDATE_OUT"`
		u.Probe = nil // run the real Probe on the produced executable
	}

	if !u.Step(t.Context()) {
		t.Fatalf("build_command upgrade failed: %+v", Load(u.Home))
	}
	if verifyCalled.Load() {
		t.Fatal("VerifySums should not be called for build_command")
	}
	if r.downloads.Load() != 0 {
		t.Fatalf("downloads = %d, want 0", r.downloads.Load())
	}
	s := Load(u.Home)
	if s.Applied == nil || s.Applied.From != "v1.0.0+custom.old" || s.Applied.To != "v1.1.0" {
		t.Fatalf("applied state = %+v", s.Applied)
	}
	if runtime.GOOS != "windows" {
		realHome, err := filepath.EvalSymlinks(u.Home) // build_command runs in the fleet home
		if err != nil {
			t.Fatal(err)
		}
		if env := content(t, outMarker); strings.TrimSpace(env) != "v1.1.0|v1.0.0+custom.old|"+realHome {
			t.Fatalf("env = %q", env)
		}
		if got := content(t, exe); !strings.Contains(got, "+custom.new") {
			t.Fatalf("exe not replaced: %q", got)
		}
	}

	// Failure cases: non-zero exit, symlink output, empty output keep old binary.
	for name, cmd := range map[string]string{
		"nonzero": "exit 2",
		"empty":   `: > "$FLEET_UPDATE_OUT"`,
	} {
		if runtime.GOOS == "windows" && name == "empty" {
			continue
		}
		rf := &release{tag: "v1.2.0", body: "unused"}
		rf.sum = sum(rf.body)
		uf, exef := setup(t, rf)
		uf.BuildCommand = cmd
		if uf.Step(t.Context()) || content(t, exef) != "old" {
			t.Fatalf("%s: expected failure keeping old binary, got %+v", name, Load(uf.Home))
		}
		if st := Load(uf.Home); st.Failed != "v1.2.0" || st.Err == "" {
			t.Fatalf("%s: failed state = %+v", name, st)
		}
	}
	if runtime.GOOS != "windows" {
		rs := &release{tag: "v1.2.0", body: "unused"}
		rs.sum = sum(rs.body)
		us, exes := setup(t, rs)
		target := filepath.Join(t.TempDir(), "target")
		os.WriteFile(target, []byte("x"), 0o755)
		us.BuildCommand = `ln -s "` + target + `" "$FLEET_UPDATE_OUT"`
		if us.Step(t.Context()) || content(t, exes) != "old" {
			t.Fatalf("symlink output must be rejected: %+v", Load(us.Home))
		}
		if st := Load(us.Home); !strings.Contains(st.Err, "not a regular file") {
			t.Fatalf("symlink err = %+v", st)
		}
	}
}

func TestUpdateLock(t *testing.T) {
	r := &release{tag: "v1.1.0", body: "new"}
	r.sum = sum(r.body)
	u, exe := setup(t, r)
	unlock, ok, err := u.tryLock()
	if err != nil || !ok {
		t.Fatalf("tryLock = %v %v", ok, err)
	}
	defer unlock()

	// Step skips quietly when another updater holds the lock.
	if u.Step(t.Context()) || content(t, exe) != "old" || r.downloads.Load() != 0 {
		t.Fatalf("Step proceeded under held lock: %+v", Load(u.Home))
	}
	// Install fails clearly when another updater holds the lock.
	if _, err := u.Install(t.Context()); err == nil || !strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("Install err = %v, want already in progress", err)
	}
}
