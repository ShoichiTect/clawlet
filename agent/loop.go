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
	"github.com/mosaxiv/clawlet/cron"
	"github.com/mosaxiv/clawlet/llm"
	"github.com/mosaxiv/clawlet/memory"
	"github.com/mosaxiv/clawlet/paths"
	"github.com/mosaxiv/clawlet/session"
	"github.com/mosaxiv/clawlet/skills"
	"github.com/mosaxiv/clawlet/tools"
)

// Loop runs synchronous gateway turns for HTTP-over-Unix-socket callers.
type Loop struct {
	cfg   *config.Config
	model string

	runner *TurnRunner

	sessions *session.Manager
	skills   *skills.Loader

	cron *cron.Service

	verbose bool

	consolidationInFlight sync.Map
}

// LoopOptions configures a gateway Loop.
type LoopOptions struct {
	Config       *config.Config
	WorkspaceDir string
	SessionDir   string
	Model        string
	MaxIters     int
	Sessions     *session.Manager
	Skills       *skills.Loader
	Cron         *cron.Service
	Verbose      bool
}

type SessionSummary struct {
	Key          string    `json:"key"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	MessageCount int       `json:"message_count"`
	Preview      string    `json:"preview,omitempty"`
}

type SessionDetail struct {
	Key          string            `json:"key"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
	MessageCount int               `json:"message_count"`
	Messages     []session.Message `json:"messages"`
}

// NewLoop creates a gateway Loop.
func NewLoop(opts LoopOptions) (*Loop, error) {
	if opts.Config == nil {
		return nil, fmt.Errorf("config is nil")
	}
	if strings.TrimSpace(opts.WorkspaceDir) == "" {
		return nil, fmt.Errorf("workspace is empty")
	}
	ws, err := filepath.Abs(opts.WorkspaceDir)
	if err != nil {
		return nil, err
	}
	if opts.MaxIters <= 0 {
		opts.MaxIters = 20
	}
	memoryWindow := opts.Config.Agents.Defaults.MemoryWindowValue()
	model := opts.Model
	if strings.TrimSpace(model) == "" {
		model = opts.Config.LLM.Model
	}

	smgr := opts.Sessions
	if smgr == nil {
		sdir := strings.TrimSpace(opts.SessionDir)
		if sdir == "" {
			sdir = paths.SessionsDir()
		}
		if err := os.MkdirAll(sdir, 0o700); err != nil {
			return nil, err
		}
		smgr = session.NewManager(sdir)
	}
	sloader := opts.Skills
	if sloader == nil {
		sloader = skills.New(ws)
	}

	client := &llm.Client{
		Provider:    opts.Config.LLM.Provider,
		BaseURL:     opts.Config.LLM.BaseURL,
		APIKey:      opts.Config.LLM.APIKey,
		Model:       model,
		MaxTokens:   opts.Config.Agents.Defaults.MaxTokensValue(),
		Temperature: opts.Config.Agents.Defaults.Temperature,
		Headers:     opts.Config.LLM.Headers,
	}

	treg := &tools.Registry{
		WorkspaceDir:        ws,
		RestrictToWorkspace: opts.Config.Tools.RestrictToWorkspaceValue(),
		ExecTimeout:         time.Duration(opts.Config.Tools.Exec.TimeoutSec) * time.Second,
		Cron:                opts.Cron,
		ReadSkill: func(name string) (string, bool) {
			if sloader == nil {
				return "", false
			}
			return sloader.Load(name)
		},
	}
	treg.SkillRegistry, treg.SkillSearchDefaultLimit = buildSkillRegistry(opts.Config)
	memMgr, err := memory.NewIndexManager(opts.Config, ws)
	if err != nil {
		return nil, err
	}
	treg.MemorySearch = memMgr

	runner := &TurnRunner{
		LLM:                 client,
		Tools:               treg,
		Workspace:           ws,
		MaxIters:            opts.MaxIters,
		MemoryWindow:        memoryWindow,
		RestrictToWorkspace: opts.Config.Tools.RestrictToWorkspaceValue(),
		SkillsLoader:        sloader,
		Observer:            nil,
		IncludeRuntime:      false,
	}

	return &Loop{
		cfg:      opts.Config,
		model:    model,
		runner:   runner,
		sessions: smgr,
		skills:   sloader,
		cron:     opts.Cron,
		verbose:  opts.Verbose,
	}, nil
}

// ProcessDirect executes a single turn synchronously for the given session/connection.
func (l *Loop) ListSessions() ([]SessionSummary, error) {
	sessions, err := l.sessions.List()
	if err != nil {
		return nil, err
	}
	out := make([]SessionSummary, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, sessionSummary(s))
	}
	return out, nil
}

