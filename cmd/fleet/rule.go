package main

import (
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"

	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/rules"
	"github.com/sturlabragason/fleet/internal/supervisor"
)

const ruleUsage = `usage: fleet rule add|ls|rm [--repo DIR] ["text" | N]`

// cmdRule edits the standing rules every spawn's playbook carries: the
// user's layer, or with --repo the repo's .fleet/worker.md (ADR-131).
func cmdRule(args []string) error {
	if len(args) == 0 {
		return errors.New(ruleUsage)
	}
	fs := flag.NewFlagSet("rule", flag.ContinueOnError)
	repo := fs.String("repo", "", "a rule for one repo: its .fleet/worker.md")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	home := logx.Home()
	path, where := rules.UserFile(home), "every spawn"
	if *repo != "" {
		p, err := rules.RepoFile(*repo)
		if err != nil {
			return err
		}
		path, where = p, "every spawn in that repo"
	}
	rest := strings.Join(fs.Args(), " ")
	switch args[0] {
	case "add":
		r, added, err := rules.Add(path, rest)
		if err != nil {
			return err
		}
		if !added {
			fmt.Printf("already a rule in %s: %s\n", path, r)
			return nil
		}
		fmt.Printf("added to %s; now in %s\n", path, where)
		return supervisor.AppendNote(home, "", "rule added ("+path+"): "+r)
	case "ls":
		rs, err := rules.List(path)
		if err != nil {
			return err
		}
		if len(rs) == 0 {
			fmt.Printf("no rules in %s\n", path)
		}
		for i, r := range rs {
			fmt.Printf("%d. %s\n", i+1, r)
		}
		return nil
	case "rm":
		n, err := strconv.Atoi(strings.TrimSpace(rest))
		if err != nil {
			return errors.New(ruleUsage)
		}
		r, err := rules.Remove(path, n)
		if err != nil {
			return err
		}
		fmt.Printf("removed from %s: %s\n", path, r)
		return supervisor.AppendNote(home, "", "rule removed ("+path+"): "+r)
	}
	return errors.New(ruleUsage)
}
