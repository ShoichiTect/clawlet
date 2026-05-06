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
	promptSessionKey
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

type healthDoneMsg GatewayResult

var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	helpStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	successStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	warnStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	userStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	agentStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	systemStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214"))
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
			systemMsg := chatMessage{Role: "system", Content: fmt.Sprintf("%s: %v", msg.status, msg.err)}
			m.messages = append(m.messages, systemMsg)
			m.errorMsg = msg.err.Error()
			return m, tea.Println(renderTranscriptMessage(systemMsg, nil))
		}
		m.latestTools = msg.response.ToolsUsed
		assistantMsg := chatMessage{Role: "assistant", Content: msg.response.Content}
		m.messages = append(m.messages, assistantMsg)
		m.status = "response received"
		m.errorMsg = ""
		return m, tea.Println(renderTranscriptMessage(assistantMsg, msg.response.ToolsUsed))
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
	if m.prompt == promptSessionKey {
		switch key {
		case "esc":
			m.prompt = promptNone
			m.chatInput.Focus()
			m.status = "session change cancelled"
			return m, nil
		case "enter":
			sessionKey := strings.TrimSpace(m.promptInput.Value())
			if sessionKey == "" {
				m.errorMsg = "session key is required"
				return m, nil
			}
			m.selected.SessionKey = sessionKey
			m.updateProject(m.selected)
			m.state = upsertProjectState(m.state, m.selected.Path, sessionKey, time.Now().UTC())
			if err := SaveState(m.state); err != nil {
				m.errorMsg = err.Error()
				return m, nil
			}
			m.prompt = promptNone
			m.chatInput.Focus()
			m.status = "session key updated"
			m.errorMsg = ""
			return m, nil
		}
		var cmd tea.Cmd
		m.promptInput, cmd = m.promptInput.Update(msg)
		return m, cmd
	}

	switch key {
	case "esc":
		m.screen = screenProjects
		m.prompt = promptNone
		m.status = "back to projects"
		return m, nil
	case "ctrl+s":
		m.prompt = promptSessionKey
		m.promptInput.SetValue(m.selected.SessionKey)
		m.promptInput.Placeholder = defaultSessionKey
		m.promptInput.Prompt = "session key > "
		m.promptInput.Focus()
		m.chatInput.Blur()
		m.status = "edit session key"
		return m, nil
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
			chatCmd(m.selected.Path, message, m.selected.SessionKey),
		)
	}

	var cmd tea.Cmd
	m.chatInput, cmd = m.chatInput.Update(msg)
	return m, cmd
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
	if m.prompt == promptSessionKey {
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
	b.WriteString(helpStyle.Render("Enter: send  Esc: projects  Ctrl+S: session  Ctrl+R: health  Ctrl+L: clear  Ctrl+C: quit"))
	b.WriteString("\n")
	b.WriteString(m.chatFooter())
	return b.String()
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
	switch role {
	case "user":
		label = userStyle.Render(label)
	case "assistant":
		label = agentStyle.Render(label)
	default:
		label = systemStyle.Render(label)
	}

	var b strings.Builder
	b.WriteString(label)
	b.WriteString("\n")
	for _, line := range strings.Split(content, "\n") {
		b.WriteString("  ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	if len(tools) > 0 {
		b.WriteString(dimStyle.Render("  tools_used: "))
		b.WriteString(dimStyle.Render(strings.Join(tools, ", ")))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
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

func healthCmd(workspace string) tea.Cmd {
	return func() tea.Msg {
		return healthDoneMsg(CheckHealth(context.Background(), workspace, 800*time.Millisecond))
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
