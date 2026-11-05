package prefs

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSquadDefaults(t *testing.T) {
	t.Setenv("FLEET_CONFIG", filepath.Join(t.TempDir(), "nope.toml"))
	p, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Squad(); got != DefaultSquad() {
		t.Errorf("%+v want %+v", got, DefaultSquad())
	}
	if got := (*Prefs)(nil).Squad(); got != DefaultSquad() {
		t.Errorf("nil prefs: %+v", got)
	}
}

func TestSquadCaps(t *testing.T) {
	write(t, "[squad]\nmax_members = 2\nmax_depth = 0\n")
	p, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Squad(); got.MaxMembers != 2 || got.MaxDepth != 0 {
		t.Errorf("%+v", got)
	}
}

func TestSquadRejectsNonsense(t *testing.T) {
	for _, body := range []string{"[squad]\nmax_members = \"lots\"\n", "[squad]\nmax_depth = -1\n"} {
		write(t, body)
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "whole number") {
			t.Errorf("%q: err %v", body, err)
		}
	}
}
