package prefs

import (
	"path/filepath"
	"testing"
)

func TestAdmiralMaxClamped(t *testing.T) {
	t.Setenv("FLEET_CONFIG", filepath.Join(t.TempDir(), "nope.toml"))
	if p, _ := Load(); p.AdmiralMax() != 3 {
		t.Errorf("default: %d", p.AdmiralMax())
	}
	for in, want := range map[string]int{"2": 2, "3": 3, "9": 3} {
		write(t, "[admiral]\nmax = "+in+"\n")
		p, err := Load()
		if err != nil || p.AdmiralMax() != want {
			t.Errorf("max %s: %d %v, want %d", in, p.AdmiralMax(), err, want)
		}
	}
}
