package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type screenMode int

const (
	screenProjects screenMode = iota
	screenChat
)

type promptMode int

const (
	promptNone promptMode = iota
	promptAddProject
	promptNewSession
)

type chatMode int

const (
	chatModeInput chatMode = iota
	chatModeSessions
	chatModeSessionPreview
)

type chatMessage struct {
	Role    string
	Content string
}

type model struct {
	state    State
	projects []ProjectView
	cursor   int
	selected ProjectView

	screen screenMode
	prompt promptMode

	chatInput   textinput.Model
	promptInput textinput.Model

	messages    []chatMessage
	latestTools []string

	chatMode       chatMode
	sessions       []SessionSummary
	sessionCursor  int
	sessionPreview SessionDetail
	sessionLoading bool

	width  int
	height int

	scanning bool
	chatting bool
	status   string
	errorMsg string
}

type scanDoneMsg struct {
	projects []ProjectView
}

type chatDoneMsg struct {
	response ChatResponse
	status   GatewayStatus
	err      error
}

type chatStreamStartedMsg struct {
	events <-chan ChatStreamEvent
}

type chatStreamEventMsg struct {
	event  ChatStreamEvent
	events <-chan ChatStreamEvent
}

type chatStreamClosedMsg struct{}

type sessionsDoneMsg struct {
	sessions []SessionSummary
	err      error
}

type sessionPreviewDoneMsg struct {
	detail SessionDetail
	err    error
}

type sessionCreateDoneMsg struct {
	detail SessionDetail
	err    error
}

type healthDoneMsg GatewayResult

var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	helpStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("248"))
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	warnStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("248"))
	userStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	agentStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	systemStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
	toolStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("220"))
)

func newModel(st State) model {
	chat := textinput.New()
	chat.Prompt = ""
	chat.Placeholder = "type a message"
	chat.CharLimit = 0
	chat.Width = 80
	chat.Focus()

	prompt := textinput.New()
	prompt.CharLimit = 0
	prompt.Width = 80

	return model{
		state:       normalizeState(st),
		projects:    DiscoverProjects(st),
		chatInput:   chat,
		promptInput: prompt,
		status:      "scanning projects...",
		scanning:    true,
	}
}

