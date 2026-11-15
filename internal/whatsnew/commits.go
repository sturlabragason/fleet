package whatsnew

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// module is the go.mod module line that marks fleet's own repo.
const module = "module github.com/sturlabragason/fleet"

// executable finds the running binary; tests replace it.
var executable = os.Executable

// Repo is fleet's source repo when the binary runs as <repo>/bin/fleet,
// else "".
func Repo() string {
	exe, err := executable()
	if err != nil {
		return ""
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return ""
	}
	bin := filepath.Dir(exe)
	if filepath.Base(bin) != "bin" {
		return ""
	}
	repo := filepath.Dir(bin)
	f, err := os.Open(filepath.Join(repo, "go.mod"))
	if err != nil {
		return ""
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	if !s.Scan() || strings.TrimSpace(s.Text()) != module {
		return ""
	}
	return repo
}

var featRe = regexp.MustCompile(`^feat(\([^)]*\))?!?:`)

func git(repo string, args ...string) (string, error) {
	c := exec.Command("git", append([]string{"-C", repo}, args...)...)
	c.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	b, err := c.Output()
	return string(b), err
}

// Commits are the feat commits on repo's main (or master) that curated
// does not cover: not cited by hash, not touching CHANGELOG.md, and not
// older than the newest curated day.
func Commits(repo string, curated []Entry) ([]Entry, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, err
	}
	ref := "refs/heads/main"
	if _, err := git(repo, "rev-parse", "--verify", "-q", ref); err != nil {
		ref = "refs/heads/master"
		if _, err := git(repo, "rev-parse", "--verify", "-q", ref); err != nil {
			return nil, nil //nolint:nilerr // a repo with neither main nor master has no commits to list
		}
	}
	// The shown day is the committer day in local time, the same date
	// --since filters on, so a rebased old commit can't land on an old day.
	args := []string{"log", ref, "--no-merges", "--format=%x1e%H%x1f%cd%x1f%s", "--date=format-local:" + "%Y-%m-%d", "--name-only"}
	if len(curated) > 0 {
		newest := curated[0].Date
		for _, e := range curated {
			newest = max(newest, e.Date)
		}
		d, err := time.ParseInLocation(DateLayout, newest, time.Local)
		if err != nil {
			return nil, err
		}
		args = append(args, "--since="+d.Format(time.RFC3339))
	}
	args = append(args, "--")
	out, err := git(repo, args...)
	if err != nil {
		return nil, err
	}
	var cited []string
	for _, e := range curated {
		cited = append(cited, e.Hashes...)
	}
	var es []Entry
	for rec := range strings.SplitSeq(out, "\x1e") {
		head, files, _ := strings.Cut(rec, "\n")
		f := strings.Split(head, "\x1f")
		if len(f) != 3 || len(f[0]) < 7 || !featRe.MatchString(f[2]) || isCited(f[0], cited) || touches(files, "CHANGELOG.md") {
			continue
		}
		short := f[0][:7] // fixed length, so the id doesn't change with core.abbrev
		es = append(es, Entry{Date: f[1], Text: f[2], Refs: []string{short}, Hashes: []string{short}, Commit: true})
	}
	return es, nil
}

func isCited(full string, cited []string) bool {
	for _, c := range cited {
		if strings.HasPrefix(full, strings.ToLower(c)) {
			return true
		}
	}
	return false
}

func touches(files, name string) bool {
	for f := range strings.SplitSeq(files, "\n") {
		if strings.TrimSpace(f) == name {
			return true
		}
	}
	return false
}
