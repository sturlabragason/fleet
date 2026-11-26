// Package retro is fleet's trace-to-improvement loop (ADR-204): a report
// that reads the run ledger, friction events and the retro: lines of reports
// and lists candidate playbook patches, and a trial that A/B tests one patch
// as a variant playbook layer on a share of new spawns. A human starts a
// trial and promotes it; the ledger gates the promotion. Nothing here
// changes a playbook on its own.
package retro

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sturlabragason/fleet/internal/playbook"
	"github.com/sturlabragason/fleet/internal/rules"
	"github.com/sturlabragason/fleet/internal/runs"
)

// TrialFile is the active trial in the fleet home.
const TrialFile = "retro-trial.json"

// DefaultShare is the percentage of new spawns a trial's variant gets.
const DefaultShare = 50

// Arm names; the ledger records "<trial id>/<arm>".
const (
	Control = "control"
	Variant = "variant"
)

// Trial is a playbook patch under test: Layer is applied as a playbook layer
// right after the user layer for Share percent of new spawns.
type Trial struct {
	ID      string    `json:"id"` // runs.Hash of Layer
	Share   int       `json:"share"`
	Started time.Time `json:"started"`
	Source  string    `json:"source"` // the patch file it was started from
	Layer   string    `json:"layer"`
}

func trialPath(home string) string { return filepath.Join(home, TrialFile) }

// Load returns the active trial; ok is false when none runs. A trial file
// that does not parse is an error, so a broken trial is never silently off.
func Load(home string) (t Trial, ok bool, err error) {
	b, err := os.ReadFile(trialPath(home))
	if errors.Is(err, fs.ErrNotExist) {
		return Trial{}, false, nil
	}
	if err != nil {
		return Trial{}, false, fmt.Errorf("read trial: %w", err)
	}
	if err := json.Unmarshal(b, &t); err != nil {
		return Trial{}, false, fmt.Errorf("parse %s: %w", TrialFile, err)
	}
	return t, true, nil
}

// Start registers layer (the patch file's text, in worker.md format) as the
// variant of a new trial. It refuses while another trial runs.
func Start(home, source, layer string, share int, now time.Time) (Trial, error) {
	if share < 1 || share > 99 {
		return Trial{}, fmt.Errorf("share %d: want 1-99 percent", share)
	}
	if runs.Hash([]byte(layer)) == "" {
		return Trial{}, fmt.Errorf("patch %s is empty", source)
	}
	// The start time is in the ID, so a restart of the same patch is a new
	// trial: its split and its runs don't mix with the last one's.
	id := runs.Hash([]byte(layer + "\n" + now.UTC().Format(time.RFC3339Nano)))
	running, ok, err := Load(home)
	if err != nil {
		return Trial{}, err
	}
	if ok {
		return Trial{}, fmt.Errorf("trial %s is running; stop it first (fleet retro trial --stop)", running.ID)
	}
	t := Trial{ID: id, Share: share, Started: now, Source: source, Layer: layer}
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return Trial{}, fmt.Errorf("encode trial: %w", err)
	}
	if err := os.WriteFile(trialPath(home), append(b, '\n'), 0o600); err != nil {
		return Trial{}, fmt.Errorf("write trial: %w", err)
	}
	return t, nil
}

// Stop ends the active trial. Runs already in the ledger keep their arm.
func Stop(home string) error {
	if err := os.Remove(trialPath(home)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return errors.New("no trial is running")
		}
		return fmt.Errorf("stop trial: %w", err)
	}
	return nil
}

// ArmOf is the arm agent name falls in: deterministic by name and trial, so
// a respawn under the same name gets the same playbook.
func (t Trial) ArmOf(name string) string {
	h := sha256.Sum256([]byte(t.ID + "/" + name))
	if int(binary.BigEndian.Uint16(h[:2])%100) < t.Share {
		return Variant
	}
	return Control
}

// Assign is what a spawn of agent name gets from the active trial: the
// variant layer text ("" for control or no trial) and the arm to record
// ("" when no trial runs). A broken trial file is reported and no arm is
// assigned, so spawning never fails over a trial.
func Assign(home, name string) (layer, arm string, err error) {
	t, ok, err := Load(home)
	if err != nil || !ok {
		return "", "", err
	}
	a := t.ArmOf(name)
	if a == Variant {
		layer = t.Layer
	}
	return layer, t.ID + "/" + a, nil
}

// Promote writes the trial's variant into the user layer (<home>/worker.md)
// and ends the trial, only when v says the variant is not worse. The old
// worker.md is kept as worker.md.bak. A replacing layer becomes worker.md
// as it is; an appending one goes after the old text, so the composed
// playbook is what the variant arm got.
func Promote(home string, t Trial, v Verdict) error {
	if v.State != NotWorse {
		return fmt.Errorf("trial %s: verdict is %q, not %q; nothing promoted", t.ID, v.State, NotWorse)
	}
	user := rules.UserFile(home)
	old, err := os.ReadFile(user)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read user layer: %w", err)
	}
	next := strings.TrimSpace(t.Layer)
	first, _, _ := strings.Cut(next, "\n")
	if o := strings.TrimSpace(string(old)); o != "" && strings.TrimSpace(first) != playbook.ReplaceMarker {
		next = o + "\n\n" + next
	}
	if target, err := filepath.EvalSymlinks(user); err == nil {
		user = target
	}
	if err := backUp(user+".bak", old); err != nil {
		return err
	}
	// Spawns read the user layer without a lock, so it is replaced whole.
	tmp := user + ".tmp"
	if err := os.WriteFile(tmp, []byte(next+"\n"), 0o600); err != nil {
		return fmt.Errorf("write user layer: %w", err)
	}
	if err := os.Rename(tmp, user); err != nil {
		return fmt.Errorf("write user layer: %w", err)
	}
	return Stop(home)
}

// backUp keeps the old user layer as bak, or removes an older bak when
// there was none, so the bak is always what promote replaced.
func backUp(bak string, old []byte) error {
	if len(old) == 0 {
		if err := os.Remove(bak); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove stale backup: %w", err)
		}
		return nil
	}
	if err := os.WriteFile(bak, old, 0o600); err != nil {
		return fmt.Errorf("back up user layer: %w", err)
	}
	return nil
}