func (m model) Init() tea.Cmd {
	return scanProjectsCmd(m.state)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		inputWidth := msg.Width - 12
		if inputWidth < 20 {
			inputWidth = 20
		}
		m.chatInput.Width = inputWidth
		m.promptInput.Width = inputWidth
		return m, nil
	case scanDoneMsg:
		m.scanning = false
		m.projects = msg.projects
		if m.cursor >= len(m.projects) {
			m.cursor = len(m.projects) - 1
		}
		if m.cursor < 0 {
			m.cursor = 0
		}
		m.status = fmt.Sprintf("found %d project(s)", len(m.projects))
		m.errorMsg = ""
		return m, nil
	case chatDoneMsg:
		m.chatting = false
		if m.screen != screenChat {
			return m, nil
		}
		if msg.err != nil {
			m.latestTools = nil
			errorMsg := chatMessage{Role: "error", Content: fmt.Sprintf("%s: %v", msg.status, msg.err)}
			m.messages = append(m.messages, errorMsg)
			m.errorMsg = msg.err.Error()
			return m, tea.Println(renderTranscriptMessage(errorMsg, nil))
		}
		m.latestTools = msg.response.ToolsUsed
		assistantMsg := chatMessage{Role: "assistant", Content: msg.response.Content}
		m.messages = append(m.messages, assistantMsg)
		m.status = "response received"
		m.errorMsg = ""
		return m, tea.Println(renderTranscriptMessage(assistantMsg, msg.response.ToolsUsed))
	case chatStreamStartedMsg:
		return m, readChatStreamCmd(msg.events)
	case chatStreamEventMsg:
		event := msg.event
		switch event.Type {
		case "tool_start", "tool_end":
			m.status = "tool event received"
			if event.Error != "" {
				m.errorMsg = event.Error
			}
			return m, tea.Sequence(tea.Println(renderChatStreamEvent(event)), readChatStreamCmd(msg.events))
		case "assistant_final":
			m.latestTools = event.ToolsUsed
			assistantMsg := chatMessage{Role: "assistant", Content: event.Content}
			m.messages = append(m.messages, assistantMsg)
			m.status = "response received"
			m.errorMsg = ""
			return m, tea.Sequence(tea.Println(renderTranscriptMessage(assistantMsg, event.ToolsUsed)), readChatStreamCmd(msg.events))
		case "error":
			m.latestTools = nil
			m.errorMsg = event.Error
			errorMsg := chatMessage{Role: "error", Content: event.Error}
			m.messages = append(m.messages, errorMsg)
			m.status = "error"
			return m, tea.Sequence(tea.Println(renderTranscriptMessage(errorMsg, nil)), readChatStreamCmd(msg.events))
		case "done":
			m.chatting = false
			if m.status == "sending message..." {
				m.status = "done"
			}
			return m, readChatStreamCmd(msg.events)
		default:
			return m, readChatStreamCmd(msg.events)
		}
	case chatStreamClosedMsg:
		m.chatting = false
		if m.status == "sending message..." {
			m.status = "stream closed"
		}
		return m, nil
	case sessionsDoneMsg:
		m.sessionLoading = false
		if msg.err != nil {
			m.errorMsg = msg.err.Error()
			m.status = "session list error"
			return m, nil
		}
		m.sessions = msg.sessions
		if m.sessionCursor >= len(m.sessions) {
			m.sessionCursor = len(m.sessions) - 1
		}
		if m.sessionCursor < 0 {
			m.sessionCursor = 0
		}
		m.status = fmt.Sprintf("found %d session(s)", len(m.sessions))
		m.errorMsg = ""
		return m, nil
	case sessionPreviewDoneMsg:
		m.sessionLoading = false
		if msg.err != nil {
			m.errorMsg = msg.err.Error()
			m.status = "session preview error"
			return m, nil
		}
		m.sessionPreview = msg.detail
		m.chatMode = chatModeSessionPreview
		m.status = "session preview"
		m.errorMsg = ""
		return m, nil
	case sessionCreateDoneMsg:
		m.sessionLoading = false
		if msg.err != nil {
			m.errorMsg = msg.err.Error()
			m.status = "session create error"
			return m, nil
		}
		return m.switchSession(msg.detail.Key)
	case healthDoneMsg:
		m.selected.Status = msg.Status
		m.selected.Health = msg.Health
		m.selected.Error = msg.Error
		m.updateProject(m.selected)
		if msg.Status == StatusOnline {
			m.status = "gateway online"
			m.errorMsg = ""
		} else {
			m.status = string(msg.Status)
			m.errorMsg = msg.Error
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}

	if m.screen == screenProjects {
		return m.handleProjectKey(key, msg)
	}
	return m.handleChatKey(key, msg)
}

func (m model) handleProjectKey(key string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.prompt == promptAddProject {
		switch key {
		case "esc":
			m.prompt = promptNone
			m.status = "add cancelled"
			return m, nil
		case "enter":
			path := strings.TrimSpace(m.promptInput.Value())
			if path == "" {
				m.errorMsg = "project path is required"
				return m, nil
			}
			abs, err := normalizeProjectPath(path)
			if err != nil {
				m.errorMsg = err.Error()
				return m, nil
			}
			m.state = upsertProjectState(m.state, abs, defaultSessionKey, time.Now().UTC())
			if err := SaveState(m.state); err != nil {
				m.errorMsg = err.Error()
				return m, nil
			}
			m.prompt = promptNone
			m.scanning = true
			m.status = "project added; rescanning..."
			m.errorMsg = ""
			return m, scanProjectsCmd(m.state)
		}
		var cmd tea.Cmd
		m.promptInput, cmd = m.promptInput.Update(msg)
		return m, cmd
	}

	switch key {
	case "q":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.projects)-1 {
			m.cursor++
		}
	case "r":
		m.scanning = true
		m.status = "rescanning projects..."
		m.errorMsg = ""
		return m, scanProjectsCmd(m.state)
	case "a":
		m.prompt = promptAddProject
		m.promptInput.SetValue("")
		m.promptInput.Placeholder = "/path/to/project"
		m.promptInput.Prompt = "project path > "
		m.promptInput.Focus()
		m.status = "enter a project path"
	case "d":
		if len(m.projects) == 0 {
			return m, nil
		}
		p := m.projects[m.cursor]
		m.state = removeProjectState(m.state, p.Path)
		if err := SaveState(m.state); err != nil {
			m.errorMsg = err.Error()
			return m, nil
		}
		m.scanning = true
		m.status = "project removed from saved list; rescanning..."
		m.errorMsg = ""
		return m, scanProjectsCmd(m.state)
	case "enter":
		if len(m.projects) == 0 {
			return m, nil
		}
		p := m.projects[m.cursor]
		if p.Status != StatusOnline {
			m.status = fmt.Sprintf("gateway %s", p.Status)
			m.errorMsg = gatewayStartHint(p.Path)
			return m, nil
		}
		m.selected = p
		m.screen = screenChat
		m.prompt = promptNone
		m.chatInput.Focus()
		m.chatInput.SetValue("")
		m.state = upsertProjectState(m.state, p.Path, p.SessionKey, time.Now().UTC())
		_ = SaveState(m.state)
		m.status = "connected"
		m.errorMsg = ""
		return m, tea.Println(renderChatHeader(m.selected))
	}
	return m, nil
}

