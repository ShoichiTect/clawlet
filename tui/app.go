package tui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type RunOptions struct {
	ProjectPath string
	SessionKey  string
}

func Run(ctx context.Context) error {
	return RunWithOptions(ctx, RunOptions{})
}

func RunWithOptions(ctx context.Context, opts RunOptions) error {
	st, err := LoadState()
	if err != nil {
		return err
	}
	if strings.TrimSpace(opts.ProjectPath) != "" {
		sessionKey := strings.TrimSpace(opts.SessionKey)
		if sessionKey == "" {
			sessionKey = defaultSessionKey
		}
		st = upsertProjectState(st, opts.ProjectPath, sessionKey, time.Now().UTC())
	}
	program := tea.NewProgram(newModel(st), tea.WithContext(ctx))
	_, err = program.Run()
	return err
}
