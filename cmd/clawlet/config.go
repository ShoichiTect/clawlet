package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mosaxiv/clawlet/config"
	"github.com/mosaxiv/clawlet/paths"
)

func loadConfig() (*config.Config, string, error) {
	cfgPath, err := paths.ConfigPath()
	if err != nil {
		return nil, "", err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, cfgPath, fmt.Errorf("failed to load config: %s\nhint: run `clawlet onboard`\n%w", cfgPath, err)
	}

	applyEnvOverrides(cfg)
	cfg.ApplyLLMRouting()

	if strings.TrimSpace(cfg.LLM.APIKey) == "" && providerNeedsAPIKey(cfg.LLM.Provider) {
		fmt.Fprintln(os.Stderr, "warning: llm.apiKey is empty (set in config.env or env vars)")
	}

	return cfg, cfgPath, nil
}

func applyEnvOverrides(cfg *config.Config) {
	if v := os.Getenv("CLAWLET_API_KEY"); v != "" {
		cfg.LLM.APIKey = v
	}
	if v := os.Getenv("CLAWLET_BASE_URL"); v != "" {
		cfg.LLM.BaseURL = v
	}
	if v := os.Getenv("CLAWLET_MODEL"); v != "" {
		cfg.Agents.Defaults.Model = v
	}
	if v := os.Getenv("CLAWLET_OPENAI_API_KEY"); v != "" {
		if cfg.Env == nil {
			cfg.Env = map[string]string{}
		}
		cfg.Env["OPENAI_API_KEY"] = v
	}
	if v := os.Getenv("CLAWLET_OPENROUTER_API_KEY"); v != "" {
		if cfg.Env == nil {
			cfg.Env = map[string]string{}
		}
		cfg.Env["OPENROUTER_API_KEY"] = v
	}
	if v := os.Getenv("CLAWLET_ANTHROPIC_API_KEY"); v != "" {
		if cfg.Env == nil {
			cfg.Env = map[string]string{}
		}
		cfg.Env["ANTHROPIC_API_KEY"] = v
	}
	if v := os.Getenv("CLAWLET_GEMINI_API_KEY"); v != "" {
		if cfg.Env == nil {
			cfg.Env = map[string]string{}
		}
		cfg.Env["GEMINI_API_KEY"] = v
	}
	if v := os.Getenv("CLAWLET_GOOGLE_API_KEY"); v != "" {
		if cfg.Env == nil {
			cfg.Env = map[string]string{}
		}
		cfg.Env["GOOGLE_API_KEY"] = v
	}
	if v := os.Getenv("OPENAI_API_KEY"); v != "" {
		if cfg.Env == nil {
			cfg.Env = map[string]string{}
		}
		cfg.Env["OPENAI_API_KEY"] = v
	}
	if v := os.Getenv("OPENROUTER_API_KEY"); v != "" {
		if cfg.Env == nil {
			cfg.Env = map[string]string{}
		}
		cfg.Env["OPENROUTER_API_KEY"] = v
	}
	if v := os.Getenv("ANTHROPIC_API_KEY"); v != "" {
		if cfg.Env == nil {
			cfg.Env = map[string]string{}
		}
		cfg.Env["ANTHROPIC_API_KEY"] = v
	}
	if v := os.Getenv("GEMINI_API_KEY"); v != "" {
		if cfg.Env == nil {
			cfg.Env = map[string]string{}
		}
		cfg.Env["GEMINI_API_KEY"] = v
	}
	if v := os.Getenv("GOOGLE_API_KEY"); v != "" {
		if cfg.Env == nil {
			cfg.Env = map[string]string{}
		}
		cfg.Env["GOOGLE_API_KEY"] = v
	}
	if v := os.Getenv("SHENGSUANYUN_API_KEY"); v != "" {
		if cfg.Env == nil {
			cfg.Env = map[string]string{}
		}
		cfg.Env["SHENGSUANYUN_API_KEY"] = v
	}
	if v := os.Getenv("CLAWLET_SKILLS_REGISTRY_AUTH_TOKEN"); v != "" {
		cfg.Tools.Skills.Registry.AuthToken = v
	}
	if v := os.Getenv("CLAWLET_SKILLS_REGISTRY_BASE_URL"); v != "" {
		cfg.Tools.Skills.Registry.BaseURL = v
	}

	if cfg.LLM.Headers == nil {
		cfg.LLM.Headers = map[string]string{}
	}
}

func splitCSV(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		s := strings.TrimSpace(p)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}

func resolveDir(dirFlag string) (wsAbs string, sessionsDir string, err error) {
	dir := strings.TrimSpace(dirFlag)
	if dir == "" {
		wsAbs, err = filepath.Abs(paths.WorkspaceDir())
		if err != nil {
			return "", "", err
		}
		return wsAbs, paths.SessionsDir(), nil
	}
	wsAbs, err = filepath.Abs(dir)
	if err != nil {
		return "", "", err
	}
	return wsAbs, filepath.Join(wsAbs, ".clawlet", "sessions"), nil
}

func providerNeedsAPIKey(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "ollama", "openai-codex":
		return false
	default:
		return true
	}
}
