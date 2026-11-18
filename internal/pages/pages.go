// Package pages finds answers the user sent from a decision page that no
// agent took (ADR-163). A decision page is a private web page an agent
// publishes with the `artifact` companion; its Send button stores the
// user's picks on the host until `artifact answer <page>` takes them. An
// agent that reported done, or was closed, never takes them, so the
// resident poll hands each answer that waited Grace to the admiral.
package pages

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/companion"
)

// Spec is the artifact CLI: `artifact list` and `artifact answer --peek`.
var Spec = companion.Register(companion.Spec{
	Name:    "artifact",
	Env:     []string{"FLEET_ARTIFACT"},
	Feature: "decision-page answers reach the admiral",
	Hint:    "the artifact CLI of the artifact-publish skill; fleet only reads with it",
})

// Grace is how long an answer may sit untaken before it is handed on: an
// agent blocked in `artifact answer --wait` takes it within seconds.
var Grace = time.Minute

// prefixes name the pages that carry a Send button.
var prefixes = []string{"decide-", "viz-"}

// Answer is the newest untaken answer of one page.
type Answer struct {
	Page     string  `json:"page"`
	Decision string  `json:"decision"`
	Line     string  `json:"line"`
	T        float64 `json:"t"`
}

// state is home/pages.json: per page, the time of the last answer handed on.
type state struct {
	Seen map[string]float64 `json:"seen"`
}

func statePath(home string) string { return filepath.Join(home, "pages.json") }

// Poll lists the decision pages, peeks at each one's newest untaken answer
// and returns those that waited Grace and were not returned before. It
// records them as handed on before returning, so a caller tells once. The
// first poll on a home only records what is there: answers from before
// fleet watched were handled by hand. A page that can't be read is skipped
// and named in the error; the others still count.
func Poll(ctx context.Context, home string, now time.Time) ([]Answer, error) {
	t, err := companion.Find(Spec)
	if err != nil {
		return nil, fmt.Errorf("find artifact: %w", err)
	}
	out, err := t.Run(ctx, companion.Opts{}, "list")
	if err != nil {
		return nil, fmt.Errorf("list pages: %w", err)
	}
	st, seed, err := load(home)
	if err != nil {
		return nil, err
	}
	seen := map[string]float64{}
	var due []Answer
	var errs []error
	for _, name := range decisionPages(string(out)) {
		seen[name] = st.Seen[name]
		a, ok, err := peek(ctx, t, name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !ok || a.T <= seen[name] || now.Sub(time.Unix(int64(a.T), 0)) < Grace {
			continue
		}
		seen[name] = a.T
		if !seed {
			due = append(due, a)
		}
	}
	if err := save(home, state{Seen: seen}); err != nil {
		return nil, err
	}
	return due, errors.Join(errs...)
}

// decisionPages picks the decide-/viz- names from `artifact list` (name
// first, tab-separated).
func decisionPages(list string) []string {
	var out []string
	for l := range strings.SplitSeq(list, "\n") {
		name, _, _ := strings.Cut(l, "\t")
		for _, p := range prefixes {
			if strings.HasPrefix(name, p) {
				out = append(out, name)
				break
			}
		}
	}
	return out
}

// peek reads name's newest untaken answer without taking it; exit 3 is none.
func peek(ctx context.Context, t companion.Tool, name string) (Answer, bool, error) {
	out, err := t.Run(ctx, companion.Opts{}, "answer", name, "--peek")
	if ee := (*exec.ExitError)(nil); errors.As(err, &ee) && ee.ExitCode() == 3 {
		return Answer{}, false, nil
	}
	if err != nil {
		return Answer{}, false, fmt.Errorf("peek %s: %w", name, err)
	}
	var a Answer
	if err := json.Unmarshal(out, &a); err != nil {
		return Answer{}, false, fmt.Errorf("peek %s: unreadable answer: %w", name, err)
	}
	if a.Page == "" {
		a.Page = name
	}
	return a, true, nil
}

// load reads the state; seed is true when there is none yet.
func load(home string) (state, bool, error) {
	data, err := os.ReadFile(statePath(home))
	if errors.Is(err, os.ErrNotExist) {
		return state{}, true, nil
	}
	if err != nil {
		return state{}, false, fmt.Errorf("read pages state: %w", err)
	}
	var st state
	if err := json.Unmarshal(data, &st); err != nil {
		return state{}, false, fmt.Errorf("parse pages state: %w", err)
	}
	return st, false, nil
}

func save(home string, st state) error {
	data, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("encode pages state: %w", err)
	}
	tmp := statePath(home) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write pages state: %w", err)
	}
	if err := os.Rename(tmp, statePath(home)); err != nil {
		return fmt.Errorf("write pages state: %w", err)
	}
	return nil
}
