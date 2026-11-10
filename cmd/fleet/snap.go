package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/status"
	"github.com/sturlabragason/fleet/internal/tmux"
)

// testdataDir is where snap writes, relative to the repo root.
const testdataDir = "internal/status/testdata"

// snapStates are the state names a fixture may start with: the file name is
// <state>[-<variant>].txt and status_test.go expects <state> back.
var snapStates = []status.Status{status.Working, status.Waiting, status.Idle, status.Error, status.Stuck}

// cmdSnap writes an agent's current pane to internal/status/testdata/<set>/<state>.txt,
// the fixture corpus the status table test reads (DESIGN §4).
func cmdSnap(f *fleet.Fleet, args []string) error {
	fs := flag.NewFlagSet("snap", flag.ContinueOnError)
	root := fs.String("C", "", "repo root (default: the nearest parent with "+testdataDir+")")
	set := fs.String("set", "", "pattern set to file it under (default: the agent's)")
	socket := fs.String("socket", "", "tmux server of a %pane argument (fleet-test-* only)")
	a, err := parseInterleaved(fs, args)
	if err != nil {
		return err
	}
	if len(a) != 2 {
		return fmt.Errorf("usage: fleet snap [-C dir] [--set NAME] [--socket S] <agent | %%pane> <state>")
	}
	name, state := a[0], a[1]
	if err := checkState(state); err != nil {
		return err
	}
	if *socket != "" && !strings.HasPrefix(name, "%") {
		return fmt.Errorf("--socket only applies to a %%pane argument; a board agent is on fleet's own server")
	}
	text, err := snapText(f, name, socket, set)
	if err != nil {
		return err
	}
	if _, ok := f.Patterns[*set]; !ok {
		return fmt.Errorf("no pattern set %q; snap files a fixture under an existing set so the table test reads it", *set)
	}
	dir, err := snapDir(*root)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, *set, state+".txt")
	if err := writeSnap(path, text); err != nil {
		return err
	}
	fmt.Println(path)
	got, ok := f.Patterns[*set].Detect(text)
	if !ok {
		got = "unknown"
	}
	if string(got) != strings.SplitN(state, "-", 2)[0] {
		fmt.Printf("warning: it reads as %s, not %s; add a [%s] pattern so the test passes\n", got, state, *set)
	}
	return nil
}

// snapText captures the pane of a board agent, or of a raw pane id when name
// starts with %. It fills *set with the agent's pattern set when unset.
func snapText(f *fleet.Fleet, name string, socket, set *string) (string, error) {
	if strings.HasPrefix(name, "%") {
		if *set == "" {
			return "", fmt.Errorf("a %%pane argument needs --set")
		}
		t := f.T
		if *socket != "" {
			if !strings.HasPrefix(*socket, "fleet-test-") {
				return "", fmt.Errorf("--socket is for scratch servers: it must start with fleet-test-")
			}
			t = tmux.New(*socket)
		}
		return t.Capture(name, 0)
	}
	if ag, ok := f.Store.Load().Agents[name]; ok && ag.ACP() {
		return "", fmt.Errorf("agent %q runs over ACP: it has no screen to snap (fleet peek shows its transcript)", name)
	}
	if *set == "" {
		ag, ok := f.Store.Load().Agents[name]
		if !ok {
			return "", fmt.Errorf("no agent %q on the board", name)
		}
		if *set = ag.Patterns; *set == "" {
			return "", fmt.Errorf("agent %q has no pattern set; pass --set", name)
		}
	}
	return f.Peek(name, 0)
}

// stateName keeps a state to <head>-<variant> words, so it can only ever
// name a file inside the testdata dir.
var stateName = regexp.MustCompile(`^[a-z]+(-[a-z0-9]+)*$`)

func checkState(state string) error {
	if !stateName.MatchString(state) {
		return fmt.Errorf("state %q must be lower-case words joined by -", state)
	}
	head := status.Status(strings.SplitN(state, "-", 2)[0])
	for _, s := range snapStates {
		if head == s {
			return nil
		}
	}
	var names []string
	for _, s := range snapStates {
		names = append(names, string(s))
	}
	return fmt.Errorf("state %q must start with one of %s (optionally <state>-<variant>)", state, strings.Join(names, ", "))
}

// snapDir returns <root>/internal/status/testdata, looking up from the working
// directory when root is empty.
func snapDir(root string) (string, error) {
	if root != "" {
		d := filepath.Join(root, testdataDir)
		if _, err := os.Stat(d); err != nil {
			return "", fmt.Errorf("no %s under %s", testdataDir, root)
		}
		return d, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for d := wd; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, testdataDir)); err == nil {
			return filepath.Join(d, testdataDir), nil
		}
		if filepath.Dir(d) == d {
			return "", fmt.Errorf("no %s above %s; run snap in the fleet checkout or pass -C", testdataDir, wd)
		}
	}
}

// writeSnap stores the capture the way the fixtures are kept: no trailing
// spaces, no trailing blank lines, one final newline.
func writeSnap(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return fmt.Errorf("the pane is empty; nothing to snap")
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}
