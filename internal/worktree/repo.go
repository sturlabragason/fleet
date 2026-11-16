package worktree

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sturlabragason/fleet/internal/conf"
)

// RepoConfigFile is where a repo states the conventions fleet merge and fleet
// adr follow there (ADR-155). Without it they use fleet's own layout.
const RepoConfigFile = ".fleet/repo.toml"

// DefaultMergeCheck is fleet's own gate, the check fleet merge runs when the
// repo names none: it must exit 0 on the rebased branch and again on main.
const DefaultMergeCheck = "make build && go vet ./..." // tests run in CI (ADR-149)

// RepoConfig is one repo's merge and ADR conventions.
type RepoConfig struct {
	MergeCheck string
	// AppendOnly maps each file every parallel agent appends to (a conflict
	// there is mechanical: keep both sides) to whether the replayed commit's
	// lines go above the base's (newest-first) or below (oldest-first).
	AppendOnly map[string]bool
	ADRFile    string         // the file whose entries hold the ADR numbers
	ADREntry   *regexp.Regexp // matches one entry; group 1 is its number
	Found      bool           // the repo has a RepoConfigFile
}

// DefaultRepoConfig is fleet's own layout (ADR-48, ADR-50): CHANGELOG.md
// newest-first at the top of today's section, DESIGN.md §14 ADRs ascending as
// bold paragraphs, so a reference inside a line ("Amends ADR-29") is no entry.
func DefaultRepoConfig() RepoConfig {
	return RepoConfig{
		MergeCheck: DefaultMergeCheck,
		AppendOnly: map[string]bool{"CHANGELOG.md": true, "DESIGN.md": false},
		ADRFile:    "DESIGN.md",
		ADREntry:   regexp.MustCompile(`(?m)^\*\*ADR-([0-9]+)\b`),
	}
}

// LoadRepoConfig reads RepoConfigFile at the top of the repo holding dir. A
// setting the file leaves out keeps its default; no file gives the defaults.
func LoadRepoConfig(dir string) (RepoConfig, error) {
	c := DefaultRepoConfig()
	top, err := TopLevel(dir)
	if err != nil {
		return c, err
	}
	b, err := os.ReadFile(filepath.Join(top, RepoConfigFile))
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, fmt.Errorf("read %s: %w", RepoConfigFile, err)
	}
	if err := c.parse(string(b)); err != nil {
		return DefaultRepoConfig(), fmt.Errorf("%s: %w", RepoConfigFile, err)
	}
	c.Found = true
	return c, nil
}

func (c *RepoConfig) parse(text string) error {
	// conf has no arrays of tables: parse each [[append_only]] block alone.
	main, blocks, err := splitTables(text, "[[append_only]]")
	if err != nil {
		return err
	}
	d, err := conf.Parse(main)
	if err != nil {
		return err
	}
	if s := conf.Str(d[""], "merge_check"); s != "" {
		c.MergeCheck = s
	}
	if s := conf.Str(d["adr"], "file"); s != "" {
		c.ADRFile = s
	}
	if s := conf.Str(d["adr"], "entry"); s != "" {
		re, err := regexp.Compile(s)
		if err != nil {
			return fmt.Errorf("adr.entry: %w", err)
		}
		if re.NumSubexp() != 1 {
			return fmt.Errorf("adr.entry %q: want one group (the number), got %d", s, re.NumSubexp())
		}
		c.ADREntry = re
	}
	if len(blocks) > 0 {
		c.AppendOnly = map[string]bool{}
	}
	for _, blk := range blocks {
		t, err := conf.Parse(blk)
		if err != nil {
			return fmt.Errorf("append_only: %w", err)
		}
		p := filepath.ToSlash(filepath.Clean(conf.Str(t[""], "path")))
		if p == "." {
			return errors.New("append_only: path is required")
		}
		switch o := conf.Str(t[""], "order"); o {
		case "newest-first":
			c.AppendOnly[p] = true
		case "oldest-first":
			c.AppendOnly[p] = false
		default:
			return fmt.Errorf("append_only %s: order %q, want newest-first or oldest-first", p, o)
		}
	}
	return nil
}

// splitTables returns text without its header blocks, and each block's body.
// A block runs until the next [header]; any other [[table]] is an error.
func splitTables(text, header string) (rest string, blocks []string, err error) {
	var main []string
	cur := -1
	for l := range strings.SplitSeq(text, "\n") {
		t, _, _ := strings.Cut(l, "#")
		t = strings.TrimSpace(t)
		switch {
		case t == header:
			blocks = append(blocks, "")
			cur = len(blocks) - 1
			continue
		case strings.HasPrefix(t, "[["):
			return "", nil, fmt.Errorf("%s: only %s is supported", t, header)
		case strings.HasPrefix(t, "["):
			cur = -1
		}
		if cur >= 0 {
			blocks[cur] += l + "\n"
		} else {
			main = append(main, l)
		}
	}
	return strings.Join(main, "\n"), blocks, nil
}
