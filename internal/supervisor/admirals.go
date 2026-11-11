package supervisor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sturlabragason/fleet/internal/sysx"
)

// Main is the admiral every fleet has. It keeps every path, mailbox and pane
// it had before several admirals existed (DESIGN §6.12, ADR-64).
const Main = "main"

func isMain(name string) bool { return name == "" || name == Main }

// Admiral is one entry of the registry: an admiral other than main.
type Admiral struct {
	Name    string `json:"name"`
	Profile string `json:"profile,omitempty"`
	// Slot is the colour slot, 1 or 2 (0 is main): stable once chosen, and
	// the lowest free slot is reused first.
	Slot int `json:"slot"`
}

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$`)

// ValidName reports whether name can name an admiral: the agent-name rules
// without dots (the mailbox is admiral.<name>), and not a reserved word.
func ValidName(name string) error {
	switch {
	case !nameRe.MatchString(name):
		return fmt.Errorf("bad admiral name %q: letters, digits, - and _, up to 32", name)
	case name == Main, name == "user", name == "admiral", name == "supervisor":
		return fmt.Errorf("admiral name %q is reserved", name)
	}
	return nil
}

func registryPath(home string) string { return filepath.Join(home, "admirals", "registry.json") }

// Admirals lists the registered admirals other than main, in the order they
// were added.
func Admirals(home string) []Admiral {
	var out []Admiral
	b, err := os.ReadFile(registryPath(home))
	if err != nil || json.Unmarshal(b, &out) != nil {
		return nil
	}
	return out
}

// Names is main followed by every registered admiral: the registry order.
func Names(home string) []string {
	out := []string{Main}
	for _, a := range Admirals(home) {
		out = append(out, a.Name)
	}
	return out
}

// RegistryReadable is false when the registry exists but cannot be read or
// parsed; a missing registry is an empty one.
func RegistryReadable(home string) bool {
	b, err := os.ReadFile(registryPath(home))
	if os.IsNotExist(err) {
		return true
	}
	var out []Admiral
	return err == nil && json.Unmarshal(b, &out) == nil
}

// Exists reports whether name is main or a registered admiral.
func Exists(home, name string) bool {
	if isMain(name) {
		return true
	}
	for _, a := range Admirals(home) {
		if a.Name == name {
			return true
		}
	}
	return false
}

// Find returns a registered admiral.
func Find(home, name string) (Admiral, bool) {
	for _, a := range Admirals(home) {
		if a.Name == name {
			return a, true
		}
	}
	return Admiral{}, false
}

// Slot is the colour slot of an admiral: 0 for main, the stored slot
// otherwise (0 when unknown).
func Slot(home, name string) int {
	a, _ := Find(home, name)
	return a.Slot
}

// update runs fn on the registry under a flock and saves it atomically.
func updateRegistry(home string, fn func([]Admiral) ([]Admiral, error)) error {
	if err := os.MkdirAll(filepath.Dir(registryPath(home)), 0o755); err != nil {
		return err
	}
	lf, err := os.OpenFile(registryPath(home)+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lf.Close()
	if err := sysx.Flock(lf, sysx.LockEx); err != nil {
		return err
	}
	defer sysx.Flock(lf, sysx.LockUn)
	as, err := fn(Admirals(home))
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(as, "", "  ")
	if err != nil {
		return err
	}
	tmp := registryPath(home) + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, registryPath(home))
}

// Add registers an admiral under max admirals in all (main counts) and
// gives it the lowest free colour slot. It returns the entry.
func Add(home, name, profile string, max int) (Admiral, error) {
	if err := ValidName(name); err != nil {
		return Admiral{}, err
	}
	var out Admiral
	err := updateRegistry(home, func(as []Admiral) ([]Admiral, error) {
		used := map[int]bool{}
		for _, a := range as {
			if a.Name == name {
				return nil, fmt.Errorf("admiral %q already exists", name)
			}
			used[a.Slot] = true
		}
		if max > 0 && len(as)+1 >= max {
			return nil, fmt.Errorf("at most %d admirals ([admiral] max in config.toml)", max)
		}
		slot := 1
		for used[slot] {
			slot++
		}
		out = Admiral{Name: name, Profile: strings.TrimSpace(profile), Slot: slot}
		return append(as, out), nil
	})
	return out, err
}

// SetProfile records the profile an admiral runs, for fleet admiral ls and
// the pane border.
func SetProfile(home, name, profile string) error {
	return updateRegistry(home, func(as []Admiral) ([]Admiral, error) {
		for i := range as {
			if as[i].Name == name {
				as[i].Profile = profile
			}
		}
		return as, nil
	})
}

// Remove drops an admiral from the registry and deletes nothing else: the
// handoffs and session stay on disk.
func Remove(home, name string) error {
	if isMain(name) {
		return fmt.Errorf("main cannot be closed")
	}
	return updateRegistry(home, func(as []Admiral) ([]Admiral, error) {
		var out []Admiral
		found := false
		for _, a := range as {
			if a.Name == name {
				found = true
				continue
			}
			out = append(out, a)
		}
		if !found {
			return nil, fmt.Errorf("no admiral %q", name)
		}
		return out, nil
	})
}
