// Package update keeps a release install of fleet on the latest release
// (DESIGN §5d, ADR-114). The resident watcher runs it: every interval it
// asks GitHub which release is latest, downloads a newer binary with
// SHA256SUMS, checks the sum (and the build attestation when gh can), runs
// the download, stages it, and renames it over the running binary once no
// agent is working. Every fleet process already follows a replaced binary
// (ADR-25, ADR-62, ADR-111), so the swap is the whole upgrade.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/logx"
)

// Where releases come from. Both are variables so a build can point its
// updates elsewhere with -ldflags -X (DESIGN §15, "Extensions").
var (
	// Repo is the GitHub repo whose releases and attestations are checked.
	Repo = "sturlabragason/fleet"
	// DefaultBaseURL is the releases page; empty is Repo's GitHub releases.
	// FLEET_UPDATE_URL overrides it.
	DefaultBaseURL = ""
)

// Hooks for a build with its own release channel; nil in a default build.
var (
	// VerifySums checks SHA256SUMS against the SHA256SUMS.sig next to it
	// before any sum in it is trusted. When set, it replaces the
	// attestation check.
	VerifySums func(sums, sig []byte) error
	// Authorize adds credentials to every update request.
	Authorize func(r *http.Request) error
)

const (
	// DefaultEvery is how often a release install checks for a new release.
	DefaultEvery = 6 * time.Hour
	// RetryFailed is how long a release that failed to download, verify or
	// install is left alone before it is tried again.
	RetryFailed = 24 * time.Hour
	// AnnounceFor is how long after an upgrade an admiral is still told.
	AnnounceFor = 7 * 24 * time.Hour
	// Channel is what the release build links into main.channel.
	Channel = "release"

	maxBinary = 256 << 20
	maxSums   = 64 << 10
)

var semverRE = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)

