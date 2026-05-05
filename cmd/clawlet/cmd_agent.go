package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mosaxiv/clawlet/agent"
	"github.com/fatih/color"
	"github.com/urfave/cli/v3"
)

func cmdAgent() *cli.Command {
	return &cli.Command{
		Name:  "agent",
		Usage: "run an agent in CLI mode",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "message", Aliases: []string{"m"}, Usage: "single message (non-interactive)"},
			&cli.StringFlag{Name: "session", Aliases: []string{"s"}, Value: "cli:default", Usage: "session key"},
			&cli.StringFlag{Name: "workspace", Usage: "workspace directory (default: ~/.clawlet/workspace or CLAWLET_WORKSPACE)"},
			&cli.IntFlag{Name: "max-iters", Value: 20, Usage: "max tool-call iterations"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, _, err := loadConfig()
			if err != nil {
				return err
			}

			wsAbs, err := resolveWorkspace(cmd.String("workspace"))
			if err != nil {
				return err
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
					fmt.Printf("%s %s %s\n", toolStyle.Sprint("tool>"), ev.Name, ev.Args)
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
				SessionKey:   cmd.String("session"),
				MaxIters:     cmd.Int("max-iters"),
				ToolObserver: observer,
			})
			if err != nil {
				return err
			}

			fmt.Fprintln(os.Stderr, dimStyle.Sprintf("workspace: %s", wsAbs))
			fmt.Fprintln(os.Stderr, dimStyle.Sprintf("session: %s", cmd.String("session")))

			msg := cmd.String("message")
			if msg != "" {
				return runSingle(ctx, a, msg, userStyle, agentStyle, errStyle, dimStyle)
			}

			return runInteractive(ctx, a, userStyle, agentStyle, errStyle, dimStyle)
		},
	}
}

func runSingle(ctx context.Context, a *agent.Agent, msg string, userStyle, agentStyle, errStyle, dimStyle *color.Color) error {
	fmt.Printf("%s %s\n", userStyle.Sprint("user>"), msg)
	start := time.Now()
	out, err := a.Process(ctx, msg)
	if err != nil {
		fmt.Fprintln(os.Stderr, errStyle.Sprintf("error: %v", err))
		return err
	}
	fmt.Printf("%s %s\n", agentStyle.Sprint("agent>"), out)
	fmt.Fprintln(os.Stderr, dimStyle.Sprintf("(took %s)", time.Since(start).Truncate(time.Millisecond)))
	return nil
}

func runInteractive(ctx context.Context, a *agent.Agent, userStyle, agentStyle, errStyle, dimStyle *color.Color) error {
	in := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print(userStyle.Sprint("user> "))
		if !in.Scan() {
			break
		}
		line := strings.TrimSpace(in.Text())
		if line == "" {
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
		fmt.Printf("%s %s\n", agentStyle.Sprint("agent>"), out)
		fmt.Fprintln(os.Stderr, dimStyle.Sprintf("(took %s)", time.Since(start).Truncate(time.Millisecond)))
	}
	return in.Err()
}
