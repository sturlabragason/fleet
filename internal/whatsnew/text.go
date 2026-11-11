package whatsnew

import (
	"fmt"
	"strings"

	"github.com/sturlabragason/fleet/internal/logx"
)

// NewMark flags an unseen entry. Not Peach ●, which means waiting.
const NewMark = "✦"

// NewIDs is the set of ids of es that are not in the seen file.
func NewIDs(es []Entry) map[string]bool {
	seen, err := LoadSeen()
	if err != nil {
		logx.For("whatsnew").Warn("load seen", "err", err)
	}
	isNew := map[string]bool{}
	for _, e := range es {
		if !seen[e.ID()] {
			isNew[e.ID()] = true
		}
	}
	return isNew
}

// Text is the terminal list of `fleet changes` and fleet_changes: the
// version line, then "date  ✦  text  (refs)" with the date on a day's first
// line only.
func Text(version string, es []Entry, isNew map[string]bool) string {
	var b strings.Builder
	b.WriteString(Header(version, es, isNew) + "\n")
	if len(es) == 0 {
		b.WriteString("no entries\n")
	}
	day := ""
	for _, e := range es {
		date := strings.Repeat(" ", len(DateLayout))
		if e.Date != day {
			date, day = e.Date, e.Date
		}
		mark := " "
		if isNew[e.ID()] {
			mark = NewMark
		}
		line := date + "  " + mark + "  " + Plain(e.Text)
		if len(e.Refs) > 0 {
			line += "  (" + strings.Join(e.Refs, ", ") + ")"
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// Header is the version line, with "· N new" when something is new.
func Header(version string, es []Entry, isNew map[string]bool) string {
	n := 0
	for _, e := range es {
		if isNew[e.ID()] {
			n++
		}
	}
	if n > 0 {
		return fmt.Sprintf("%s · %d new", version, n)
	}
	return version
}
