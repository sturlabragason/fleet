package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/sturlabragason/fleet/internal/board"
	"github.com/sturlabragason/fleet/internal/theme"
)

// The owner chip (DESIGN §6.12): which admiral an agent reports to. It only
// exists while the fleet has more than one admiral, so a single-admiral
// overview looks exactly as before.

// OwnerInitials gives each admiral its chip text: the uppercase first letter,
// or the first two characters when two admirals share an initial.
func OwnerInitials(names []string) map[string]string {
	count := map[string]int{}
	for _, n := range names {
		count[strings.ToUpper(first(n, 1))]++
	}
	out := map[string]string{}
	for _, n := range names {
		i := strings.ToUpper(first(n, 1))
		if count[i] > 1 {
			i = strings.ToUpper(first(n, 1)) + strings.ToLower(first(n, 2)[len(first(n, 1)):])
		}
		out[n] = i
	}
	return out
}

func first(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}

// ownerChip is the filled chip for a's owner, "" with one admiral. It is
// followed by a space so it can lead a title line.
func (m *Model) ownerChip(a *board.Agent) string {
	if len(m.adms) < 2 {
		return ""
	}
	owner := a.Owner()
	var names []string
	col := theme.Supervisor
	for _, ad := range m.adms {
		names = append(names, ad.Name)
		if ad.Name == owner {
			col = ad.Color
		}
	}
	txt := OwnerInitials(names)[owner]
	if txt == "" {
		txt = strings.ToUpper(first(owner, 1))
	}
	st := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Crust)).Background(lipgloss.Color(col)).Bold(true)
	return st.Render(" "+txt+" ") + " "
}