func (l *Loop) GetSession(key string) (SessionDetail, error) {
	s, err := l.sessions.LoadExisting(key)
	if err != nil {
		return SessionDetail{}, err
	}
	if s == nil {
		return SessionDetail{}, fmt.Errorf("session not found: %s", key)
	}
	return sessionDetail(s), nil
}

func (l *Loop) CreateSession(key string) (SessionDetail, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return SessionDetail{}, fmt.Errorf("session key is required")
	}
	s, err := l.sessions.Create(key)
	if err != nil {
		return SessionDetail{}, err
	}
	return sessionDetail(s), nil
}

func sessionSummary(s *session.Session) SessionSummary {
	msgs := s.History(0)
	preview := ""
	for i := len(msgs) - 1; i >= 0; i-- {
		preview = strings.TrimSpace(strings.ReplaceAll(msgs[i].Content, "\n", " "))
		if preview != "" {
			break
		}
	}
	if len(preview) > 80 {
		preview = preview[:80] + "..."
	}
	return SessionSummary{
		Key:          s.Key,
		CreatedAt:    s.CreatedAt,
		UpdatedAt:    s.UpdatedAt,
		MessageCount: len(msgs),
		Preview:      preview,
	}
}

func sessionDetail(s *session.Session) SessionDetail {
	msgs := s.History(0)
	return SessionDetail{
		Key:          s.Key,
		CreatedAt:    s.CreatedAt,
		UpdatedAt:    s.UpdatedAt,
		MessageCount: len(msgs),
		Messages:     msgs,
	}
}

func (l *Loop) ProcessDirect(ctx context.Context, content, sessionKey, channel, chatID string) (string, error) {
	out, err := l.ProcessTurn(ctx, content, sessionKey, channel, chatID)
	if err != nil {
		return "", err
	}
	return out.Final, nil
}

// ProcessTurn executes a single turn and returns the full turn output.
func (l *Loop) ProcessTurn(ctx context.Context, content, sessionKey, channel, chatID string) (TurnOutput, error) {
	return l.ProcessTurnWithObserver(ctx, content, sessionKey, channel, chatID, nil)
}

// ProcessTurnWithObserver executes a single turn and emits per-turn tool events to observer.
func (l *Loop) ProcessTurnWithObserver(ctx context.Context, content, sessionKey, channel, chatID string, observer TurnObserver) (TurnOutput, error) {
	userText := strings.TrimSpace(content)
	if strings.TrimSpace(sessionKey) == "" {
		sessionKey = "default"
	}
	return l.processDirect(ctx, llm.Message{Role: "user", Content: content}, userText, sessionKey, channel, chatID, observer)
}

func (l *Loop) processDirect(ctx context.Context, userMessage llm.Message, sessionUserText, sessionKey, channel, chatID string, observer TurnObserver) (TurnOutput, error) {
	sess, err := l.sessions.GetOrCreate(sessionKey)
	if err != nil {
		return TurnOutput{}, err
	}
	l.scheduleConsolidation(sessionKey, sess)

	out, err := l.runner.Run(ctx, TurnInput{
		UserMessage:     userMessage,
		SessionUserText: sessionUserText,
		Channel:         channel,
		ChatID:          chatID,
		Observer:        observer,
	}, sess)
	if err != nil {
		return TurnOutput{}, err
	}

	_ = l.sessions.Save(sess)
	return out, nil
}

func (l *Loop) scheduleConsolidation(sessionKey string, sess *session.Session) {
	if l == nil || sess == nil {
		return
	}
	if !sess.NeedsConsolidation(l.runner.MemoryWindow) {
		return
	}
	if _, loaded := l.consolidationInFlight.LoadOrStore(sessionKey, struct{}{}); loaded {
		return
	}
	go func() {
		defer l.consolidationInFlight.Delete(sessionKey)

		cctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		done, err := maybeConsolidateSession(cctx, l.runner.Workspace, sess, l.runner.MemoryWindow, func(ctx context.Context, currentMemory, conversation string) (string, string, error) {
			return summarizeConsolidationWithLLM(ctx, l.runner.LLM, currentMemory, conversation)
		})
		if err != nil {
			if l.verbose {
				fmt.Fprintf(os.Stderr, "consolidation error (%s): %v\n", sessionKey, err)
			}
			return
		}
		if !done {
			return
		}
		if err := l.sessions.Save(sess); err != nil && l.verbose {
			fmt.Fprintf(os.Stderr, "consolidation save error (%s): %v\n", sessionKey, err)
		}
	}()
}
