package burn

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/companion"
	"github.com/sturlabragason/fleet/internal/prefs"
)

// AgySpec declares the agy companion binary used to read quota snapshots.
var AgySpec = companion.Register(companion.Spec{
	Name:    "agy",
	Bins:    []string{"agy"},
	Env:     []string{"FLEET_AGY"},
	Feature: "read agy quota for fleet burn",
	Hint:    "install agy CLI",
})

// AgyReader reads quota snapshots from `agy -p /quota`.
type AgyReader struct{}

// Name returns the reader identifier.
func (AgyReader) Name() string { return "agy" }

// Read executes `agy -p /quota` via companion and parses the reported windows.
func (r AgyReader) Read(ctx context.Context, profile string, _ prefs.BurnProfile, env map[string]string) Quota {
	now := time.Now().UTC()
	q := Quota{
		Profile:   profile,
		Reader:    r.Name(),
		FetchedAt: now,
	}

	tool, err := companion.Find(AgySpec)
	if err != nil {
		q.Available = false
		q.Error = err.Error()
		if errors.Is(err, companion.ErrMissing) || errors.Is(err, companion.ErrOff) {
			q.Hint = AgySpec.Hint
		}
		return q
	}

	out, err := tool.Run(ctx, companion.Opts{
		Timeout: 15 * time.Second,
		Env:     mapToEnvSlice(env),
	}, "-p", "/quota")
	if err != nil {
		q.Available = false
		q.Error = err.Error()
		return q
	}

	windows, err := ParseAgyQuota(string(out), now)
	if err != nil {
		q.Available = false
		q.Error = err.Error()
		return q
	}

	q.Available = true
	q.Windows = windows
	return q
}

var (
	agyPctRe        = regexp.MustCompile(`(\d+(?:\.\d+)?)%`)
	agyMultiSpaceRe = regexp.MustCompile(`\s{2,}`)
)

var agyFixedZones = map[string]*time.Location{
	"UTC":  time.UTC,
	"GMT":  time.UTC,
	"Z":    time.UTC,
	"CET":  time.FixedZone("CET", 1*3600),
	"CEST": time.FixedZone("CEST", 2*3600),
	"WET":  time.FixedZone("WET", 0),
	"WEST": time.FixedZone("WEST", 1*3600),
	"EET":  time.FixedZone("EET", 2*3600),
	"EEST": time.FixedZone("EEST", 3*3600),
	"EST":  time.FixedZone("EST", -5*3600),
	"EDT":  time.FixedZone("EDT", -4*3600),
	"CST":  time.FixedZone("CST", -6*3600),
	"CDT":  time.FixedZone("CDT", -5*3600),
	"MST":  time.FixedZone("MST", -7*3600),
	"MDT":  time.FixedZone("MDT", -6*3600),
	"PST":  time.FixedZone("PST", -8*3600),
	"PDT":  time.FixedZone("PDT", -7*3600),
}

// ParseAgyQuota parses the text output of `agy -p /quota` into quota windows.
func ParseAgyQuota(output string, now time.Time) ([]Window, error) {
	var windows []Window
	for raw := range strings.SplitSeq(output, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		loc := agyPctRe.FindStringSubmatchIndex(line)
		if loc == nil {
			continue
		}
		pct, err := strconv.ParseFloat(line[loc[2]:loc[3]], 64)
		if err != nil {
			return nil, fmt.Errorf("parse agy percentage %q: %w", line[loc[2]:loc[3]], err)
		}

		group, label := splitAgyGroupLabel(line[:loc[0]])
		resetAt, err := parseAgyResetTime(strings.TrimSpace(line[loc[1]:]), now)
		if err != nil {
			return nil, fmt.Errorf("parse agy reset time in %q: %w", line, err)
		}

		windows = append(windows, Window{
			Group:        group,
			Kind:         classifyWindowKind(label),
			Label:        label,
			RemainingPct: pct,
			ResetAt:      resetAt,
		})
	}
	if len(windows) == 0 {
		return nil, errors.New("no quota windows found in agy output")
	}
	return windows, nil
}

func splitAgyGroupLabel(left string) (string, string) {
	trimmed := strings.TrimSpace(left)
	if trimmed == "" {
		return "", ""
	}
	parts := agyMultiSpaceRe.Split(trimmed, -1)
	if len(parts) < 2 {
		return "", trimmed
	}
	group := strings.TrimSpace(parts[0])
	label := strings.TrimSpace(strings.Join(parts[1:], " "))
	return group, label
}

func parseAgyResetTime(s string, now time.Time) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}

	fields := strings.Fields(s)
	switch len(fields) {
	case 2:
		loc := time.UTC
		if !now.IsZero() {
			loc = now.Location()
		}
		t, err := time.ParseInLocation("2006-01-02 15:04", fields[0]+" "+fields[1], loc)
		if err != nil {
			return time.Time{}, err
		}
		return t.UTC(), nil
	case 3:
		tz := strings.ToUpper(fields[2])
		if loc, ok := agyFixedZones[tz]; ok {
			t, err := time.ParseInLocation("2006-01-02 15:04", fields[0]+" "+fields[1], loc)
			if err != nil {
				return time.Time{}, err
			}
			return t.UTC(), nil
		}
		if loc, err := time.LoadLocation(fields[2]); err == nil {
			t, err := time.ParseInLocation("2006-01-02 15:04", fields[0]+" "+fields[1], loc)
			if err != nil {
				return time.Time{}, err
			}
			return t.UTC(), nil
		}
		t, err := time.Parse("2006-01-02 15:04 MST", s)
		if err != nil {
			return time.Time{}, err
		}
		return t.UTC(), nil
	default:
		return time.Time{}, fmt.Errorf("unrecognized timestamp %q", s)
	}
}

func classifyWindowKind(label string) string {
	lower := strings.ToLower(label)
	switch {
	case strings.Contains(lower, "five hour") || strings.Contains(lower, "5h") || strings.Contains(lower, "5-hour") || strings.Contains(lower, "5 hour") || strings.Contains(lower, "five_hour"):
		return "5h"
	case strings.Contains(lower, "weekly") || strings.Contains(lower, "7d"):
		return "weekly"
	case strings.Contains(lower, "daily") || strings.Contains(lower, "24h"):
		return "daily"
	default:
		return "other"
	}
}

func mapToEnvSlice(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}