func (m model) handleChatKey(key string, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.prompt == promptNewSession {
		switch key {
		case "esc":
			m.prompt = promptNone
			m.promptInput.Blur()
			m.status = "new session cancelled"
			return m, nil
		case "enter":
			sessionKey := strings.TrimSpace(m.promptInput.Value())
			if sessionKey == "" {
				sessionKey = generatedSessionKey()
			}
			m.prompt = promptNone
			m.promptInput.Blur()
			m.sessionLoading = true
			m.status = "creating session..."
			m.errorMsg = ""
			return m, createSessionCmd(m.selected.Path, sessionKey)
		}
		var cmd tea.Cmd
		m.promptInput, cmd = m.promptInput.Update(msg)
		return m, cmd
	}

	if m.chatMode == chatModeSessions {
		return m.handleSessionListKey(key)
	}
	if m.chatMode == chatModeSessionPreview {
		return m.handleSessionPreviewKey(key)
	}

	switch key {
	case "esc":
		m.screen = screenProjects
		m.prompt = promptNone
		m.status = "back to projects"
		return m, nil
	case "ctrl+s":
		m.chatMode = chatModeSessions
		m.prompt = promptNone
		m.sessionLoading = true
		m.status = "loading sessions..."
		m.errorMsg = ""
		return m, sessionsCmd(m.selected.Path)
	case "ctrl+r":
		m.status = "checking gateway health..."
		m.errorMsg = ""
		return m, healthCmd(m.selected.Path)
	case "ctrl+l":
		m.messages = nil
		m.latestTools = nil
		m.status = "chat preview cleared (terminal scrollback remains)"
		return m, tea.ClearScreen
	case "enter":
		if m.chatting {
			return m, nil
		}
		message := strings.TrimSpace(m.chatInput.Value())
		if message == "" {
			return m, nil
		}
		userMsg := chatMessage{Role: "user", Content: message}
		m.messages = append(m.messages, userMsg)
		m.chatInput.SetValue("")
		m.chatting = true
		m.status = "sending message..."
		m.errorMsg = ""
		return m, tea.Sequence(
			tea.Println(renderTranscriptMessage(userMsg, nil)),
			chatStreamCmd(m.selected.Path, message, m.selected.SessionKey),
		)
	}

	var cmd tea.Cmd
	m.chatInput, cmd = m.chatInput.Update(msg)
	return m, cmd
}

func (m model) handleSessionListKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m.chatMode = chatModeInput
		m.prompt = promptNone
		m.chatInput.Focus()
		m.status = "back to chat"
		return m, nil
	case "up", "k":
		if m.sessionCursor > 0 {
			m.sessionCursor--
		}
	case "down", "j":
		if m.sessionCursor < len(m.sessions)-1 {
			m.sessionCursor++
		}
	case "r":
		m.sessionLoading = true
		m.status = "loading sessions..."
		m.errorMsg = ""
		return m, sessionsCmd(m.selected.Path)
	case "n":
		m.prompt = promptNewSession
		m.promptInput.SetValue("")
		m.promptInput.Placeholder = generatedSessionKey()
		m.promptInput.Prompt = "new session > "
		m.promptInput.Focus()
		m.chatInput.Blur()
		m.status = "enter a session key (empty = auto)"
		return m, nil
	case "enter":
		if len(m.sessions) == 0 {
			return m, nil
		}
		return m.switchSession(m.sessions[m.sessionCursor].Key)
	case "v", " ":
		if len(m.sessions) == 0 {
			return m, nil
		}
		m.sessionLoading = true
		m.status = "loading session preview..."
		m.errorMsg = ""
		return m, sessionPreviewCmd(m.selected.Path, m.sessions[m.sessionCursor].Key)
	}
	return m, nil
}

