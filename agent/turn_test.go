package agent

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/mosaxiv/clawlet/llm"
	"github.com/mosaxiv/clawlet/session"
	"github.com/mosaxiv/clawlet/tools"
)

// fakeHTTPDoer serves canned JSON responses in sequence.
type fakeHTTPDoer struct {
	responses []string
	callCount int
}

func (f *fakeHTTPDoer) Do(req *http.Request) (*http.Response, error) {
	if f.callCount >= len(f.responses) {
		body := `{"choices":[]}`
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	}
	body := f.responses[f.callCount]
	f.callCount++
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func newFakeLLM(responses ...string) *llm.Client {
	return &llm.Client{
		Provider: "openai",
		BaseURL:  "http://fake",
		Model:    "fake-model",
		HTTP:     &fakeHTTPDoer{responses: responses},
	}
}

func TestTurnRunner_TextOnly(t *testing.T) {
	client := newFakeLLM(`{"choices":[{"message":{"content":"Hello, world!"}}]}`)

	tr := &TurnRunner{LLM: client, Tools: emptyTools(t), MaxIters: 5, MemoryWindow: 50}
	sess := session.New("test")

	out, err := tr.Run(context.Background(), TurnInput{
		UserMessage:     llm.Message{Role: "user", Content: "hi"},
		SessionUserText: "hi",
	}, sess)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if out.Final != "Hello, world!" {
		t.Fatalf("unexpected final: %q", out.Final)
	}
	if len(out.ToolsUsed) != 0 {
		t.Fatalf("expected no tools, got %v", out.ToolsUsed)
	}

	history := sess.History(50)
	if len(history) != 2 {
		t.Fatalf("expected 2 messages in session, got %d", len(history))
	}
	if history[0].Role != "user" || history[0].Content != "hi" {
		t.Fatalf("unexpected user message: %+v", history[0])
	}
	if history[1].Role != "assistant" || history[1].Content != "Hello, world!" {
		t.Fatalf("unexpected assistant message: %+v", history[1])
	}
}

func TestTurnRunner_EmptyResponse_Fallback(t *testing.T) {
	client := newFakeLLM(`{"choices":[{"message":{"content":"  "}}]}`)

	tr := &TurnRunner{LLM: client, Tools: emptyTools(t), MaxIters: 5, MemoryWindow: 50}
	sess := session.New("test")

	out, err := tr.Run(context.Background(), TurnInput{
		UserMessage:     llm.Message{Role: "user", Content: "hi"},
		SessionUserText: "hi",
	}, sess)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if out.Final != "(no response)" {
		t.Fatalf("expected fallback, got %q", out.Final)
	}
}

func TestTurnRunner_Observer_NoTools(t *testing.T) {
	client := newFakeLLM(`{"choices":[{"message":{"content":"OK"}}]}`)

	var events []ToolEvent
	tr := &TurnRunner{
		LLM:          client,
		Tools:        emptyTools(t),
		MaxIters:     5,
		MemoryWindow: 50,
		Observer: func(ev ToolEvent) {
			events = append(events, ev)
		},
	}
	sess := session.New("test")

	_, err := tr.Run(context.Background(), TurnInput{
		UserMessage:     llm.Message{Role: "user", Content: "hi"},
		SessionUserText: "hi",
	}, sess)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no tool events, got %d", len(events))
	}
}

func TestTurnRunner_ToolCallThenText(t *testing.T) {
	client := newFakeLLM(
		`{"choices":[{"message":{"content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"/tmp/fake\"}"}}]}}]}`,
		`{"choices":[{"message":{"content":"File contents: hello"}}]}`,
	)

	tr := &TurnRunner{
		LLM:          client,
		Tools:        readOnlyTools(t),
		MaxIters:     5,
		MemoryWindow: 50,
	}
	sess := session.New("test")

	out, err := tr.Run(context.Background(), TurnInput{
		UserMessage:     llm.Message{Role: "user", Content: "read file"},
		SessionUserText: "read file",
	}, sess)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if out.Final != "File contents: hello" {
		t.Fatalf("unexpected final: %q", out.Final)
	}
	if len(out.ToolsUsed) != 1 || out.ToolsUsed[0] != "read_file" {
		t.Fatalf("expected [read_file], got %v", out.ToolsUsed)
	}

	history := sess.History(50)
	if len(history) != 2 {
		t.Fatalf("expected 2 messages in session, got %d", len(history))
	}
	if len(history[1].ToolsUsed) != 1 || history[1].ToolsUsed[0] != "read_file" {
		t.Fatalf("unexpected tools_used: %v", history[1].ToolsUsed)
	}
}

