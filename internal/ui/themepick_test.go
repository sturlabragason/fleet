package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"github.com/sturlabragason/fleet/internal/theme"
)

// themeBE records the picker's calls to the backend.
type themeBE struct {
	fakeBE
	chromes []string // the active palette each chrome run applied
	saved   []string
	saveErr error
}

func (b *themeBE) ThemeChrome() func() {
	name := theme.Active().Name
	return func() { b.chromes = append(b.chromes, name) }
}

func (b *themeBE) SaveTheme(name string) func() error {
	return func() error { b.saved = append(b.saved, name); return b.saveErr }
}

func pickerModel(t *testing.T, be Backend) *Model {
	t.Helper()
	if err := theme.Load(theme.Default); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = theme.Load(theme.Default) })
	zone.NewGlobal()
	m := New(be, "")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	return m
}

func TestThemePicker(t *testing.T) {
	names := theme.Names()
	for _, tc := range []struct {
		name   string
		keys   []string
		active string   // palette active after the keys
		chrome []string // palettes the tmux chrome was given, in order
		saved  []string
		open   bool
	}{
		{"opens on the active theme", []string{"t"}, names[0], nil, nil, true},
		{"move previews", []string{"t", "j", "j"}, names[2], []string{names[1], names[2]}, nil, true},
		{"top edge does nothing", []string{"t", "k"}, names[0], nil, nil, true},
		{"esc restores", []string{"t", "j", "j", "esc"}, names[0], []string{names[1], names[2], names[0]}, nil, false},
		{"esc unmoved touches nothing", []string{"t", "esc"}, names[0], nil, nil, false},
		{"enter saves the previewed", []string{"t", "j", "enter"}, names[1], []string{names[1]}, []string{names[1]}, false},
		{"ctrl+c restores before quitting", []string{"t", "j", "ctrl+c"}, names[0], []string{names[1]}, nil, false},
		{"other keys stay in the picker", []string{"t", "w", "r", "x"}, names[0], nil, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			be := &themeBE{}
			m := pickerModel(t, be)
			for _, k := range tc.keys {
				run(m, k)
			}
			if got := theme.Active().Name; got != tc.active {
				t.Errorf("active %q, want %q", got, tc.active)
			}
			if strings.Join(be.chromes, ",") != strings.Join(tc.chrome, ",") {
				t.Errorf("chrome %v, want %v", be.chromes, tc.chrome)
			}
			if strings.Join(be.saved, ",") != strings.Join(tc.saved, ",") {
				t.Errorf("saved %v, want %v", be.saved, tc.saved)
			}
			if (m.tp != nil) != tc.open || m.wv != nil || m.ans != nil {
				t.Errorf("picker open %v (waits %v, answer %v), want %v", m.tp != nil, m.wv != nil, m.ans != nil, tc.open)
			}
		})
	}
}

func TestThemePickerSaveFails(t *testing.T) {
	be := &themeBE{saveErr: errors.New("read-only")}
	m := pickerModel(t, be)
	for _, k := range []string{"t", "j", "enter"} {
		run(m, k)
	}
	if !strings.Contains(m.msg, "not saved: read-only") {
		t.Fatalf("msg %q", m.msg)
	}
}

func TestThemePickerNeedsBackend(t *testing.T) {
	m := pickerModel(t, &fakeBE{})
	run(m, "t")
	if m.tp != nil || !strings.Contains(m.msg, "needs fleet up") {
		t.Fatalf("open %v msg %q", m.tp != nil, m.msg)
	}
}

func TestThemePickerDrawsAboveTiles(t *testing.T) {
	be := &themeBE{}
	m := pickerModel(t, be)
	as, _ := be.Refresh()
	m.setAgents(as)
	run(m, "t")
	v := ansi.Strip(m.View())
	first, _ := theme.Get(theme.Names()[0])
	if !strings.Contains(v, "theme 1/") || !strings.Contains(v, "› ") || !strings.Contains(v, first.Label) || !strings.Contains(v, "alpha") {
		t.Fatalf("picker view:\n%s", v)
	}
}
