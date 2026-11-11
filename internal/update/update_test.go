package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
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
