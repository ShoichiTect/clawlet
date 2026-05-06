package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mosaxiv/clawlet/bus"
	"github.com/mosaxiv/clawlet/config"
	"github.com/mosaxiv/clawlet/cron"
	"github.com/mosaxiv/clawlet/llm"
	"github.com/mosaxiv/clawlet/media"
	"github.com/mosaxiv/clawlet/memory"
	"github.com/mosaxiv/clawlet/session"
	"github.com/mosaxiv/clawlet/skills"
	"github.com/mosaxiv/clawlet/tools"
)

// Loop runs the gateway agent loop, consuming inbound messages and producing outbound replies.
type Loop struct {
	cfg   *config.Config
	model string

	runner *TurnRunner

	bus      *bus.Bus
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
	Model        string
	MaxIters     int
	Bus          *bus.Bus
	Sessions     *session.Manager
	Skills       *skills.Loader
	Cron         *cron.Service
	Spawn        func(ctx context.Context, task, label, originChannel, originChatID string) (string, error)
	Verbose      bool
}

// NewLoop creates a gateway Loop.
func NewLoop(opts LoopOptions) (*Loop, error) {
	if opts.Config == nil {
		return nil, fmt.Errorf("config is nil")
	}
	if opts.Bus == nil {
		return nil, fmt.Errorf("bus is nil")
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
		return nil, fmt.Errorf("sessions manager is nil")
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
		WorkspaceDir:           ws,
		RestrictToWorkspace:    opts.Config.Tools.RestrictToWorkspaceValue(),
		ExecTimeout:            time.Duration(opts.Config.Tools.Exec.TimeoutSec) * time.Second,
		BraveAPIKey:            opts.Config.Tools.Web.BraveAPIKey,
		WebFetchAllowedDomains: append([]string(nil), opts.Config.Tools.Web.AllowedDomains...),
		WebFetchBlockedDomains: append([]string(nil), opts.Config.Tools.Web.BlockedDomains...),
		WebFetchMaxResponse:    opts.Config.Tools.Web.MaxResponseBytes,
		WebFetchTimeout:        time.Duration(opts.Config.Tools.Web.FetchTimeoutSec) * time.Second,
		Outbound: func(ctx context.Context, msg bus.OutboundMessage) error {
			return opts.Bus.PublishOutbound(ctx, msg)
		},
		Spawn: opts.Spawn,
		Cron:  opts.Cron,
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
		Observer:            nil, // gateway does not use ToolObserver yet
		IncludeRuntime:      false,
	}

	return &Loop{
		cfg:     opts.Config,
		model:   model,
		runner:  runner,
		bus:     opts.Bus,
		sessions: smgr,
		skills:  sloader,
		cron:    opts.Cron,
		verbose: opts.Verbose,
	}, nil
}

// SetSpawn replaces the Spawn callback on the tool registry.
func (l *Loop) SetSpawn(fn func(ctx context.Context, task, label, originChannel, originChatID string) (string, error)) {
	if l == nil || l.runner == nil || l.runner.Tools == nil {
		return
	}
	l.runner.Tools.Spawn = fn
}

// Run starts the main consume loop. Blocks until the context is cancelled.
func (l *Loop) Run(ctx context.Context) error {
	for {
		msg, err := l.bus.ConsumeInbound(ctx)
		if err != nil {
			return err
		}
		out, omsg, err := l.processInbound(ctx, msg)
		_ = out
		if err != nil {
			if omsg.Channel != "" && omsg.ChatID != "" {
				omsg.Content = "error: " + err.Error()
				_ = l.bus.PublishOutbound(ctx, omsg)
			}
			continue
		}
		if omsg.Channel != "" && omsg.ChatID != "" && strings.TrimSpace(omsg.Content) != "" {
			_ = l.bus.PublishOutbound(ctx, omsg)
		}
	}
}

// ProcessDirect executes a single turn synchronously for the given session/channel/chatID.
func (l *Loop) ProcessDirect(ctx context.Context, content, sessionKey, channel, chatID string) (string, error) {
	userText := strings.TrimSpace(content)
	return l.processDirect(ctx, llm.Message{Role: "user", Content: content}, userText, sessionKey, channel, chatID)
}

func (l *Loop) processInbound(ctx context.Context, msg bus.InboundMessage) (string, bus.OutboundMessage, error) {
	// System message is used by subagents to announce back to origin.
	if msg.Channel == "system" {
		originCh, originChat := parseOrigin(msg.ChatID)
		if originCh == "" || originChat == "" {
			originCh = "cli"
			originChat = msg.ChatID
		}
		sk := originCh + ":" + originChat
		res, err := l.processDirect(ctx, llm.Message{Role: "user", Content: msg.Content}, msg.Content, sk, originCh, originChat)
		return res, bus.OutboundMessage{Channel: originCh, ChatID: originChat, Content: res}, err
	}

	sessionKey := msg.SessionKey
	if strings.TrimSpace(sessionKey) == "" {
		sessionKey = msg.Channel + ":" + msg.ChatID
	}
	userInput, err := media.PrepareInbound(ctx, l.runner.LLM, l.cfg.Tools.Media, msg)
	if err != nil {
		return "", bus.OutboundMessage{}, err
	}
	sessionText := strings.TrimSpace(userInput.SessionText)
	if sessionText == "" {
		sessionText = strings.TrimSpace(msg.Content)
	}
	res, err := l.processDirect(ctx, userInput.UserMessage, sessionText, sessionKey, msg.Channel, msg.ChatID)
	return res, bus.OutboundMessage{
		Channel:  msg.Channel,
		ChatID:   msg.ChatID,
		Content:  res,
		Delivery: msg.Delivery,
	}, err
}

func (l *Loop) processDirect(ctx context.Context, userMessage llm.Message, sessionUserText, sessionKey, channel, chatID string) (string, error) {
	sess, err := l.sessions.GetOrCreate(sessionKey)
	if err != nil {
		return "", err
	}
	l.scheduleConsolidation(sessionKey, sess)

	out, err := l.runner.Run(ctx, TurnInput{
		UserMessage:     userMessage,
		SessionUserText: sessionUserText,
		Channel:         channel,
		ChatID:          chatID,
	}, sess)
	if err != nil {
		return "", err
	}

	_ = l.sessions.Save(sess)
	return out.Final, nil
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

func parseOrigin(chatID string) (string, string) {
	if before, after, ok := strings.Cut(chatID, ":"); ok {
		return before, after
	}
	return "", ""
}
