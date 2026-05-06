package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mosaxiv/clawlet/cron"
	"github.com/urfave/cli/v3"
)

func cmdCron() *cli.Command {
	return &cli.Command{
		Name:  "cron",
		Usage: "manage scheduled jobs",
		Commands: []*cli.Command{
			cronListCmd(),
			cronAddCmd(),
			cronRemoveCmd(),
			cronToggleCmd(),
			cronRunCmd(),
		},
	}
}

func cronDirFlag() cli.Flag {
	return &cli.StringFlag{Name: "dir", Usage: "project directory (default: ~/.clawlet/workspace)"}
}

func cronServiceForCmd(cmd *cli.Command) (*cron.Service, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return nil, err
	}
	wsAbs, _, err := resolveDir(cmd.String("dir"))
	if err != nil {
		return nil, err
	}
	return cron.NewService(resolveCronStorePath(wsAbs, cfg.Cron.StorePath), nil), nil
}

func cronListCmd() *cli.Command {
	return &cli.Command{
		Name:  "list",
		Usage: "list jobs",
		Flags: []cli.Flag{cronDirFlag()},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			svc, err := cronServiceForCmd(cmd)
			if err != nil {
				return err
			}
			jobs := svc.List(true)
			if len(jobs) == 0 {
				fmt.Println("No jobs.")
				return nil
			}
			for _, j := range jobs {
				fmt.Printf("- %s id=%s enabled=%v kind=%s session=%s next=%d\n", j.Name, j.ID, j.Enabled, j.Schedule.Kind, j.Payload.SessionKey, j.State.NextRunAtMS)
			}
			return nil
		},
	}
}

func cronAddCmd() *cli.Command {
	return &cli.Command{
		Name:  "add",
		Usage: "add a job",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "name", Usage: "job name"},
			&cli.StringFlag{Name: "message", Usage: "message for agent", Required: true},
			&cli.IntFlag{Name: "every", Usage: "run every N seconds"},
			&cli.StringFlag{Name: "cron", Usage: "cron expression (5-field)"},
			&cli.StringFlag{Name: "at", Usage: "run once at time (RFC3339)"},
			&cli.StringFlag{Name: "session", Aliases: []string{"s"}, Usage: "session key for the agent turn"},
			cronDirFlag(),
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			dirFlag := strings.TrimSpace(cmd.String("dir"))
			svc, err := cronServiceForCmd(cmd)
			if err != nil {
				return err
			}

			message := strings.TrimSpace(cmd.String("message"))
			jname := strings.TrimSpace(cmd.String("name"))
			if jname == "" {
				jname = message
			}

			every := cmd.Int("every")
			cronExpr := strings.TrimSpace(cmd.String("cron"))
			at := strings.TrimSpace(cmd.String("at"))

			scheduleFlags := 0
			if every != 0 {
				scheduleFlags++
			}
			if cronExpr != "" {
				scheduleFlags++
			}
			if at != "" {
				scheduleFlags++
			}
			if scheduleFlags != 1 {
				return cli.Exit("exactly one of --every/--cron/--at must be set", 2)
			}

			var sched cron.Schedule
			switch {
			case every != 0:
				if every <= 0 {
					return cli.Exit("--every must be a positive number of seconds", 2)
				}
				sched = cron.Schedule{Kind: "every", EveryMS: int64(every) * 1000}
			case cronExpr != "":
				sched = cron.Schedule{Kind: "cron", Expr: cronExpr}
			case at != "":
				t, err := time.Parse(time.RFC3339, at)
				if err != nil {
					return err
				}
				sched = cron.Schedule{Kind: "at", AtMS: t.UnixMilli()}
			}

			sessionKey := strings.TrimSpace(cmd.String("session"))
			if sessionKey == "" {
				if dirFlag == "" {
					sessionKey = "gateway:default"
				} else {
					sessionKey = "default"
				}
			}

			payload := cron.Payload{
				Kind:       "agent_turn",
				Message:    message,
				SessionKey: sessionKey,
			}

			j, err := svc.Add(jname, sched, payload)
			if err != nil {
				return err
			}
			fmt.Printf("Created job %s (id=%s)\n", j.Name, j.ID)
			return nil
		},
	}
}

func cronRemoveCmd() *cli.Command {
	return &cli.Command{
		Name:      "remove",
		Usage:     "remove a job",
		ArgsUsage: "<job_id>",
		Flags:     []cli.Flag{cronDirFlag()},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			svc, err := cronServiceForCmd(cmd)
			if err != nil {
				return err
			}
			if cmd.Args().Len() < 1 {
				return cli.Exit("usage: clawlet cron remove <job_id>", 2)
			}
			id := cmd.Args().Get(0)
			if svc.Remove(id) {
				fmt.Println("Removed:", id)
			} else {
				fmt.Println("Not found:", id)
			}
			return nil
		},
	}
}

func cronToggleCmd() *cli.Command {
	return &cli.Command{
		Name:      "toggle",
		Usage:     "enable or disable a job",
		ArgsUsage: "<job_id>",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "disable", Usage: "disable instead of enable"},
			cronDirFlag(),
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			svc, err := cronServiceForCmd(cmd)
			if err != nil {
				return err
			}
			if cmd.Args().Len() < 1 {
				return cli.Exit("usage: clawlet cron toggle [--disable] <job_id>", 2)
			}
			id := cmd.Args().Get(0)
			if svc.Toggle(id, cmd.Bool("disable")) {
				if cmd.Bool("disable") {
					fmt.Println("Disabled:", id)
				} else {
					fmt.Println("Enabled:", id)
				}
			} else {
				fmt.Println("Not found:", id)
			}
			return nil
		},
	}
}

func cronRunCmd() *cli.Command {
	return &cli.Command{
		Name:      "run",
		Usage:     "trigger a job immediately",
		ArgsUsage: "<job_id>",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "force", Usage: "run even if disabled"},
			cronDirFlag(),
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			svc, err := cronServiceForCmd(cmd)
			if err != nil {
				return err
			}
			if cmd.Args().Len() < 1 {
				return cli.Exit("usage: clawlet cron run [--force] <job_id>", 2)
			}
			id := cmd.Args().Get(0)
			_, err = svc.RunNow(ctx, id, cmd.Bool("force"))
			if err != nil {
				return err
			}
			fmt.Println("Triggered:", id)
			return nil
		},
	}
}
