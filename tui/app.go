package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
)

func Run(ctx context.Context) error {
	st, err := LoadState()
	if err != nil {
		return err
	}
	program := tea.NewProgram(newModel(st), tea.WithContext(ctx))
	_, err = program.Run()
	return err
}