func (m model) handleSessionPreviewKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		m.chatMode = chatModeSessions
		m.status = "sessions"
		return m, nil
	case "enter":
		return m.switchSession(m.sessionPreview.Key)
	}
	return m, nil
}

func (m model) switchSession(sessionKey string) (model, tea.Cmd) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		m.errorMsg = "session key is required"
		return m, nil
	}
	oldSessionKey := m.selected.SessionKey
	m.selected.SessionKey = sessionKey
	m.updateProject(m.selected)
	m.state = upsertProjectState(m.state, m.selected.Path, sessionKey, time.Now().UTC())
	if err := SaveState(m.state); err != nil {
		m.errorMsg = err.Error()
		return m, nil
	}
	m.chatMode = chatModeInput
	m.prompt = promptNone
	m.chatInput.Focus()
	m.errorMsg = ""
	if oldSessionKey == sessionKey {
		m.status = "session key unchanged"
		return m, nil
	}
	m.status = "session key updated"
	sessionMsg := chatMessage{Role: "session-changed", Content: fmt.Sprintf("%s → %s", oldSessionKey, sessionKey)}
	m.messages = append(m.messages, sessionMsg)
	return m, tea.Println(renderTranscriptMessage(sessionMsg, nil))
}

func generatedSessionKey() string {
	return "session-" + time.Now().Format("20060102-150405")
}

func (m model) View() string {
	if m.screen == screenChat {
		return m.viewChat()
	}
	return m.viewProjects()
}

func (m model) viewProjects() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("clawlet tui — projects"))
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("↑/↓: select  Enter: connect  r: rescan  a: add  d: delete saved  q: quit"))
	b.WriteString("\n\n")

	if len(m.projects) == 0 {
		b.WriteString(dimStyle.Render("No projects found. Press a to add a project path."))
		b.WriteString("\n")
	}
	for i, p := range m.projects {
		cursor := " "
		if i == m.cursor {
			cursor = ">"
		}
		line := fmt.Sprintf("%s %s  %s", cursor, renderStatus(p.Status), p.Path)
		if p.Saved {
			line += dimStyle.Render("  saved")
		}
		b.WriteString(line)
		b.WriteString("\n")
		meta := fmt.Sprintf("    session=%s socket=%s", p.SessionKey, p.SocketPath)
		if p.Status == StatusOnline {
			meta += fmt.Sprintf(" pid=%d workspace=%s", p.Health.PID, p.Health.Workspace)
		} else if p.Error != "" {
			meta += " " + p.Error
		}
		b.WriteString(dimStyle.Render(meta))
		b.WriteString("\n")
	}

	if m.prompt == promptAddProject {
		b.WriteString("\n")
		b.WriteString(m.promptInput.View())
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(m.statusLine())
	if len(m.projects) > 0 && m.projects[m.cursor].Status != StatusOnline {
		b.WriteString("\n")
		b.WriteString(dimStyle.Render(gatewayStartHint(m.projects[m.cursor].Path)))
	}
	return b.String()
}

func (m model) viewChat() string {
	var b strings.Builder

	switch m.chatMode {
	case chatModeSessions:
		b.WriteString(m.viewSessions())
	case chatModeSessionPreview:
		b.WriteString(m.viewSessionPreview())
	case chatModeInput:
		fallthrough
	default:
		if m.prompt == promptNewSession {
			b.WriteString(m.promptInput.View())
			b.WriteString("\n")
		} else {
			b.WriteString("message > ")
			b.WriteString(m.chatInput.View())
			b.WriteString("\n")
		}

		if m.chatting {
			b.WriteString(warnStyle.Render("sending..."))
			b.WriteString("\n")
		}
		if len(m.latestTools) > 0 {
			b.WriteString(dimStyle.Render("tools_used: " + strings.Join(m.latestTools, ", ")))
			b.WriteString("\n")
		} else {
			b.WriteString(dimStyle.Render("tools_used: -"))
			b.WriteString("\n")
		}
		if status := m.statusLine(); status != "" {
			b.WriteString(status)
			b.WriteString("\n")
		}
		b.WriteString(helpStyle.Render("Enter: send  Esc: projects  Ctrl+S: sessions  Ctrl+R: health  Ctrl+L: clear  Ctrl+C: quit"))
		b.WriteString("\n")
		b.WriteString(m.chatFooter())
	}
	return b.String()
}