// Parse reads a release tag vX.Y.Z; anything else (a git describe of a
// dev build, a snapshot) is not a release.
func Parse(v string) ([3]int, bool) {
	m := semverRE.FindStringSubmatch(v)
	if m == nil {
		return [3]int{}, false
	}
	var out [3]int
	for i := range out {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

// Newer reports whether release tag a is newer than b. A tag that does not
// parse is never newer, and nothing is newer than a tag that does not parse.
func Newer(a, b string) bool {
	x, ok1 := Parse(a)
	y, ok2 := Parse(b)
	if !ok1 || !ok2 {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return false
}

// Eligible says whether this binary may upgrade itself, and why not. Only a
// release build (main.channel = "release" and a vX.Y.Z tag) does: a make
// build, a go install or a dirty tree is the user's own and is left alone.
func Eligible(version, channel string) (bool, string) {
	if channel != Channel {
		return false, "not a release build (built from source)"
	}
	if _, ok := Parse(version); !ok {
		return false, fmt.Sprintf("version %q is not a release tag", version)
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		return false, "no release binary for " + runtime.GOOS
	}
	return true, ""
}

// Target is the release asset suffix for this machine: linux-amd64, ...
func Target() string { return runtime.GOOS + "-" + runtime.GOARCH }

// Asset is the release file for target: fleet-linux-amd64,
// fleet-windows-amd64.exe.
func Asset(target string) string {
	if strings.HasPrefix(target, "windows-") {
		return "fleet-" + target + ".exe"
	}
	return "fleet-" + target
}

// exeExt is what an executable's name must end in here: Windows runs
// nothing without it.
func exeExt() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// State is what the updater remembers between passes and re-execs, in
// <home>/update/state.json.
type State struct {
	CheckedAt time.Time `json:"checked_at,omitzero"`
	Latest    string    `json:"latest,omitempty"`
	// Staged is a verified download of a newer release, waiting for a quiet
	// moment, at <home>/update/fleet-<Staged>.
	Staged    string `json:"staged,omitempty"`
	StagedSum string `json:"staged_sha256,omitempty"`
	// Failed is the last release that could not be installed, and why.
	Failed   string    `json:"failed,omitempty"`
	FailedAt time.Time `json:"failed_at,omitzero"`
	Err      string    `json:"error,omitempty"`
	Applied  *Applied  `json:"applied,omitempty"`
}

// Applied records the last upgrade.
type Applied struct {
	From string    `json:"from"`
	To   string    `json:"to"`
	At   time.Time `json:"at"`
	Exe  string    `json:"exe"`
}

// Dir is where the updater keeps its state and downloads.
func Dir(home string) string { return filepath.Join(home, "update") }

func statePath(home string) string { return filepath.Join(Dir(home), "state.json") }

// Load reads the state; a missing or broken file is an empty state.
func Load(home string) State {
	var s State
	if data, err := os.ReadFile(statePath(home)); err == nil {
		if json.Unmarshal(data, &s) != nil {
			s = State{}
		}
	}
	return s
}

func save(home string, s State) error {
	if err := os.MkdirAll(Dir(home), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := statePath(home) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, statePath(home))
}

// Updater checks, stages and applies releases for one binary.
type Updater struct {
	Home    string // ~/.fleet
	Exe     string // the binary to replace (symlinks resolved)
	Current string // the running release tag
	BaseURL string // FLEET_UPDATE_URL, else DefaultBaseURL, else Repo's releases, when empty
	Target  string // Target() when empty
	Every   time.Duration
	Client  *http.Client
	// Quiet reports whether the swap may happen now: no agent mid-turn.
	// nil = always.
	Quiet func() bool
	// Verify checks provenance beyond the checksum; nil = Attest.
	Verify func(path string) error
	// Probe runs the downloaded binary; nil = Probe.
	Probe func(path, want string) error
	Now   func() time.Time

	applied bool   // this process swapped the binary: nothing more to do
	waiting string // the staged release last logged as waiting
}

func (u *Updater) now() time.Time {
	if u.Now != nil {
		return u.Now()
	}
	return time.Now()
}

func (u *Updater) every() time.Duration {
	if u.Every > 0 {
		return u.Every
	}
	return DefaultEvery
}

func (u *Updater) base() string {
	if u.BaseURL != "" {
		return strings.TrimRight(u.BaseURL, "/")
	}
	if v := os.Getenv("FLEET_UPDATE_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	if DefaultBaseURL != "" {
		return strings.TrimRight(DefaultBaseURL, "/")
	}
	return "https://github.com/" + Repo + "/releases"
}

// authorize applies Authorize to req, if set.
func authorize(req *http.Request) error {
	if Authorize == nil {
		return nil
	}
	return Authorize(req)
}

func (u *Updater) target() string {
	if u.Target != "" {
		return u.Target
	}
	return Target()
}

func (u *Updater) client() *http.Client {
	if u.Client != nil {
		return u.Client
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

// Run passes every minute until ctx ends or the binary is replaced, the
// first pass after delay. A pass is a file read unless a check is due.
func (u *Updater) Run(ctx context.Context, delay time.Duration) {
	t := time.NewTimer(delay)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if u.Step(ctx) {
			return
		}
		t.Reset(time.Minute)
	}
}

// Step does one pass: check when a check is due and stage what it finds,
// then swap a staged release in when Quiet allows. It reports true once
// this process has swapped the binary. Errors are logged once per release,
// recorded in the state and never stop the loop.
func (u *Updater) Step(ctx context.Context) (done bool) {
	if u.applied {
		return true
	}
	log := logx.For("update")
	defer func() {
		if r := recover(); r != nil {
			log.Error("panic", "err", r)
			done = false
		}
	}()
	s := Load(u.Home)
	now := u.now()
	if s.Staged != "" && !Newer(s.Staged, u.Current) {
		os.Remove(u.stagedPath(s.Staged))
		s.Staged, s.StagedSum = "", ""
		save(u.Home, s)
	}
	if s.Staged == "" && now.Sub(s.CheckedAt) >= u.every() {
		s = u.check(ctx, s, now)
	}
	if s.Staged == "" {
		return false
	}
	if u.Quiet != nil && !u.Quiet() {
		if u.waiting != s.Staged {
			u.waiting = s.Staged
			log.Info("staged; waiting until no agent is working", "version", s.Staged)
		}
		return false
	}
	if err := u.apply(&s, now); err != nil {
		u.fail(&s, s.Staged, now, err)
		os.Remove(u.stagedPath(s.Staged))
		s.Staged, s.StagedSum = "", ""
		save(u.Home, s)
		return false
	}
	u.applied = true
	return true
}

// check asks for the latest release and stages it when it is newer.
func (u *Updater) check(ctx context.Context, s State, now time.Time) State {
	log := logx.For("update")
	s.CheckedAt = now
	latest, err := u.Latest(ctx)
	if err != nil {
		log.Info("check failed", "err", err) // offline is normal; try at the next interval
		save(u.Home, s)
		return s
	}
	s.Latest = latest
	if !Newer(latest, u.Current) || (s.Failed == latest && now.Sub(s.FailedAt) < RetryFailed) {
		save(u.Home, s)
		return s
	}
	sum, err := u.Stage(ctx, latest)
	if err != nil {
		u.fail(&s, latest, now, err)
		save(u.Home, s)
		return s
	}
	s.Staged, s.StagedSum = latest, sum
	s.Failed, s.FailedAt, s.Err = "", time.Time{}, ""
	log.Info("staged", "version", latest, "sha256", sum)
	save(u.Home, s)
	return s
}

// fail records a failed release and logs it once.
func (u *Updater) fail(s *State, version string, now time.Time, err error) {
	if s.Failed != version || s.Err != err.Error() {
		logx.For("update").Warn("upgrade failed; keeping the current binary", "version", version, "err", err)
	}
	s.Failed, s.FailedAt, s.Err = version, now, err.Error()
}

// Latest is the newest release's tag, from the redirect of /releases/latest
// (no API call, no rate limit), as install.sh finds it.
func (u *Updater) Latest(ctx context.Context) (string, error) {
	c := *u.client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u.base()+"/latest", nil)
	if err != nil {
		return "", err
	}
	if err := authorize(req); err != nil {
		return "", err
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if loc == "" {
		return "", fmt.Errorf("latest release: %s with no redirect", resp.Status)
	}
	tag := loc[strings.LastIndex(loc, "/")+1:]
	if _, ok := Parse(tag); !ok {
		return "", fmt.Errorf("latest release: %q is not a release tag", tag)
	}
	return tag, nil
}

func (u *Updater) stagedPath(version string) string {
	return filepath.Join(Dir(u.Home), "fleet-"+version+exeExt())
}

// Stage downloads release version, checks it against SHA256SUMS and its
// attestation, runs it, and leaves it at <home>/update/fleet-<version>. It
// returns the checked sum. Nothing is staged on any failure.
func (u *Updater) Stage(ctx context.Context, version string) (string, error) {
	asset := Asset(u.target())
	url := u.base() + "/download/" + version + "/"
	sums, err := u.get(ctx, url+"SHA256SUMS", maxSums)
	if err != nil {
		return "", err
	}
	if VerifySums != nil {
		sig, err := u.get(ctx, url+"SHA256SUMS.sig", maxSums)
		if err != nil {
			return "", err
		}
		if err := VerifySums(sums, sig); err != nil {
			return "", fmt.Errorf("SHA256SUMS signature: %w", err)
		}
	}
	want, err := sumFor(sums, asset)
	if err != nil {
		return "", err
	}
	bin, err := u.get(ctx, url+asset, maxBinary)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(bin)
	got := hex.EncodeToString(h[:])
	if got != want {
		return "", fmt.Errorf("checksum mismatch for %s: SHA256SUMS says %s, the download is %s", asset, want, got)
	}
	if err := os.MkdirAll(Dir(u.Home), 0o700); err != nil {
		return "", err
	}
	path := u.stagedPath(version)
	tmp := strings.TrimSuffix(path, exeExt()) + ".part" + exeExt()
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		os.Remove(tmp)
		return "", err
	}
	verify, probe := u.Verify, u.Probe
	if verify == nil && VerifySums != nil {
		verify = func(string) error { return nil } // the signed sums cover it
	}
	if verify == nil {
		verify = Attest
	}
	if probe == nil {
		probe = Probe
	}
	if err := verify(tmp); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := probe(tmp, version); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return got, nil
}

func (u *Updater) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if err := authorize(req); err != nil {
		return nil, err
	}
	resp, err := u.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("download %s: larger than %d bytes", url, limit)
	}
	return data, nil
}

// sumFor finds asset's line in a SHA256SUMS file.
func sumFor(sums []byte, asset string) (string, error) {
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == asset && len(f[0]) == 64 {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("SHA256SUMS has no line for %s", asset)
}

// apply copies the staged release beside Exe and renames it over Exe, so
// the swap is one step even while fleet runs from it. The old binary stays
// hard-linked at <home>/update/fleet-<current>.prev when the filesystem
// allows, for a manual rollback.
func (u *Updater) apply(s *State, now time.Time) error {
	staged := u.stagedPath(s.Staged)
	data, err := os.ReadFile(staged)
	if err != nil {
		return err
	}
	h := sha256.Sum256(data)
	if hex.EncodeToString(h[:]) != s.StagedSum {
		return errors.New("staged binary changed since it was verified")
	}
	fi, err := os.Stat(u.Exe)
	if err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(u.Exe), fmt.Sprintf(".fleet.new.%d%s", os.Getpid(), exeExt()))
	if err := os.WriteFile(tmp, data, fi.Mode().Perm()|0o500); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("cannot write beside %s: %w", u.Exe, err)
	}
	prev := filepath.Join(Dir(u.Home), "fleet-"+u.Current+".prev")
	os.Remove(prev)
	os.Link(u.Exe, prev) // best effort: another filesystem has no hard link
	// Recorded before the rename: the watcher re-execs as soon as it sees
	// the new file, and nothing after the rename may be needed to keep it.
	before := s.Applied
	s.Applied = &Applied{From: u.Current, To: s.Staged, At: now, Exe: u.Exe}
	if err := save(u.Home, *s); err != nil {
		os.Remove(tmp)
		s.Applied = before
		return err
	}
	// Windows renames a running exe but never replaces it: move it aside
	// first. The .old copy goes once nothing runs it, on a later apply.
	old := ""
	if runtime.GOOS == "windows" {
		old = u.Exe + ".old"
		os.Remove(old)
		if err := os.Rename(u.Exe, old); err != nil {
			os.Remove(tmp)
			s.Applied = before
			return err
		}
	}
	if err := os.Rename(tmp, u.Exe); err != nil {
		if old != "" {
			os.Rename(old, u.Exe)
		}
		os.Remove(tmp)
		s.Applied = before
		return err
	}
	os.Remove(staged)
	logx.For("update").Info("upgraded", "from", u.Current, "to", s.Staged, "exe", u.Exe)
	s.Staged, s.StagedSum, s.Failed, s.FailedAt, s.Err = "", "", "", time.Time{}, ""
	return save(u.Home, *s)
}

