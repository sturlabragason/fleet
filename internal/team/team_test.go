package team

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/sturlabragason/fleet/internal/theme"
)

func TestAutoColoursDistinctAndSkipUsed(t *testing.T) {
	s := NewStore(t.TempDir())
	if err := s.Create("a", "blue"); err != nil {
		t.Fatal(err)
	}
	s.Create("b", "")
	s.Create("c", "")
	f := s.Load()
	if f.Teams["b"].Color != theme.TeamHex("pink") || f.Teams["c"].Color != theme.TeamHex("teal") {
		t.Errorf("auto colours: b=%s c=%s", f.Teams["b"].Color, f.Teams["c"].Color)
	}
	s.Delete("b")
	s.Create("d", "")
	if got := s.Load().Teams["d"].Color; got != theme.TeamHex("pink") {
		t.Errorf("freed colour not reused: %s", got)
	}
	if err := s.Create("a", ""); err == nil || !strings.Contains(err.Error(), "exists") {
		t.Errorf("duplicate create: %v", err)
	}
	if err := s.Create("bad name", ""); err == nil {
		t.Error("bad name accepted")
	}
}

func TestAddMovesAndCreates(t *testing.T) {
	s := NewStore(t.TempDir())
	moved, err := s.Add("alpha", "a1", "a2")
	if err != nil || len(moved) != 0 {
		t.Fatal(moved, err)
	}
	if c := s.Load().Teams["alpha"].Color; c != theme.TeamHex("blue") {
		t.Errorf("created team colour %s", c)
	}
	moved, err = s.Add("beta", "a2")
	if err != nil || moved["a2"] != "alpha" {
		t.Fatalf("move: %v %v", moved, err)
	}
	if m, _ := s.Members("alpha"); !reflect.DeepEqual(m, []string{"a1"}) {
		t.Errorf("alpha members %v", m)
	}
	if tm, _ := s.Of("a2"); tm != "beta" {
		t.Errorf("a2 in %q", tm)
	}
	if err := s.Remove("beta", "a1"); err == nil {
		t.Error("remove of non-member accepted")
	}
	if err := s.Remove("beta", "a2"); err != nil {
		t.Fatal(err)
	}
	if tm, _ := s.Of("a2"); tm != "" {
		t.Errorf("a2 still in %q", tm)
	}
	if err := s.Delete("nope"); err == nil || !strings.Contains(err.Error(), "unknown team") {
		t.Errorf("delete unknown: %v", err)
	}
	if err := s.Delete("alpha"); err != nil {
		t.Fatal(err)
	}
	if tm, _ := s.Of("a1"); tm != "" {
		t.Error("delete kept membership")
	}
}

func TestSetColorAndParse(t *testing.T) {
	s := NewStore(t.TempDir())
	s.Create("x", "")
	if err := s.SetColor("x", "Sapphire"); err != nil || s.Load().Teams["x"].Color != theme.TeamHex("sapphire") {
		t.Fatalf("by name: %v", err)
	}
	if err := s.SetColor("x", "#ABCDEF"); err != nil || s.Load().Teams["x"].Color != "#abcdef" {
		t.Fatalf("by hex: %v %s", err, s.Load().Teams["x"].Color)
	}
	for _, bad := range []string{"green", "#abc", "#abcdeg", ""} {
		if _, err := ParseColor(bad); err == nil || !strings.Contains(err.Error(), "sapphire") {
			t.Errorf("ParseColor(%q): %v", bad, err)
		}
	}
	if err := s.SetColor("nope", "blue"); err == nil {
		t.Error("unknown team accepted")
	}
}

func TestSurvivesRestartSameTeamDrop(t *testing.T) {
	dir := t.TempDir()
	NewStore(dir).Add("alpha", "a1", "a2", "a3")
	NewStore(dir).Add("beta", "b1")
	s := NewStore(dir) // a fresh process
	if !s.SameTeam("a1", "a2") || s.SameTeam("a1", "b1") || s.SameTeam("x", "y") || s.SameTeam("a1", "x") {
		t.Error("SameTeam")
	}
	mates := s.Teammates("a2")
	sort.Strings(mates)
	if !reflect.DeepEqual(mates, []string{"a1", "a3"}) {
		t.Errorf("teammates %v", mates)
	}
	if s.Teammates("nobody") != nil {
		t.Error("teammates of no-team agent")
	}
	if err := s.DropAgent("a2"); err != nil {
		t.Fatal(err)
	}
	if err := s.DropAgent("nobody"); err != nil {
		t.Fatal(err)
	}
	by := NewStore(dir).ByAgent()
	if _, ok := by["a2"]; ok || by["a1"].Team != "alpha" || by["b1"].Color != theme.TeamHex("pink") {
		t.Errorf("ByAgent %v", by)
	}
}

func TestBrokenJSON(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "teams.json"), []byte("{nope"), 0o644)
	s := NewStore(dir)
	if len(s.Load().Teams) != 0 {
		t.Fatal("broken file not treated as empty")
	}
	if _, err := os.Stat(filepath.Join(dir, "teams.json.broken")); err != nil {
		t.Error("no .broken backup")
	}
	if _, err := os.Stat(filepath.Join(dir, "teams.json")); !os.IsNotExist(err) {
		t.Error("broken teams.json left in place; every Load would log and back it up again")
	}
	if _, err := s.Add("a", "x"); err != nil {
		t.Fatal(err)
	}
	if tm, _ := s.Of("x"); tm != "a" {
		t.Error("store unusable after recovery")
	}
}

// A key this build does not know, on a team or at the top of teams.json,
// survives a write: an old watcher's DropAgent erased `squad` (ADR-62).
func TestUnknownKeysSurvive(t *testing.T) {
	dir := t.TempDir()
	raw := `{"k_file":1,"teams":{"t1":{"color":"red","members":["a1","a2"],"created":"2026-01-01T00:00:00Z","k_team":{"x":1}}}}`
	os.WriteFile(filepath.Join(dir, "teams.json"), []byte(raw), 0o644)
	s := NewStore(dir)
	if err := s.DropAgent("a1"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "teams.json"))
	if !strings.Contains(string(b), `"k_file"`) || !strings.Contains(string(b), `"k_team"`) {
		t.Fatalf("unknown key dropped: %s", b)
	}
}