func (m model) viewSessions() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("clawlet tui — sessions"))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("project: %s\n", m.selected.Path))
	b.WriteString(helpStyle.Render("↑/↓: select  Enter: switch  n: new  v: preview  r: reload  Esc: chat"))
	b.WriteString("\n\n")

	if m.sessionLoading {
		b.WriteString(warnStyle.Render("loading..."))
		b.WriteString("\n")
	} else if len(m.sessions) == 0 {
		b.WriteString(dimStyle.Render("No sessions found. Press n to create a new session."))
		b.WriteString("\n")
	}
	for i, s := range m.sessions {
		cursor := " "
		if i == m.sessionCursor {
			cursor = ">"
		}
		current := ""
		if s.Key == m.selected.SessionKey {
			current = warnStyle.Render(" [current]")
		}
		updated := s.UpdatedAt.Format("2006-01-02 15:04")
		line := fmt.Sprintf("%s %s  updated: %s  msgs: %d%s", cursor, s.Key, updated, s.MessageCount, current)
		b.WriteString(line)
		b.WriteString("\n")
		if strings.TrimSpace(s.Preview) != "" {
			b.WriteString(dimStyle.Render("    " + s.Preview))
			b.WriteString("\n")
		}
	}

	if m.prompt == promptNewSession {
		b.WriteString("\n")
		b.WriteString(m.promptInput.View())
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(m.statusLine())
	b.WriteString("\n")
	b.WriteString(m.chatFooter())
	return b.String()
}

func (m model) viewSessionPreview() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("clawlet tui — session preview"))
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("Enter: switch  Esc: sessions"))
	b.WriteString("\n\n")

	if m.sessionLoading {
		b.WriteString(warnStyle.Render("loading..."))
		b.WriteString("\n")
	} else {
		b.WriteString(fmt.Sprintf("session: %s  msgs: %d\n", m.sessionPreview.Key, m.sessionPreview.MessageCount))
		b.WriteString("\n")
		for _, msg := range lastNMessages(m.sessionPreview.Messages, 10) {
			label := msg.Role
			switch msg.Role {
			case "user":
				label = userStyle.Render("user")
			case "assistant":
				label = agentStyle.Render("assistant")
			default:
				label = dimStyle.Render(msg.Role)
			}
			b.WriteString(label + ":\n")
			content := strings.TrimSpace(msg.Content)
			if content == "" {
				content = "(empty)"
			}
			for _, line := range strings.Split(content, "\n") {
				b.WriteString("  " + line + "\n")
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(m.statusLine())
	b.WriteString("\n")
	b.WriteString(m.chatFooter())
	return b.String()
}

func lastNMessages(msgs []SessionMessage, n int) []SessionMessage {
	if n <= 0 || len(msgs) <= n {
		return msgs
	}
	return msgs[len(msgs)-n:]
}

func (m model) chatFooter() string {
	health := string(m.selected.Status)
	if m.selected.Status == StatusOnline {
		health = fmt.Sprintf("online pid=%d", m.selected.Health.PID)
	}
	return dimStyle.Render(fmt.Sprintf("root: %s  session: %s  gateway: %s", m.selected.Path, m.selected.SessionKey, health))
}

func (m model) renderChatLog(maxLines int) string {
	if len(m.messages) == 0 {
		return dimStyle.Render("No messages yet.") + "\n"
	}
	width := m.width - 4
	if width < 40 {
		width = 80
	}
	var lines []string
	for _, msg := range m.messages {
		label := msg.Role
		switch msg.Role {
		case "user":
			label = userStyle.Render("user")
		case "assistant":
			label = agentStyle.Render("assistant")
		case "error":
			label = errorStyle.Render("error")
		case "session-changed":
			label = warnStyle.Render("session-changed")
		default:
			label = systemStyle.Render(msg.Role)
		}
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			content = "(empty)"
		}
		wrapped := lipgloss.NewStyle().Width(width).Render(content)
		parts := strings.Split(wrapped, "\n")
		lines = append(lines, label+":")
		for _, p := range parts {
			lines = append(lines, "  "+p)
		}
		lines = append(lines, "")
	}
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n") + "\n"
}

func renderChatHeader(p ProjectView) string {
	health := string(p.Status)
	if p.Status == StatusOnline {
		health = fmt.Sprintf("online pid=%d", p.Health.PID)
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("clawlet tui — chat"))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("project: %s\n", p.Path))
	b.WriteString(fmt.Sprintf("session: %s  gateway: %s", p.SessionKey, renderStatusText(p.Status, health)))
	return b.String()
}

