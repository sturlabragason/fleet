package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/fleet"
	"github.com/sturlabragason/fleet/internal/mail"
	"github.com/sturlabragason/fleet/internal/recall"
)

// parseInterspersed parses fs with flags before, between or after the
// positional words, and returns the words.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var words []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if n := len(args) - len(rest); n > 0 && args[n-1] == "--" {
			return append(words, rest...), nil
		}
		args = rest
		if len(args) == 0 {
			return words, nil
		}
		words = append(words, args[0])
		args = args[1:]
	}
}

// cmdRecall is `fleet recall <topic>` (ADR-119).
func cmdRecall(f *fleet.Fleet, args []string) error {
	fs := flag.NewFlagSet("recall", flag.ContinueOnError)
	days := fs.Int("days", 0, "only the last N days")
	limit := fs.Int("n", 10, "hits per section")
	asJSON := fs.Bool("json", false, "print JSON")
	refresh := fs.Bool("refresh", false, "update the cass index first")
	words, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(words) == 0 {
		return errors.New("usage: fleet recall [--days N] [-n 10] [--refresh] [--json] <words...>")
	}
	ctx := context.Background()
	if *refresh {
		if err := recall.Refresh(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "fleet recall:", err)
		}
	}
	bd := recall.FromBoard(f.Store.Load())
	r := recall.Search(ctx, strings.Join(words, " "), recall.Opts{Home: f.Home, Days: *days, Limit: *limit, Agents: bd.Sessions})
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	fmt.Print(r.Format())
	return nil
}

// cmdDay is `fleet day` (ADR-119).
func cmdDay(f *fleet.Fleet, args []string) error {
	fs := flag.NewFlagSet("day", flag.ContinueOnError)
	since := fs.String("since", "24h", "a duration back from now (24h, 3d) or a date (2006-01-02)")
	admiral := fs.String("admiral", board.Main, "whose unread reports")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New("usage: fleet day [--since 24h|3d|2006-01-02] [--admiral NAME]")
	}
	t, err := parseSince(*since, time.Now())
	if err != nil {
		return err
	}
	fmt.Print(recall.DayFor(context.Background(), f.Home, f.Store.Load(), f.Mail().Peek(mail.BoxOf(*admiral)), t))
	return nil
}

// parseSince reads 24h, 3d or a date.
func parseSince(s string, now time.Time) (time.Time, error) {
	if n, ok := strings.CutSuffix(s, "d"); ok {
		var d int
		if _, err := fmt.Sscanf(n, "%d", &d); err == nil && d > 0 {
			return now.AddDate(0, 0, -d), nil
		}
	}
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return now.Add(-d), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("--since %q: want a duration (24h, 3d) or a date (2006-01-02)", s)
}
