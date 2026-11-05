package prefs

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func loadCfg(t *testing.T, body string) (*Prefs, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(p, []byte(body), 0o644)
	t.Setenv("FLEET_CONFIG", p)
	return Load()
}

func TestMemoryDefaultsAndOverrides(t *testing.T) {
	p, err := loadCfg(t, "[defaults]\nmode = \"autopilot\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if m := p.Memory(); !m.Guard || m.MinFree != DefaultMinFree || m.Every != DefaultMemEvery {
		t.Errorf("default memory %+v", m)
	}
	p, err = loadCfg(t, "[memory]\nguard = false\nmin_free = \"6GB\"\ncheck_every = \"2m\"\n")
	if err != nil {
		t.Fatal(err)
	}
	m := p.Memory()
	if m.Guard || m.MinFree != 6<<30 || m.Every != 2*time.Minute {
		t.Errorf("memory %+v", m)
	}
	if n := (*Prefs)(nil).Memory(); n != DefaultMemory() {
		t.Errorf("nil prefs memory %+v", n)
	}
}

func TestMemoryBadValues(t *testing.T) {
	if _, err := loadCfg(t, "[memory]\nmin_free = \"lots\"\n"); err == nil {
		t.Error("min_free = lots must fail")
	}
	if _, err := loadCfg(t, "[memory]\ncheck_every = \"0s\"\n"); err == nil {
		t.Error("check_every = 0s must fail")
	}
}

func TestParseSize(t *testing.T) {
	for _, c := range []struct {
		in   string
		want uint64
	}{{"4GB", 4 << 30}, {"4 GiB", 4 << 30}, {"512MB", 512 << 20}, {"1.5G", 1536 << 20}, {"2048", 2048}, {"3k", 3 << 10}} {
		got, err := ParseSize(c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseSize(%q) = %d, %v; want %d", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "GB", "-1GB", "4PB", "four"} {
		if _, err := ParseSize(bad); err == nil {
			t.Errorf("ParseSize(%q) must fail", bad)
		}
	}
}
