package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mosaxiv/clawlet/memory"
	"github.com/mosaxiv/clawlet/skills"
)

// PromptOpts controls what goes into the system prompt.
// Zero value corresponds to CLI mode.
type PromptOpts struct {
	Workspace           string
	RestrictToWorkspace bool
	Channel             string // empty means CLI mode
	ChatID              string
	SkillsLoader        *skills.Loader
	IncludeRuntime      bool // CLI includes runtime info
}

// BuildSystemPrompt constructs the system prompt shared by CLI and gateway.
func BuildSystemPrompt(opts PromptOpts) string {
	now := time.Now().Format("2006-01-02 15:04 (Mon)")

	var b strings.Builder
	b.WriteString("# clawlet\n\n")
	b.WriteString("You are clawlet, a helpful AI assistant.\n")

	if opts.Channel == "" {
		// CLI mode: simpler tool description.
		b.WriteString("You can use tools to read/write/edit files, list directories, execute shell commands, and fetch/search the web.\n\n")
		b.WriteString("IMPORTANT: Reply with plain text. Do not call the message tool.\n\n")
	} else {
		// Gateway mode: mentions message/spawn/cron tools.
		b.WriteString("You can use tools to read/write/edit files, list directories, execute shell commands, fetch/search the web, schedule tasks, and spawn background subagents.\n\n")
		b.WriteString("IMPORTANT: When replying to the current conversation, respond with plain text. Do not call the message tool.\n")
		b.WriteString("Only use the message tool when you must send to a different channel/chat_id.\n\n")
	}

	b.WriteString("## Current Time\n")
	b.WriteString(now + "\n\n")

	if opts.IncludeRuntime {
		rt := fmt.Sprintf("%s/%s Go %s", runtime.GOOS, runtime.GOARCH, runtime.Version())
		b.WriteString("## Runtime\n")
		b.WriteString(rt + "\n\n")
	}

	b.WriteString("## Workspace\n")
	b.WriteString(opts.Workspace + "\n\n")

	if opts.RestrictToWorkspace {
		b.WriteString("## Safety\nTools are restricted to the workspace directory.\n\n")
	}

	if opts.Channel != "" && opts.ChatID != "" {
		b.WriteString("## Current Session\n")
		b.WriteString("Channel: " + opts.Channel + "\nChat ID: " + opts.ChatID + "\n\n")
	}

	// Bootstrap files from workspace (optional).
	for _, fn := range []string{"AGENTS.md", "SOUL.md", "USER.md", "TOOLS.md", "IDENTITY.md"} {
		p := filepath.Join(opts.Workspace, fn)
		if bb, err := os.ReadFile(p); err == nil && len(bb) > 0 {
			b.WriteString("## " + fn + "\n\n")
			b.Write(bb)
			if bb[len(bb)-1] != '\n' {
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}
	}

	// Memory (long-term + today's notes).
	mem := memory.New(opts.Workspace).GetContext()
	if strings.TrimSpace(mem) != "" {
		b.WriteString("# Memory\n\n")
		b.WriteString(mem)
		b.WriteString("\n\n")
	}

	// Skills summary (gateway only).
	if opts.SkillsLoader != nil {
		sum := opts.SkillsLoader.SummaryXML()
		if sum != "" {
			b.WriteString("# Skills\n\n")
			b.WriteString("To use a skill:\n- workspace skills: read_file(path)\n- bundled skills: read_skill(name)\n\n")
			b.WriteString(sum + "\n\n")
		}
	}

	return b.String()
}
