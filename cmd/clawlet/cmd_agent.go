package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/mosaxiv/clawlet/agent"
	"github.com/urfave/cli/v3"
)

func cmdAgent() *cli.Command {
	return &cli.Command{
		Name:  "agent",
		Usage: "run an agent in CLI mode",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "message", Aliases: []string{"m"}, Usage: "single message (non-interactive)"},
			&cli.StringFlag{Name: "session", Aliases: []string{"s"}, Usage: "session key"},
			&cli.StringFlag{Name: "dir", Usage: "project directory (default: ~/.clawlet/workspace)"},
			&cli.IntFlag{Name: "max-iters", Value: 20, Usage: "max tool-call iterations"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, _, err := loadConfig()
			if err != nil {
				return err
			}

			dirFlag := strings.TrimSpace(cmd.String("dir"))
			wsAbs, sessionsDir, err := resolveDir(dirFlag)
			if err != nil {
				return err
			}
			sessionKey := strings.TrimSpace(cmd.String("session"))
			if sessionKey == "" {
				if dirFlag == "" {
					sessionKey = "cli:default"
				} else {
					sessionKey = "default"
				}
			}

			var (
				userStyle  = color.New(color.FgCyan, color.Bold)
				agentStyle = color.New(color.FgGreen, color.Bold)
				toolStyle  = color.New(color.FgYellow, color.Bold)
				errStyle   = color.New(color.FgRed, color.Bold)
				dimStyle   = color.New(color.FgHiBlack)
			)

			observer := func(ev agent.ToolEvent) {
				switch ev.Phase {
				case agent.ToolStart:
					fmt.Println(toolStyle.Sprintf("--- tool --- %s %s", ev.Name, ev.Args))
				case agent.ToolEnd:
					if ev.Error != "" {
						fmt.Println(errStyle.Sprintf("(error) %s", ev.Error))
					} else if strings.TrimSpace(ev.Output) != "" {
						fmt.Println(ev.Output)
					}
					fmt.Fprintln(os.Stderr, dimStyle.Sprintf("(took %s)", ev.Duration.Truncate(time.Millisecond)))
				}
			}

			a, err := agent.New(agent.Options{
				Config:       cfg,
				WorkspaceDir: wsAbs,
				SessionDir:   sessionsDir,
				SessionKey:   sessionKey,
				MaxIters:     cmd.Int("max-iters"),
				ToolObserver: observer,
			})
			if err != nil {
				return err
			}

			fmt.Fprintln(os.Stderr, dimStyle.Sprintf("workspace: %s", wsAbs))
			fmt.Fprintln(os.Stderr, dimStyle.Sprintf("sessions: %s", sessionsDir))
			fmt.Fprintln(os.Stderr, dimStyle.Sprintf("session: %s", sessionKey))

			msg := cmd.String("message")
			if msg != "" {
				return runSingle(ctx, a, msg, userStyle, agentStyle, errStyle, dimStyle)
			}

			return runInteractive(ctx, a, userStyle, agentStyle, errStyle, dimStyle)
		},
	}
}

func runSingle(ctx context.Context, a *agent.Agent, msg string, userStyle, agentStyle, errStyle, dimStyle *color.Color) error {
	fmt.Println(userStyle.Sprint("--- user ---"))
	fmt.Println(msg)
	start := time.Now()
	out, err := a.Process(ctx, msg)
	if err != nil {
		fmt.Fprintln(os.Stderr, errStyle.Sprintf("error: %v", err))
		return err
	}
	fmt.Println(agentStyle.Sprint("--- agent ---"))
	fmt.Println(out)
	fmt.Fprintln(os.Stderr, dimStyle.Sprintf("(took %s)", time.Since(start).Truncate(time.Millisecond)))
	return nil
}

func runInteractive(ctx context.Context, a *agent.Agent, userStyle, agentStyle, errStyle, dimStyle *color.Color) error {
	for {
		fmt.Println(userStyle.Sprint("--- user ---"))
		// readMultiline reads raw input; Ctrl+J inserts newline, Enter submits.
		text, err := readMultiline("> ", "  ")
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			fmt.Fprintln(os.Stderr, errStyle.Sprintf("input error: %v", err))
			continue
		}

		line := strings.TrimSpace(text)
		if line == "" {
			// Ctrl+C or empty submit → skip
			continue
		}
		if line == "/exit" || line == "/quit" {
			break
		}
		start := time.Now()
		out, err := a.Process(ctx, line)
		if err != nil {
			fmt.Fprintln(os.Stderr, errStyle.Sprintf("error: %v", err))
			continue
		}
		fmt.Println(agentStyle.Sprint("--- agent ---"))
		fmt.Println(out)
		fmt.Fprintln(os.Stderr, dimStyle.Sprintf("(took %s)", time.Since(start).Truncate(time.Millisecond)))
	}
	return nil
}