func renderTranscriptMessage(msg chatMessage, tools []string) string {
	role := strings.TrimSpace(msg.Role)
	if role == "" {
		role = "system"
	}
	content := strings.TrimSpace(msg.Content)
	if content == "" {
		content = "(empty)"
	}

	label := "[" + role + "]"
	contentStyle := lipgloss.NewStyle()
	switch role {
	case "user":
		label = userStyle.Render(label)
	case "assistant":
		label = agentStyle.Render(label)
	case "error":
		label = errorStyle.Render(label)
		contentStyle = errorStyle
	case "session-changed":
		label = warnStyle.Render(label)
		contentStyle = warnStyle
	default:
		label = systemStyle.Render(label)
	}

	var b strings.Builder
	b.WriteString(label)
	b.WriteString("\n")
	for _, line := range strings.Split(content, "\n") {
		b.WriteString("  ")
		b.WriteString(contentStyle.Render(line))
		b.WriteString("\n")
	}
	if len(tools) > 0 {
		b.WriteString(dimStyle.Render("  tools_used: "))
		b.WriteString(dimStyle.Render(strings.Join(tools, ", ")))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderChatStreamEvent(ev ChatStreamEvent) string {
	switch ev.Type {
	case "tool_start":
		line := strings.TrimSpace(ev.Name)
		if strings.TrimSpace(ev.Args) != "" {
			line += " " + strings.TrimSpace(ev.Args)
		}
		return renderBlock("tool:stdin", toolStyle, line)
	case "tool_end":
		return renderToolEnd(ev)
	case "error":
		return renderTranscriptMessage(chatMessage{Role: "error", Content: ev.Error}, nil)
	default:
		return dimStyle.Render(ev.Type)
	}
}

func renderBlock(role string, style lipgloss.Style, content string) string {
	return renderStyledBlock(role, style, lipgloss.NewStyle(), content)
}

func renderStyledBlock(role string, labelStyle lipgloss.Style, contentStyle lipgloss.Style, content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		content = "(empty)"
	}
	var b strings.Builder
	b.WriteString(labelStyle.Render("[" + role + "]"))
	b.WriteString("\n")
	for _, line := range strings.Split(content, "\n") {
		b.WriteString("  ")
		b.WriteString(contentStyle.Render(line))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderToolEnd(ev ChatStreamEvent) string {
	status := strings.TrimSpace(ev.Name)
	if ev.DurationMS > 0 {
		status += fmt.Sprintf(" (%s)", time.Duration(ev.DurationMS)*time.Millisecond)
	}

	var blocks []string
	if ev.Error != "" {
		blocks = append(blocks, renderBlock("tool:status", toolStyle, status))
		blocks = append(blocks, renderStyledBlock("tool:stderr", errorStyle, errorStyle, "(error) "+ev.Error))
		return strings.Join(blocks, "\n") + "\n"
	}

	meta, stdout, stderr := splitToolOutput(ev.Output)
	if len(meta) > 0 {
		status += "\n" + strings.Join(meta, "\n")
	}
	blocks = append(blocks, renderBlock("tool:status", toolStyle, status))
	if len(stdout) > 0 {
		blocks = append(blocks, renderBlock("tool:stdout", toolStyle, strings.Join(stdout, "\n")))
	}
	if len(stderr) > 0 {
		blocks = append(blocks, renderStyledBlock("tool:stderr", errorStyle, errorStyle, strings.Join(stderr, "\n")))
	}
	return strings.Join(blocks, "\n") + "\n"
}

func splitToolOutput(output string) (meta []string, stdout []string, stderr []string) {
	output = strings.TrimRight(output, "\n")
	if strings.TrimSpace(output) == "" {
		return nil, nil, nil
	}

	section := "meta"
	sawStreamMarker := false
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		switch trimmed {
		case "stdout:":
			section = "stdout"
			sawStreamMarker = true
			continue
		case "stderr:":
			section = "stderr"
			sawStreamMarker = true
			continue
		}
		if strings.HasPrefix(trimmed, "error:") {
			stderr = append(stderr, line)
			sawStreamMarker = true
			continue
		}
		switch section {
		case "stdout":
			stdout = append(stdout, line)
		case "stderr":
			stderr = append(stderr, line)
		default:
			meta = append(meta, line)
		}
	}

	if !sawStreamMarker && !metaLooksLikeStatus(meta) {
		stdout = meta
		meta = nil
	}
	return meta, stdout, stderr
}

func metaLooksLikeStatus(lines []string) bool {
	if len(lines) == 0 {
		return false
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "exit=") {
			continue
		}
		return false
	}
	return true
}

func (m model) statusLine() string {
	status := m.status
	if m.scanning {
		status = "scanning..."
	}
	if m.errorMsg != "" {
		if status == "" {
			return errorStyle.Render(m.errorMsg)
		}
		return fmt.Sprintf("%s  %s", dimStyle.Render(status), errorStyle.Render(m.errorMsg))
	}
	if status == "" {
		return ""
	}
	return dimStyle.Render(status)
}

func (m *model) updateProject(p ProjectView) {
	for i := range m.projects {
		if samePath(m.projects[i].Path, p.Path) {
			m.projects[i] = p
			return
		}
	}
	m.projects = append(m.projects, p)
}

func scanProjectsCmd(st State) tea.Cmd {
	return func() tea.Msg {
		return scanDoneMsg{projects: ScanProjects(context.Background(), st)}
	}
}

func chatCmd(workspace string, message string, sessionKey string) tea.Cmd {
	return func() tea.Msg {
		resp, err := SendChat(context.Background(), workspace, message, sessionKey, 10*time.Minute)
		if err == nil {
			return chatDoneMsg{response: resp}
		}
		status := StatusError
		var gatewayErr *GatewayError
		if errors.As(err, &gatewayErr) {
			status = gatewayErr.Status
		}
		return chatDoneMsg{status: status, err: err}
	}
}

func chatStreamCmd(workspace string, message string, sessionKey string) tea.Cmd {
	return func() tea.Msg {
		return chatStreamStartedMsg{events: StreamChat(context.Background(), workspace, message, sessionKey, 10*time.Minute)}
	}
}

func readChatStreamCmd(events <-chan ChatStreamEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-events
		if !ok {
			return chatStreamClosedMsg{}
		}
		return chatStreamEventMsg{event: ev, events: events}
	}
}

