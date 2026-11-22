package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sturlabragason/fleet/internal/sysx"
)

// DefaultBuildTimeout caps a configured [update] build_command pass.
const DefaultBuildTimeout = 10 * time.Minute

// versionMatches reports whether `fleet version` output matches tag want,
// allowing either a trailing space/build details ("fleet v1.2.3 (...)") or
// valid SemVer build metadata ("fleet v1.2.3+custom.abc").
func versionMatches(out, want string) bool {
	rest, ok := strings.CutPrefix(out, "fleet ")
	if !ok {
		return false
	}
	tok, _, _ := strings.Cut(rest, " ")
	if tok == want {
		return true
	}
	return strings.HasPrefix(tok, want+"+") && sameRelease(tok, want)
}

// sameRelease reports whether a and b refer to the same vX.Y.Z release tag,
// ignoring SemVer build metadata (+...).
func sameRelease(a, b string) bool {
	if a == b {
		return true
	}
	pa, ok1 := Parse(a)
	pb, ok2 := Parse(b)
	return ok1 && ok2 && pa == pb
}

const defaultRepo = "sturlabragason/fleet"

func (u *Updater) hasCustomBase() bool {
	return u.BaseURL != "" || os.Getenv("FLEET_UPDATE_URL") != "" || DefaultBaseURL != "" || Repo != defaultRepo
}

// stageBuild runs u.BuildCommand to build release version into tmp, checks that
// the output is a non-empty regular file within maxBinary, probes it, and
// atomically moves it to path.
func (u *Updater) stageBuild(ctx context.Context, version, path, tmp string) (string, error) {
	if !tagRE.MatchString(version) {
		return "", fmt.Errorf("version %q is not a release tag", version)
	}
	if err := os.MkdirAll(Dir(u.Home), 0o700); err != nil {
		return "", err
	}
	os.Remove(tmp)

	timeout := u.BuildTimeout
	if timeout <= 0 {
		timeout = DefaultBuildTimeout
	}
	bctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := sysx.ShellCommand(bctx, u.BuildCommand)
	cmd.Dir = u.Home // fixed, never the folder (often an agent worktree) the watcher was started in
	sysx.KillGroupOnCancel(cmd)
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = append(os.Environ(),
		"FLEET_UPDATE_VERSION="+version,
		"FLEET_UPDATE_OUT="+tmp,
		"FLEET_UPDATE_CURRENT="+u.Current,
		"FLEET_UPDATE_EXE="+u.Exe,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		os.Remove(tmp)
		if tail := tailOutput(out, 512); tail != "" {
			return "", fmt.Errorf("build_command failed: %w: %s", err, tail)
		}
		return "", fmt.Errorf("build_command failed: %w", err)
	}

	fi, err := os.Lstat(tmp)
	if err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("build_command did not produce %s: %w", tmp, err)
	}
	if !fi.Mode().IsRegular() {
		os.Remove(tmp)
		return "", fmt.Errorf("build_command output %s is not a regular file", tmp)
	}
	if fi.Size() <= 0 || fi.Size() > maxBinary {
		os.Remove(tmp)
		return "", fmt.Errorf("build_command output size %d is invalid", fi.Size())
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(tmp, 0o755); err != nil {
			os.Remove(tmp)
			return "", fmt.Errorf("chmod build output: %w", err)
		}
	}

	got, err := fileSHA256(tmp)
	if err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("hash build output: %w", err)
	}

	probe := u.Probe
	if probe == nil {
		probe = Probe
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

// tryLock takes a non-blocking exclusive lock on <home>/update.lock so two
// processes (for example, the watcher and `fleet update --now`) do not stage or
// swap concurrently. Kept in Home rather than Dir(Home) so update/ holds only
// state and staged downloads.
func (u *Updater) tryLock() (func(), bool, error) {
	if err := os.MkdirAll(u.Home, 0o700); err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(filepath.Join(u.Home, "update.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := sysx.Flock(f, sysx.LockEx|sysx.LockNB); err != nil {
		f.Close()
		return nil, false, nil //nolint:nilerr // non-blocking lock held by another updater
	}
	return func() {
		sysx.Unlock(f)
		f.Close()
	}, true, nil
}

func tailOutput(b []byte, maxBytes int) string {
	s := strings.TrimSpace(string(b))
	if len(s) <= maxBytes {
		return s
	}
	i := len(s) - maxBytes
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	return "..." + s[i:]
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
