package main

import (
	"context"

	clawlettui "github.com/mosaxiv/clawlet/tui"
	"github.com/urfave/cli/v3"
)

func cmdTUI() *cli.Command {
	return &cli.Command{
		Name:  "tui",
		Usage: "open the local terminal UI for gateway chat",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return clawlettui.Run(ctx)
		},
	}
}