func healthCmd(workspace string) tea.Cmd {
	return func() tea.Msg {
		return healthDoneMsg(CheckHealth(context.Background(), workspace, 800*time.Millisecond))
	}
}

func sessionsCmd(workspace string) tea.Cmd {
	return func() tea.Msg {
		resp, err := FetchSessions(context.Background(), workspace, 5*time.Second)
		if err == nil {
			return sessionsDoneMsg{sessions: resp.Sessions}
		}
		return sessionsDoneMsg{err: err}
	}
}

func sessionPreviewCmd(workspace string, key string) tea.Cmd {
	return func() tea.Msg {
		detail, err := FetchSession(context.Background(), workspace, key, 5*time.Second)
		if err == nil {
			return sessionPreviewDoneMsg{detail: detail}
		}
		return sessionPreviewDoneMsg{err: err}
	}
}

func createSessionCmd(workspace string, key string) tea.Cmd {
	return func() tea.Msg {
		detail, err := CreateSession(context.Background(), workspace, key, 5*time.Second)
		if err == nil {
			return sessionCreateDoneMsg{detail: detail}
		}
		return sessionCreateDoneMsg{err: err}
	}
}

func renderStatus(status GatewayStatus) string {
	return renderStatusText(status, string(status))
}

func renderStatusText(status GatewayStatus, text string) string {
	switch status {
	case StatusOnline:
		return successStyle.Render(text)
	case StatusOffline:
		return dimStyle.Render(text)
	case StatusStaleSocket, StatusTimeout:
		return warnStyle.Render(text)
	default:
		return errorStyle.Render(text)
	}
}

func gatewayStartHint(path string) string {
	return fmt.Sprintf("start gateway: clawlet gateway --dir %s", path)
}
