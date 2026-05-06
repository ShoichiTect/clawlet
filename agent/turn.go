package agent

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/mosaxiv/clawlet/llm"
	"github.com/mosaxiv/clawlet/session"
	"github.com/mosaxiv/clawlet/skills"
	"github.com/mosaxiv/clawlet/tools"
)

// TurnObserver receives lifecycle events during turn execution.
// It is called for tool start/end events.
type TurnObserver func(ev ToolEvent)

// ToolPhase marks whether a tool event is at start or end of execution.
type ToolPhase int

const (
	ToolStart ToolPhase = iota
	ToolEnd
)

// ToolEvent carries information about a tool execution lifecycle event.
type ToolEvent struct {
	Phase    ToolPhase
	Name     string
	Args     string
	Output   string
	Error    string
	Duration time.Duration
}

// TurnInput carries everything needed to execute one turn.
type TurnInput struct {
	UserMessage     llm.Message // full message for LLM (may include media/image parts)
	SessionUserText string      // text to store in session history for the user message
	Channel         string
	ChatID          string
}

// TurnOutput holds the result of a turn.
type TurnOutput struct {
	Final     string
	ToolsUsed []string
}

// TurnRunner executes a single conversational turn:
// system prompt → history → LLM tool-call loop → session update.
//
// It does NOT persist the session; callers must do that themselves so
// they can choose between direct file save (CLI) and manager save (gateway).
type TurnRunner struct {
	LLM                 *llm.Client
	Tools               *tools.Registry
	Workspace           string
	MaxIters            int
	MemoryWindow        int
	RestrictToWorkspace bool
	SkillsLoader        *skills.Loader
	Observer            TurnObserver
	IncludeRuntime      bool
}

// Run executes the turn and mutates sess in-memory (sess.Add / AddWithTools).
// Callers must persist the session afterwards via their own save mechanism.
func (tr *TurnRunner) Run(ctx context.Context, input TurnInput, sess *session.Session) (TurnOutput, error) {
	sys := BuildSystemPrompt(PromptOpts{
		Workspace:           tr.Workspace,
		RestrictToWorkspace: tr.RestrictToWorkspace,
		Channel:             input.Channel,
		ChatID:              input.ChatID,
		SkillsLoader:        tr.SkillsLoader,
		IncludeRuntime:      tr.IncludeRuntime,
	})

	history := sess.History(tr.MemoryWindow)
	messages := make([]llm.Message, 0, 1+len(history)+1)
	messages = append(messages, llm.Message{Role: "system", Content: sys})
	for _, m := range history {
		messages = append(messages, llm.Message{Role: m.Role, Content: m.Content})
	}
	messages = append(messages, input.UserMessage)

	toolsDefs := tr.Tools.Definitions()

	var final string
	toolsUsed := make([]string, 0, 8)
	for iter := 0; iter < tr.MaxIters; iter++ {
		res, err := tr.LLM.Chat(ctx, messages, toolsDefs)
		if err != nil {
			return TurnOutput{}, err
		}
		if res.HasToolCalls() {
			for _, tc := range res.ToolCalls {
				toolsUsed = append(toolsUsed, tc.Name)
			}
			messages = appendToolRound(messages, res.Content, res.ToolCalls, func(tc llm.ToolCall) string {
				argsPreview := previewJSON(tc.Arguments, 200)
				if tr.Observer != nil {
					tr.Observer(ToolEvent{Phase: ToolStart, Name: tc.Name, Args: argsPreview})
				}
				start := time.Now()
				out, err := tr.Tools.Execute(ctx, tools.Context{
					Channel:    input.Channel,
					ChatID:     input.ChatID,
					SessionKey: sess.Key,
				}, tc.Name, tc.Arguments)
				dur := time.Since(start)
				if tr.Observer != nil {
					ev := ToolEvent{Phase: ToolEnd, Name: tc.Name, Duration: dur}
					if err != nil {
						ev.Error = err.Error()
					} else {
						ev.Output = out
					}
					tr.Observer(ev)
				}
				if err != nil {
					return "error: " + err.Error()
				}
				return out
			})
			continue
		}
		final = res.Content
		break
	}
	if strings.TrimSpace(final) == "" {
		final = "(no response)"
	}

	sess.Add("user", input.SessionUserText)
	sess.AddWithTools("assistant", final, toolsUsed)

	return TurnOutput{Final: final, ToolsUsed: toolsUsed}, nil
}

// previewJSON returns a truncated preview of raw JSON for logging.
func previewJSON(b json.RawMessage, max int) string {
	s := strings.TrimSpace(string(b))
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
