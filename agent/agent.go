package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mosaxiv/clawlet/config"
	"github.com/mosaxiv/clawlet/llm"
	"github.com/mosaxiv/clawlet/memory"
	"github.com/mosaxiv/clawlet/paths"
	"github.com/mosaxiv/clawlet/session"
	"github.com/mosaxiv/clawlet/skills"
	"github.com/mosaxiv/clawlet/tools"
)

// Options configures a CLI-mode Agent.
type Options struct {
	Config       *config.Config
	WorkspaceDir string
	SessionDir   string
	SessionKey   string
	MaxIters     int
	Verbose      bool
	ToolObserver func(ToolEvent)
}

// Agent runs in CLI mode (single-session, interactive or one-shot).
type Agent struct {
	cfg     *config.Config
	verbose bool

	runner *TurnRunner

	sessionDir string
	sess       *session.Session

	consolidationMu      sync.Mutex
	consolidationRunning bool
}

// New creates a CLI-mode Agent.
func New(opts Options) (*Agent, error) {
	if opts.Config == nil {
		return nil, fmt.Errorf("config is nil")
	}
	if strings.TrimSpace(opts.WorkspaceDir) == "" {
		return nil, fmt.Errorf("workspace is empty")
	}
	wsAbs, err := filepath.Abs(opts.WorkspaceDir)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(opts.SessionKey) == "" {
		opts.SessionKey = "cli:default"
	}
	if opts.MaxIters <= 0 {
		opts.MaxIters = 20
	}
	if err := paths.EnsureStateDirs(); err != nil {
		return nil, err
	}
	sdir := strings.TrimSpace(opts.SessionDir)
	if sdir == "" {
		sdir = paths.SessionsDir()
	}
	if err := os.MkdirAll(sdir, 0o700); err != nil {
		return nil, err
	}

	sess, err := session.Load(sdir, opts.SessionKey)
	if err != nil {
		return nil, err
	}
	if sess == nil {
		sess = session.New(opts.SessionKey)
	}

	c := &llm.Client{
		Provider:    opts.Config.LLM.Provider,
		BaseURL:     opts.Config.LLM.BaseURL,
		APIKey:      opts.Config.LLM.APIKey,
		Model:       opts.Config.LLM.Model,
		MaxTokens:   opts.Config.Agents.Defaults.MaxTokensValue(),
		Temperature: opts.Config.Agents.Defaults.Temperature,
		Headers:     opts.Config.LLM.Headers,
	}

	treg := &tools.Registry{
		WorkspaceDir:        wsAbs,
		RestrictToWorkspace: opts.Config.Tools.RestrictToWorkspaceValue(),
		ExecTimeout:         time.Duration(opts.Config.Tools.Exec.TimeoutSec) * time.Second,
		ReadSkill: func(name string) (string, bool) {
			l := skills.New(wsAbs)
			return l.Load(name)
		},
	}
	treg.SkillRegistry, treg.SkillSearchDefaultLimit = buildSkillRegistry(opts.Config)
	memMgr, err := memory.NewIndexManager(opts.Config, wsAbs)
	if err != nil {
		return nil, err
	}
	treg.MemorySearch = memMgr

	runner := &TurnRunner{
		LLM:                 c,
		Tools:               treg,
		Workspace:           wsAbs,
		MaxIters:            opts.MaxIters,
		MemoryWindow:        opts.Config.Agents.Defaults.MemoryWindowValue(),
		RestrictToWorkspace: opts.Config.Tools.RestrictToWorkspaceValue(),
		SkillsLoader:        nil, // CLI does not show skills in prompt
		Observer:            opts.ToolObserver,
		IncludeRuntime:      true,
	}

	return &Agent{
		cfg:        opts.Config,
		verbose:    opts.Verbose,
		runner:     runner,
		sessionDir: sdir,
		sess:       sess,
	}, nil
}

// Process executes a single user input turn and returns the final assistant response.
func (a *Agent) Process(ctx context.Context, input string) (string, error) {
	a.scheduleConsolidation()

	out, err := a.runner.Run(ctx, TurnInput{
		UserMessage:     llm.Message{Role: "user", Content: input},
		SessionUserText: input,
		Channel:         "cli",
		ChatID:          "direct",
	}, a.sess)
	if err != nil {
		return "", err
	}

	_ = session.Save(a.sessionDir, a.sess)
	return out.Final, nil
}

func (a *Agent) scheduleConsolidation() {
	if a == nil || a.sess == nil {
		return
	}
	if !a.sess.NeedsConsolidation(a.runner.MemoryWindow) {
		return
	}

	a.consolidationMu.Lock()
	if a.consolidationRunning {
		a.consolidationMu.Unlock()
		return
	}
	a.consolidationRunning = true
	a.consolidationMu.Unlock()

	go func() {
		defer func() {
			a.consolidationMu.Lock()
			a.consolidationRunning = false
			a.consolidationMu.Unlock()
		}()

		cctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		done, err := maybeConsolidateSession(cctx, a.runner.Workspace, a.sess, a.runner.MemoryWindow, func(ctx context.Context, currentMemory, conversation string) (string, string, error) {
			return summarizeConsolidationWithLLM(ctx, a.runner.LLM, currentMemory, conversation)
		})
		if err != nil {
			if a.verbose {
				fmt.Fprintf(os.Stderr, "consolidation error: %v\n", err)
			}
			return
		}
		if !done {
			return
		}
		if err := session.Save(a.sessionDir, a.sess); err != nil && a.verbose {
			fmt.Fprintf(os.Stderr, "consolidation save error: %v\n", err)
		}
	}()
}
