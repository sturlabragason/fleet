package ui

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/sturlabragason/fleet/internal/logo"
)

// splash is the empty overview: the largest mark that fits, centred.
func (m *Model) splash() []string {
	avail := max(m.h-1, 1)
	s, ok := logo.Fit(m.w, avail)
	if !ok {
		return nil
	}
	mark := logo.Render(s, logo.ASCII(os.Getenv))
	return strings.Split(lipgloss.Place(m.w, avail, lipgloss.Center, lipgloss.Center, mark), "\n")
}
