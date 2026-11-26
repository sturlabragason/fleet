// Package plan reads a fleet run plan (ADR-203): tasks with a profile, dir,
// prompt, optional done-check, `after` dependencies and an optional review
// stage, written in the TOML subset internal/conf parses:
//
//	[task.schema]
//	profile = "claude"
//	dir = "~/git/app"
//	prompt = "add the users table"
//	check = "make test"
//
//	[task.api]
//	profile = "copilot"
//	dir = "~/git/app"
//	prompt = "serve /users"
//	after = ["schema"]
//	review = { profile = "claude", max_rounds = 2 }
//
// The state of a running plan lives in a JSON file next to it (state.go).
package plan

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/sturlabragason/fleet/internal/conf"
)

// DefaultRounds is how many review rounds a task gets when its review sets
// no max_rounds.
const DefaultRounds = 2

// Review is a task's review stage: a reviewer on another vendor reads the
// writer's committed branch, up to MaxRounds times.
type Review struct {
	Profile   string `json:"profile"`
	MaxRounds int    `json:"max_rounds"`
}

// Task is one agent's work in a plan.
type Task struct {
	Name    string   `json:"name"`
	Profile string   `json:"profile"`
	Dir     string   `json:"dir"`
	Prompt  string   `json:"prompt"`
	Check   string   `json:"check,omitempty"`
	After   []string `json:"after,omitempty"`
	Review  *Review  `json:"review,omitempty"`
}

// Reviewer is the agent name of the task's reviewer.
func (t Task) Reviewer() string { return t.Name + "-review" }

// Plan is a parsed plan file; Tasks are sorted by name.
type Plan struct {
	Name  string `json:"name"`
	Tasks []Task `json:"tasks"`
}

// Task returns the task called name, or nil.
func (p *Plan) Task(name string) *Task {
	for i := range p.Tasks {
		if p.Tasks[i].Name == name {
			return &p.Tasks[i]
		}
	}
	return nil
}

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ParseFile reads a plan file; the plan is named after the file.
func ParseFile(path string) (*Plan, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read plan: %w", err)
	}
	return Parse(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), string(b))
}

// Parse reads plan text and checks it: valid names, known keys, every
// dependency a task of the plan, and no cycle.
func Parse(name, text string) (*Plan, error) {
	if !validName.MatchString(name) {
		return nil, fmt.Errorf("plan name %q: use lower-case letters, digits and -", name)
	}
	d, err := conf.Parse(text)
	if err != nil {
		return nil, fmt.Errorf("plan %s: %w", name, err)
	}
	p := &Plan{Name: name}
	for sec, kv := range d {
		if sec == "" && len(kv) == 0 {
			continue
		}
		tn, ok := strings.CutPrefix(sec, "task.")
		if !ok {
			return nil, fmt.Errorf("plan %s: unknown section [%s]; tasks are [task.<name>]", name, sec)
		}
		t, err := parseTask(tn, kv)
		if err != nil {
			return nil, fmt.Errorf("plan %s: %w", name, err)
		}
		p.Tasks = append(p.Tasks, t)
	}
	if len(p.Tasks) == 0 {
		return nil, fmt.Errorf("plan %s has no [task.<name>] sections", name)
	}
	slices.SortFunc(p.Tasks, func(a, b Task) int { return strings.Compare(a.Name, b.Name) })
	if err := p.check(); err != nil {
		return nil, fmt.Errorf("plan %s: %w", name, err)
	}
	return p, nil
}

var taskKeys = []string{"profile", "dir", "prompt", "check", "after", "review"}

func parseTask(name string, kv map[string]conf.Value) (Task, error) {
	if !validName.MatchString(name) {
		return Task{}, fmt.Errorf("task name %q: use lower-case letters, digits and -", name)
	}
	for k := range kv {
		if !slices.Contains(taskKeys, k) {
			return Task{}, fmt.Errorf("task %s: unknown key %q (known: %s)", name, k, strings.Join(taskKeys, ", "))
		}
	}
	t := Task{Name: name, Profile: conf.Str(kv, "profile"), Dir: conf.Str(kv, "dir"),
		Prompt: conf.Str(kv, "prompt"), Check: conf.Str(kv, "check"), After: conf.List(kv, "after")}
	if v, ok := kv["after"]; ok {
		if _, isList := v.([]string); !isList {
			return Task{}, fmt.Errorf("task %s: after is a list: after = [\"other-task\"]", name)
		}
	}
	if len(t.After) == 0 {
		t.After = nil // after = [] and no after resume as the same plan
	}
	if t.Profile == "" || t.Dir == "" || t.Prompt == "" {
		return Task{}, fmt.Errorf("task %s needs profile, dir and prompt", name)
	}
	if _, ok := kv["review"]; !ok {
		return t, nil
	}
	r := conf.Map(kv, "review")
	if r == nil || r["profile"] == "" {
		return Task{}, fmt.Errorf("task %s: review = { profile = \"...\", max_rounds = 2 }", name)
	}
	t.Review = &Review{Profile: r["profile"], MaxRounds: DefaultRounds}
	if s := r["max_rounds"]; s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			return Task{}, fmt.Errorf("task %s: max_rounds %q is not a positive number", name, s)
		}
		t.Review.MaxRounds = n
	}
	return t, nil
}

// check refuses a task named like another task's reviewer, an unknown
// dependency and a cycle.
func (p *Plan) check() error {
	for _, t := range p.Tasks {
		if t.Review != nil && p.Task(t.Reviewer()) != nil {
			return fmt.Errorf("task %s clashes with the reviewer of task %s", t.Reviewer(), t.Name)
		}
	}
	const (
		visiting = iota + 1
		done
	)
	mark := map[string]int{}
	var visit func(n string, path []string) error
	visit = func(n string, path []string) error {
		switch mark[n] {
		case visiting:
			return fmt.Errorf("dependency cycle: %s", strings.Join(append(path, n), " -> "))
		case done:
			return nil
		}
		mark[n] = visiting
		for _, d := range p.Task(n).After {
			if p.Task(d) == nil {
				return fmt.Errorf("task %s is after %q, which is not a task of the plan", n, d)
			}
			if err := visit(d, append(path, n)); err != nil {
				return err
			}
		}
		mark[n] = done
		return nil
	}
	for _, t := range p.Tasks {
		if err := visit(t.Name, nil); err != nil {
			return err
		}
	}
	return nil
}

// ResolveDirs makes every task dir absolute: ~/ is the home directory and
// a relative dir is taken from base (the plan file's directory). base ""
// refuses relative dirs.
func (p *Plan) ResolveDirs(base string) error {
	home, _ := os.UserHomeDir()
	for i := range p.Tasks {
		d := p.Tasks[i].Dir
		if rest, ok := strings.CutPrefix(d, "~/"); ok && home != "" {
			d = filepath.Join(home, rest)
		}
		if !filepath.IsAbs(d) {
			if base == "" {
				return fmt.Errorf("task %s: dir %q must be absolute", p.Tasks[i].Name, d)
			}
			d = filepath.Join(base, d)
		}
		p.Tasks[i].Dir = filepath.Clean(d)
	}
	return nil
}
