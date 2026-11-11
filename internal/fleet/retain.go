package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/acphost"
	"github.com/sturlabragason/fleet/internal/logx"
)

// ACPKeep is how long the acp-host files (transcript, stderr, queued
// prompts, socket) of a name fleet no longer knows are kept after their
// last write (ADR-113). A name on the board (live, parked or queued) or
// with a closed record keeps its files whatever their age, so nothing
// resumable loses them. The tool keeps its own copy of each session.
var ACPKeep = 7 * 24 * time.Hour

// retainEvery is how often the resident poll looks for old files.
const retainEvery = time.Hour

var acpSuffixes = []string{".jsonl", ".stderr", ".prompt", ".sock"}

// retain is the resident poll's hourly retention pass.
func (f *Fleet) retain(now time.Time) {
	if !f.retainedAt.IsZero() && now.Sub(f.retainedAt) < retainEvery {
		return
	}
	f.retainedAt = now
	f.pruneACP(now)
}

// pruneACP removes acp-host files of names that are neither agents nor
// closed records and have not been written for ACPKeep.
func (f *Fleet) pruneACP(now time.Time) int {
	dir := acphost.Dir(f.Home)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	b, err := f.Store.LoadChecked()
	if err != nil {
		logx.For("fleet").Warn("retention: skipped, the board cannot be read", "err", err)
		return 0
	}
	known := map[string]bool{}
	for n := range b.Agents {
		known[n] = true
	}
	for _, c := range b.ClosedRecords() {
		known[c.Agent.Name] = true
	}
	n := 0
	for _, e := range ents {
		name := ""
		for _, s := range acpSuffixes {
			if base, ok := strings.CutSuffix(e.Name(), s); ok {
				name = base
				break
			}
		}
		if name == "" || known[name] || e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) < ACPKeep {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
			logx.For("fleet").Warn("retention: remove acp file", "file", e.Name(), "err", err)
			continue
		}
		n++
	}
	if n > 0 {
		logx.For("fleet").Info("retention: removed acp files of gone agents", "files", n, "older_than", ACPKeep)
	}
	return n
}