// Probe runs the binary at path: `version` must name want and `mcp --probe`
// must answer ok, the check every re-exec makes (ADR-25).
func Probe(path, want string) error {
	run := func(args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, path, args...).Output()
		return strings.TrimSpace(string(out)), err
	}
	out, err := run("version")
	if err != nil {
		return fmt.Errorf("%s version: %w", filepath.Base(path), err)
	}
	if out != "fleet "+want && !strings.HasPrefix(out, "fleet "+want+" ") {
		return fmt.Errorf("downloaded binary says %q, want fleet %s", out, want)
	}
	if out, err := run("mcp", "--probe"); err != nil || out != "ok" {
		return fmt.Errorf("downloaded binary failed its probe: %q %v", out, err)
	}
	return nil
}

// Attest checks that the release workflow built the file, with
// `gh attestation verify`, when gh is installed and logged in (ADR-103);
// otherwise the checksum is all there is, as with install.sh.
// FLEET_SKIP_ATTEST=1 skips it.
func Attest(path string) error {
	if os.Getenv("FLEET_SKIP_ATTEST") == "1" {
		return nil
	}
	gh, err := exec.LookPath("gh")
	if err != nil {
		return nil
	}
	if exec.Command(gh, "auth", "token").Run() != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, gh, "attestation", "verify", path, "-R", Repo,
		"--signer-workflow", Repo+"/.github/workflows/release.yml").CombinedOutput()
	if err != nil {
		return fmt.Errorf("gh attestation verify failed: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Announce returns the note an admiral sees once per upgrade on its next
// tool result, "" when there is nothing new for who. running is the build
// this process runs: the note waits until the admiral's server is on it.
func Announce(home, running, who string, now time.Time) string {
	s := Load(home)
	a := s.Applied
	if a == nil || a.To != running || now.Sub(a.At) > AnnounceFor {
		return ""
	}
	if who == "" {
		who = "main"
	}
	mark := filepath.Join(Dir(home), "announced-"+a.To+"-"+strings.ReplaceAll(who, "/", "_"))
	f, err := os.OpenFile(mark, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return ""
	}
	f.Close()
	return fmt.Sprintf("\n[fleet upgraded itself: %s → %s. fleet_changes shows what's new; [update] auto = false in config.toml turns this off.]", a.From, a.To)
}

// Install checks, stages and applies the latest release now, without
// waiting for a quiet moment: `fleet update --now`. It returns the release
// installed, "" when the running one is the latest.
func (u *Updater) Install(ctx context.Context) (string, error) {
	s := Load(u.Home)
	now := u.now()
	s.CheckedAt = now
	latest, err := u.Latest(ctx)
	if err != nil {
		save(u.Home, s)
		return "", err
	}
	s.Latest = latest
	if !Newer(latest, u.Current) {
		save(u.Home, s)
		return "", nil
	}
	sum, err := u.Stage(ctx, latest)
	if err == nil {
		s.Staged, s.StagedSum = latest, sum
		err = u.apply(&s, now)
	}
	if err != nil {
		u.fail(&s, latest, now, err)
		if s.Staged != "" {
			os.Remove(u.stagedPath(s.Staged))
		}
		s.Staged, s.StagedSum = "", ""
		save(u.Home, s)
		return "", err
	}
	u.applied = true
	return latest, nil
}
