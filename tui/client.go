package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mosaxiv/clawlet/agent"
	"github.com/mosaxiv/clawlet/config"
	"github.com/mosaxiv/clawlet/session"
)

// ---- Domain types (shared between runner and model) ----

type ChatStreamEvent struct {
	Type       string   `json:"type"`
	Name       string   `json:"name,omitempty"`
	Args       string   `json:"args,omitempty"`
	Output     string   `json:"output,omitempty"`
	Error      string   `json:"error,omitempty"`
	DurationMS int64    `json:"duration_ms,omitempty"`
	Content    string   `json:"content,omitempty"`
	ToolsUsed  []string `json:"tools_used,omitempty"`
}

type SessionSummary struct {
	Key          string    `json:"key"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	MessageCount int       `json:"message_count"`
	Preview      string    `json:"preview,omitempty"`
}

type SessionMessage struct {
	Role      string   `json:"role"`
	Content   string   `json:"content"`
	Timestamp string   `json:"timestamp,omitempty"`
	ToolsUsed []string `json:"tools_used,omitempty"`
}

type SessionDetail struct {
	Key          string           `json:"key"`
	CreatedAt    time.Time        `json:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
	MessageCount int              `json:"message_count"`
	Messages     []SessionMessage `json:"messages"`
}

type ChatResponse struct {
	Content   string   `json:"content"`
	ToolsUsed []string `json:"tools_used"`
}

// ---- Runner ----

// RunTurn executes a single user message via agent.Agent, streaming
// tool events through a channel.
func RunTurn(ctx context.Context, cfg *config.Config, workspace, message, sessionKey string, maxIters int) (<-chan ChatStreamEvent, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil, fmt.Errorf("message is required")
	}
	if strings.TrimSpace(sessionKey) == "" {
		sessionKey = defaultSessionKey
	}
	if maxIters <= 0 {
		maxIters = 20
	}

	wsAbs, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	sessionsDir := filepath.Join(wsAbs, ".clawlet", "sessions")

	events := make(chan ChatStreamEvent, 16)
	var toolsUsed []string

	a, err := agent.New(agent.Options{
		Config:       cfg,
		WorkspaceDir: wsAbs,
		SessionDir:   sessionsDir,
		SessionKey:   sessionKey,
		MaxIters:     maxIters,
		ToolObserver: func(ev agent.ToolEvent) {
			switch ev.Phase {
			case agent.ToolStart:
				events <- ChatStreamEvent{Type: "tool_start", Name: ev.Name, Args: ev.Args}
			case agent.ToolEnd:
				if ev.Error == "" {
					toolsUsed = append(toolsUsed, ev.Name)
				}
				events <- ChatStreamEvent{
					Type:       "tool_end",
					Name:       ev.Name,
					Output:     ev.Output,
					Error:      ev.Error,
					DurationMS: ev.Duration.Milliseconds(),
				}
			}
		},
	})
	if err != nil {
		return nil, err
	}

	go func() {
		defer close(events)

		ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()

		final, err := a.Process(ctx, message)
		if err != nil {
			events <- ChatStreamEvent{Type: "error", Error: err.Error()}
		} else {
			events <- ChatStreamEvent{
				Type:      "assistant_final",
				Content:   final,
				ToolsUsed: toolsUsed,
			}
		}
		events <- ChatStreamEvent{Type: "done"}
	}()

	return events, nil
}

// ListSessions returns all sessions for a workspace.
func ListSessions(workspace string) ([]SessionSummary, error) {
	wsAbs, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	sessionsDir := filepath.Join(wsAbs, ".clawlet", "sessions")
	all, err := session.List(sessionsDir)
	if err != nil {
		return nil, err
	}
	out := make([]SessionSummary, 0, len(all))
	for _, s := range all {
		if s == nil {
			continue
		}
		preview := ""
		if len(s.Messages) > 0 {
			last := s.Messages[len(s.Messages)-1]
			content := strings.TrimSpace(last.Content)
			if len(content) > 80 {
				content = content[:80]
			}
			preview = content
		}
		out = append(out, SessionSummary{
			Key:          s.Key,
			CreatedAt:    s.CreatedAt,
			UpdatedAt:    s.UpdatedAt,
			MessageCount: len(s.Messages),
			Preview:      preview,
		})
	}
	return out, nil
}

// GetSession returns full session detail.
func GetSession(workspace, key string) (SessionDetail, error) {
	wsAbs, err := filepath.Abs(workspace)
	if err != nil {
		return SessionDetail{}, err
	}
	sessionsDir := filepath.Join(wsAbs, ".clawlet", "sessions")
	s, err := session.Load(sessionsDir, key)
	if err != nil {
		return SessionDetail{}, err
	}
	if s == nil {
		return SessionDetail{Key: key}, nil
	}
	msgs := make([]SessionMessage, 0, len(s.Messages))
	for _, m := range s.Messages {
		msgs = append(msgs, SessionMessage{
			Role:      m.Role,
			Content:   m.Content,
			Timestamp: m.Timestamp,
			ToolsUsed: m.ToolsUsed,
		})
	}
	return SessionDetail{
		Key:          s.Key,
		CreatedAt:    s.CreatedAt,
		UpdatedAt:    s.UpdatedAt,
		MessageCount: len(s.Messages),
		Messages:     msgs,
	}, nil
}

// CreateSession creates a new empty session.
func CreateSession(workspace, key string) (SessionDetail, error) {
	wsAbs, err := filepath.Abs(workspace)
	if err != nil {
		return SessionDetail{}, err
	}
	sessionsDir := filepath.Join(wsAbs, ".clawlet", "sessions")
	s := session.New(key)
	s.CreatedAt = time.Now()
	s.UpdatedAt = time.Now()
	if err := session.Save(sessionsDir, s); err != nil {
		return SessionDetail{}, err
	}
	return SessionDetail{
		Key:       s.Key,
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
	}, nil
}

// CheckProject verifies a workspace directory exists and can be used.
func CheckProject(workspace string) error {
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return err
	}
	info, err := os.Stat(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("directory not found: %s", abs)
		}
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", abs)
	}
	return nil
}
