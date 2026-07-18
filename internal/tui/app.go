package tui

import (
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

// Run builds the root model and runs the Bubble Tea program on the alternate
// screen until the user quits.
func Run(backend Backend, cfg Config) error {
	if cfg.Stdout == nil {
		cfg.Stdout = os.Stdout
	}
	p := tea.NewProgram(New(backend, cfg), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
