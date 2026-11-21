package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/logx"
	"github.com/sturlabragason/fleet/internal/runs"
)

// statsKey picks what fleet stats groups interventions by.
var statsKey = map[string]func(board.Event) string{
	"kind":    func(e board.Event) string { return e.Kind },
	"profile": func(e board.Event) string { return e.Profile },
	"model":   func(e board.Event) string { return e.Model },
}

// cmdStats counts fleet's own interventions from events.jsonl (ADR-154):
// how often a policy acted, grouped by kind, profile or model.
func cmdStats(args []string) error {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	since := fs.String("since", "", "only events since a duration (24h, 7d) or a date (2006-01-02)")
	by := fs.String("by", "", "group by kind, profile or model; with --runs also effort, version, repo, week, playbook, agents_md or route")
	ledger := fs.Bool("runs", false, "score closed runs from runs.jsonl instead of counting interventions (ADR-170)")
	asJSON := fs.Bool("json", false, "machine-readable output: an object of counts, or with --runs a list of rows")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var from time.Time
	if *since != "" {
		t, err := parseSince(*since, time.Now())
		if err != nil {
			return err
		}
		from = t
	}
	if *ledger {
		return statsRuns(os.Stdout, from, *by, *asJSON)
	}
	if *by == "" {
		*by = "kind"
	}
	key, ok := statsKey[*by]
	if !ok {
		return fmt.Errorf("--by %q: want kind, profile or model", *by)
	}
	evs, err := board.NewStore(logx.Home()).ReadEvents(from)
	if err != nil {
		return err
	}
	return writeStats(os.Stdout, countInterventions(evs, key), *asJSON)
}

// countInterventions counts the intervention lines of evs by key; a
// transition line (no kind) and a friction line (ADR-187) are left out, an
// empty key counts as "-".
func countInterventions(evs []board.Event, key func(board.Event) string) map[string]int {
	out := map[string]int{}
	for _, e := range evs {
		if !slices.Contains(board.Kinds, e.Kind) {
			continue
		}
		k := key(e)
		if k == "" {
			k = "-"
		}
		out[k]++
	}
	return out
}

// statsRuns is fleet stats --runs: the run ledger grouped by one field
// (ADR-170), profile when by is "".
func statsRuns(out io.Writer, from time.Time, by string, asJSON bool) error {
	if by == "" {
		by = "profile"
	}
	key, ok := runs.Keys[by]
	if !ok {
		names := slices.Sorted(maps.Keys(runs.Keys))
		return fmt.Errorf("--by %q: want one of %s", by, strings.Join(names, ", "))
	}
	rs, err := runs.Read(logx.Home(), from)
	if err != nil {
		return err
	}
	rows := runs.Summarize(rs, key)
	if asJSON {
		return json.NewEncoder(out).Encode(rows)
	}
	return runs.Write(out, rows)
}

func writeStats(out io.Writer, counts map[string]int, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(out).Encode(counts)
	}
	if len(counts) == 0 {
		_, err := io.WriteString(out, "no interventions recorded\n")
		return err
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return keys[i] < keys[j]
	})
	w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	for _, k := range keys {
		fmt.Fprintf(w, "%s\t%d\n", k, counts[k])
	}
	return w.Flush()
}