func TestTurnRunner_Observer_WithTools(t *testing.T) {
	client := newFakeLLM(
		`{"choices":[{"message":{"content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"/tmp/fake\"}"}}]}}]}`,
		`{"choices":[{"message":{"content":"Done"}}]}`,
	)

	var events []ToolEvent
	tr := &TurnRunner{
		LLM:          client,
		Tools:        readOnlyTools(t),
		MaxIters:     5,
		MemoryWindow: 50,
		Observer: func(ev ToolEvent) {
			events = append(events, ev)
		},
	}
	sess := session.New("test")

	_, err := tr.Run(context.Background(), TurnInput{
		UserMessage:     llm.Message{Role: "user", Content: "read file"},
		SessionUserText: "read file",
	}, sess)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("expected 2 tool events (start+end), got %d", len(events))
	}
	if events[0].Phase != ToolStart || events[0].Name != "read_file" {
		t.Fatalf("unexpected start event: %+v", events[0])
	}
	if events[1].Phase != ToolEnd || events[1].Name != "read_file" {
		t.Fatalf("unexpected end event: %+v", events[1])
	}
}

func TestTurnRunner_MaxIters_Exceeded(t *testing.T) {
	var responses []string
	for range 10 {
		responses = append(responses, `{"choices":[{"message":{"content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"/tmp/fake\"}"}}]}}]}`)
	}
	client := newFakeLLM(responses...)

	tr := &TurnRunner{
		LLM:          client,
		Tools:        readOnlyTools(t),
		MaxIters:     3,
		MemoryWindow: 50,
	}
	sess := session.New("test")

	out, err := tr.Run(context.Background(), TurnInput{
		UserMessage:     llm.Message{Role: "user", Content: "hi"},
		SessionUserText: "hi",
	}, sess)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if out.Final != "(no response)" {
		t.Fatalf("expected (no response), got %q", out.Final)
	}
	if len(out.ToolsUsed) != 3 {
		t.Fatalf("expected 3 tools used, got %d (%v)", len(out.ToolsUsed), out.ToolsUsed)
	}
}

func TestBuildSystemPrompt_CLI(t *testing.T) {
	ws := t.TempDir()
	s := BuildSystemPrompt(PromptOpts{
		Workspace:           ws,
		RestrictToWorkspace: true,
		IncludeRuntime:      true,
	})
	if !strings.Contains(s, "# clawlet") {
		t.Fatal("missing header")
	}
	if !strings.Contains(s, "## Current Time") {
		t.Fatal("missing Current Time")
	}
	if !strings.Contains(s, "## Runtime") {
		t.Fatal("missing Runtime section (CLI mode)")
	}
	if !strings.Contains(s, "## Workspace") {
		t.Fatal("missing Workspace")
	}
	if !strings.Contains(s, "## Safety") {
		t.Fatal("missing Safety section")
	}
	if strings.Contains(s, "## Current Session") {
		t.Fatal("unexpected Current Session in CLI mode")
	}
	if strings.Contains(s, "# Skills") {
		t.Fatal("unexpected Skills in CLI mode")
	}
}

func TestBuildSystemPrompt_Gateway(t *testing.T) {
	ws := t.TempDir()
	s := BuildSystemPrompt(PromptOpts{
		Workspace:           ws,
		RestrictToWorkspace: false,
		Channel:             "tui",
		ChatID:              "default",
		IncludeRuntime:      false,
	})
	if !strings.Contains(s, "# clawlet") {
		t.Fatal("missing header")
	}
	if strings.Contains(s, "## Runtime") {
		t.Fatal("unexpected Runtime in gateway mode")
	}
	if !strings.Contains(s, "## Current Session") {
		t.Fatal("missing Current Session section")
	}
	if !strings.Contains(s, "Connection: tui") {
		t.Fatal("missing connection info")
	}
	if !strings.Contains(s, "Chat ID: default") {
		t.Fatal("missing chat ID info")
	}
}

func TestBuildSystemPrompt_BootstrapFiles(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(ws+"/AGENTS.md", []byte("test agents content"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := BuildSystemPrompt(PromptOpts{Workspace: ws})
	if !strings.Contains(s, "## AGENTS.md") {
		t.Fatal("missing AGENTS.md bootstrap")
	}
	if !strings.Contains(s, "test agents content") {
		t.Fatal("missing bootstrap content")
	}
}

func TestPreviewJSON(t *testing.T) {
	short := `{"a":1}`
	if out := previewJSON([]byte(short), 200); out != short {
		t.Fatalf("short preview: %q", out)
	}

	long := `{"` + strings.Repeat("x", 300) + `":"value"}`
	out := previewJSON([]byte(long), 200)
	if !strings.HasSuffix(out, "...") {
		t.Fatalf("expected truncation: %q", out)
	}
	if len(out) != 203 { // 200 + "..."
		t.Fatalf("wrong truncated length: %d", len(out))
	}
}

func emptyTools(t *testing.T) *tools.Registry {
	t.Helper()
	return &tools.Registry{
		WorkspaceDir:        t.TempDir(),
		RestrictToWorkspace: true,
		AllowTools:          []string{},
	}
}

func readOnlyTools(t *testing.T) *tools.Registry {
	t.Helper()
	return &tools.Registry{
		WorkspaceDir:        t.TempDir(),
		RestrictToWorkspace: false,
		AllowTools:          []string{"read_file"},
	}
}
