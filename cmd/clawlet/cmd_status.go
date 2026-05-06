package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/mosaxiv/clawlet/paths"
	"github.com/urfave/cli/v3"
)

func cmdStatus() *cli.Command {
	return &cli.Command{
		Name:  "status",
		Usage: "print effective configuration status",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, cfgPath, err := loadConfig()
			if err != nil {
				return err
			}
			fmt.Printf("config: %s\n", cfgPath)
			fmt.Printf("workspace: %s\n", paths.WorkspaceDir())
			fmt.Printf("llm.provider: %s\n", cfg.LLM.Provider)
			fmt.Printf("llm.baseURL: %s\n", cfg.LLM.BaseURL)
			fmt.Printf("llm.model: %s\n", cfg.LLM.Model)
			if strings.TrimSpace(cfg.Agents.Defaults.Model) != "" {
				fmt.Printf("agents.defaults.model: %s\n", cfg.Agents.Defaults.Model)
			}
			fmt.Printf("agents.defaults.maxTokens: %d\n", cfg.Agents.Defaults.MaxTokensValue())
			fmt.Printf("agents.defaults.temperature: %.2f\n", cfg.Agents.Defaults.TemperatureValue())
			fmt.Printf("tools.restrictToWorkspace: %v\n", cfg.Tools.RestrictToWorkspaceValue())
			fmt.Printf("tools.exec.timeoutSec: %d\n", cfg.Tools.Exec.TimeoutSec)
			fmt.Printf("tools.skills.enabled: %v\n", cfg.Tools.Skills.EnabledValue())
			fmt.Printf("tools.skills.registry.baseURL: %s\n", cfg.Tools.Skills.Registry.BaseURL)
			fmt.Printf("tools.skills.registry.authToken: %v\n", cfg.Tools.Skills.Registry.AuthToken != "")
			return nil
		},
	}
}
