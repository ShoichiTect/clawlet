package tui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mosaxiv/clawlet/config"
)

type RunOptions struct {
	Config      *config.Config
	MaxIters    int
	ProjectPath string
	SessionKey  string
}

func Run(ctx context.Context) error {
	return RunWithOptions(ctx, RunOptions{})
}

func RunWithOptions(ctx context.Context, opts RunOptions) error {
	maxIters := opts.MaxIters
	if maxIters <= 0 {
		maxIters = 20
	}

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
	program := tea.NewProgram(newModel(opts.Config, maxIters, st), tea.WithContext(ctx))
	_, err = program.Run()
	return err
}
