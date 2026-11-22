package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// fleet design (ADR-195): print one section or ADR of the repo's DESIGN.md,
// so an agent reads what it needs instead of the whole file, which is
// bigger than a context window.

const designUsage = "usage: fleet design [-C dir] <ADR-N | §x | words>"

var (
	designADR     = regexp.MustCompile(`(?i)^adr-?(\d+)$`)
	designADRLine = regexp.MustCompile(`^\*\*ADR-(\d+)[:.]`)
)

func cmdDesign(args []string) error {
	fs := flag.NewFlagSet("design", flag.ContinueOnError)
	dir := fs.String("C", ".", "start the DESIGN.md search here")
	if err := fs.Parse(args); err != nil || fs.NArg() == 0 {
		return errors.New(designUsage)
	}
	path, err := findDesign(*dir)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read design: %w", err)
	}
	out, err := designSection(string(b), strings.Join(fs.Args(), " "))
	if err != nil {
		return err
	}
	fmt.Print(out)
	return nil
}

// findDesign is the nearest DESIGN.md at or above dir.
func findDesign(dir string) (string, error) {
	d, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("design dir: %w", err)
	}
	for {
		p := filepath.Join(d, "DESIGN.md")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		up := filepath.Dir(d)
		if up == d {
			return "", errors.New("no DESIGN.md here or above")
		}
		d = up
	}
}

// designSection returns the part of text that query names: an ADR
// ("ADR-71", "71") runs to the next ADR or heading; a section ("§7b",
// "7b") or words matching a heading or ADR title run to the next heading
// of the same or a higher level. Words matching more than one place print
// the first and list the others.
func designSection(text, query string) (string, error) {
	lines := strings.Split(text, "\n")
	query = strings.TrimSpace(query)
	q := strings.TrimSpace(strings.TrimPrefix(query, "§"))
	sect := q != query
	if q == "" {
		return "", errors.New(designUsage)
	}
	var starts []int
	if m := designADR.FindStringSubmatch(q); !sect && (m != nil || isDigits(q)) {
		n := q
		if m != nil {
			n = m[1]
		}
		for i, l := range lines {
			if a := designADRLine.FindStringSubmatch(l); a != nil && a[1] == n {
				starts = append(starts, i)
			}
		}
	} else {
		sec := regexp.MustCompile(`^#+ ` + regexp.QuoteMeta(q) + `[. ]`)
		for i, l := range lines {
			if sec.MatchString(l + " ") {
				starts = append(starts, i)
			}
		}
		if len(starts) == 0 {
			starts = designMatches(lines, strings.Fields(strings.ToLower(q)))
		}
	}
	if len(starts) == 0 {
		return "", fmt.Errorf("nothing in DESIGN.md matches %q", query)
	}
	var out strings.Builder
	out.WriteString(strings.Join(lines[starts[0]:designEnd(lines, starts[0])], "\n") + "\n")
	if len(starts) > 1 {
		out.WriteString("\nAlso matching:\n")
		for _, i := range starts[1:] {
			fmt.Fprintf(&out, "  %d: %s\n", i+1, lines[i])
		}
	}
	return out.String(), nil
}

// designMatches are the headings and ADR title lines holding every word.
func designMatches(lines, words []string) []int {
	var out []int
	for i, l := range lines {
		if !strings.HasPrefix(l, "#") && !designADRLine.MatchString(l) {
			continue
		}
		low := strings.ToLower(l)
		all := true
		for _, w := range words {
			all = all && strings.Contains(low, w)
		}
		if all {
			out = append(out, i)
		}
	}
	return out
}

// designEnd is the line after the block that starts at i.
func designEnd(lines []string, i int) int {
	level := headingLevel(lines[i])
	for j := i + 1; j < len(lines); j++ {
		l := lines[j]
		if level == 0 && designADRLine.MatchString(l) {
			return j
		}
		if h := headingLevel(l); h > 0 && (level == 0 || h <= level) {
			return j
		}
	}
	return len(lines)
}

func headingLevel(l string) int {
	n := len(l) - len(strings.TrimLeft(l, "#"))
	if n == 0 || !strings.HasPrefix(l[n:], " ") {
		return 0
	}
	return n
}

func isDigits(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}
