package tui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type GatewayStatus string

const (
	StatusOnline      GatewayStatus = "online"
	StatusOffline     GatewayStatus = "offline"
	StatusStaleSocket GatewayStatus = "stale socket"
	StatusTimeout     GatewayStatus = "timeout"
	StatusError       GatewayStatus = "error"
)

type HealthResponse struct {
	Status    string `json:"status"`
	Workspace string `json:"workspace"`
	PID       int    `json:"pid"`
}

type ChatResponse struct {
	Content   string   `json:"content"`
	ToolsUsed []string `json:"tools_used"`
}

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

type SessionMessage struct {
	Role      string   `json:"role"`
	Content   string   `json:"content"`
	Timestamp string   `json:"timestamp,omitempty"`
	ToolsUsed []string `json:"tools_used,omitempty"`
}

type SessionSummary struct {
	Key          string    `json:"key"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	MessageCount int       `json:"message_count"`
	Preview      string    `json:"preview,omitempty"`
}

type SessionListResponse struct {
	Sessions []SessionSummary `json:"sessions"`
}

type SessionDetail struct {
	Key          string           `json:"key"`
	CreatedAt    time.Time        `json:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
	MessageCount int              `json:"message_count"`
	Messages     []SessionMessage `json:"messages"`
}

type GatewayResult struct {
	Status GatewayStatus
	Health HealthResponse
	Error  string
}

type GatewayError struct {
	Status GatewayStatus
	Err    error
}

func (e *GatewayError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func SocketPath(workspace string) string {
	return filepath.Join(workspace, ".clawlet", "gateway.sock")
}

func CheckHealth(ctx context.Context, workspace string, timeout time.Duration) GatewayResult {
	sockPath := SocketPath(workspace)
	if _, err := os.Stat(sockPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return GatewayResult{Status: StatusOffline, Error: "socket not found"}
		}
		return GatewayResult{Status: StatusError, Error: err.Error()}
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client := unixHTTPClient(sockPath, timeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix/api/health", nil)
	if err != nil {
		return GatewayResult{Status: StatusError, Error: err.Error()}
	}
	resp, err := client.Do(req)
	if err != nil {
		status := classifyRequestError(ctx, err)
		return GatewayResult{Status: status, Error: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		return GatewayResult{Status: StatusError, Error: msg}
	}
	var health HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return GatewayResult{Status: StatusError, Error: "decode health: " + err.Error()}
	}
	if strings.ToLower(strings.TrimSpace(health.Status)) != "ok" {
		return GatewayResult{Status: StatusError, Health: health, Error: "gateway status is " + health.Status}
	}
	return GatewayResult{Status: StatusOnline, Health: health}
}

func SendChat(ctx context.Context, workspace string, message string, sessionKey string, timeout time.Duration) (ChatResponse, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return ChatResponse{}, &GatewayError{Status: StatusError, Err: fmt.Errorf("message is required")}
	}
	if strings.TrimSpace(sessionKey) == "" {
		sessionKey = defaultSessionKey
	}

	sockPath := SocketPath(workspace)
	if _, err := os.Stat(sockPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ChatResponse{}, &GatewayError{Status: StatusOffline, Err: fmt.Errorf("socket not found")}
		}
		return ChatResponse{}, &GatewayError{Status: StatusError, Err: err}
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	body, err := json.Marshal(map[string]string{
		"message":     message,
		"session_key": sessionKey,
	})
	if err != nil {
		return ChatResponse{}, &GatewayError{Status: StatusError, Err: err}
	}

	client := unixHTTPClient(sockPath, timeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix/api/chat", bytes.NewReader(body))
	if err != nil {
		return ChatResponse{}, &GatewayError{Status: StatusError, Err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return ChatResponse{}, &GatewayError{Status: classifyRequestError(ctx, err), Err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		msg := gatewayErrorMessage(body, resp.Status)
		return ChatResponse{}, &GatewayError{Status: StatusError, Err: fmt.Errorf("gateway API error: %s", msg)}
	}
	var out ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return ChatResponse{}, &GatewayError{Status: StatusError, Err: fmt.Errorf("decode chat response: %w", err)}
	}
	return out, nil
}

func FetchSessions(ctx context.Context, workspace string, timeout time.Duration) (SessionListResponse, error) {
	var out SessionListResponse
	if err := doGatewayJSON(ctx, workspace, timeout, http.MethodGet, "http://unix/api/sessions", nil, &out); err != nil {
		return SessionListResponse{}, err
	}
	return out, nil
}

func FetchSession(ctx context.Context, workspace string, key string, timeout time.Duration) (SessionDetail, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return SessionDetail{}, &GatewayError{Status: StatusError, Err: fmt.Errorf("session key is required")}
	}
	var out SessionDetail
	endpoint := "http://unix/api/session?key=" + url.QueryEscape(key)
	if err := doGatewayJSON(ctx, workspace, timeout, http.MethodGet, endpoint, nil, &out); err != nil {
		return SessionDetail{}, err
	}
	return out, nil
}

func CreateSession(ctx context.Context, workspace string, key string, timeout time.Duration) (SessionDetail, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return SessionDetail{}, &GatewayError{Status: StatusError, Err: fmt.Errorf("session key is required")}
	}
	body, err := json.Marshal(map[string]string{"key": key})
	if err != nil {
		return SessionDetail{}, &GatewayError{Status: StatusError, Err: err}
	}
	var out SessionDetail
	if err := doGatewayJSON(ctx, workspace, timeout, http.MethodPost, "http://unix/api/sessions", body, &out); err != nil {
		return SessionDetail{}, err
	}
	return out, nil
}

func doGatewayJSON(ctx context.Context, workspace string, timeout time.Duration, method string, endpoint string, body []byte, out any) error {
	sockPath := SocketPath(workspace)
	if _, err := os.Stat(sockPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &GatewayError{Status: StatusOffline, Err: fmt.Errorf("socket not found")}
		}
		return &GatewayError{Status: StatusError, Err: err}
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	client := unixHTTPClient(sockPath, timeout)
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return &GatewayError{Status: StatusError, Err: err}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return &GatewayError{Status: classifyRequestError(ctx, err), Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		return &GatewayError{Status: StatusError, Err: fmt.Errorf("gateway API error: %s", gatewayErrorMessage(b, resp.Status))}
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return &GatewayError{Status: StatusError, Err: fmt.Errorf("decode gateway response: %w", err)}
	}
	return nil
}

func StreamChat(ctx context.Context, workspace string, message string, sessionKey string, timeout time.Duration) <-chan ChatStreamEvent {
	events := make(chan ChatStreamEvent, 16)
	go func() {
		defer close(events)
		if err := streamChat(ctx, workspace, message, sessionKey, timeout, func(ev ChatStreamEvent) {
			events <- ev
		}); err != nil {
			events <- ChatStreamEvent{Type: "error", Error: err.Error()}
		}
	}()
	return events
}

func streamChat(ctx context.Context, workspace string, message string, sessionKey string, timeout time.Duration, emit func(ChatStreamEvent)) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return &GatewayError{Status: StatusError, Err: fmt.Errorf("message is required")}
	}
	if strings.TrimSpace(sessionKey) == "" {
		sessionKey = defaultSessionKey
	}

	sockPath := SocketPath(workspace)
	if _, err := os.Stat(sockPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &GatewayError{Status: StatusOffline, Err: fmt.Errorf("socket not found")}
		}
		return &GatewayError{Status: StatusError, Err: err}
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	body, err := json.Marshal(map[string]string{
		"message":     message,
		"session_key": sessionKey,
	})
	if err != nil {
		return &GatewayError{Status: StatusError, Err: err}
	}

	client := unixHTTPClient(sockPath, timeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://unix/api/chat/stream", bytes.NewReader(body))
	if err != nil {
		return &GatewayError{Status: StatusError, Err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	resp, err := client.Do(req)
	if err != nil {
		return &GatewayError{Status: classifyRequestError(ctx, err), Err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
		msg := gatewayErrorMessage(body, resp.Status)
		return &GatewayError{Status: StatusError, Err: fmt.Errorf("gateway stream API error: %s", msg)}
	}
	return consumeSSE(resp.Body, emit)
}

func consumeSSE(r io.Reader, emit func(ChatStreamEvent)) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 2<<20)

	var eventName string
	var dataLines []string
	dispatch := func() {
		if eventName == "" && len(dataLines) == 0 {
			return
		}
		data := strings.Join(dataLines, "\n")
		ev := ChatStreamEvent{Type: eventName}
		if strings.TrimSpace(data) != "" {
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				ev = ChatStreamEvent{Type: "error", Error: "decode stream event: " + err.Error()}
			}
		}
		if ev.Type == "" {
			ev.Type = eventName
		}
		emit(ev)
		eventName = ""
		dataLines = nil
	}

	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			dispatch()
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, ok := strings.Cut(line, ":")
		if ok {
			value = strings.TrimPrefix(value, " ")
		} else {
			value = ""
		}
		switch field {
		case "event":
			eventName = value
		case "data":
			dataLines = append(dataLines, value)
		}
	}
	dispatch()
	return scanner.Err()
}

func gatewayErrorMessage(body []byte, fallback string) string {
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && strings.TrimSpace(payload.Error) != "" {
		return strings.TrimSpace(payload.Error)
	}
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = fallback
	}
	return msg
}

func unixHTTPClient(sockPath string, timeout time.Duration) *http.Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", sockPath)
		},
	}
	return &http.Client{Transport: transport, Timeout: timeout}
}

func classifyRequestError(ctx context.Context, err error) GatewayStatus {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) || os.IsTimeout(err) {
		return StatusTimeout
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded") {
		return StatusTimeout
	}
	if strings.Contains(msg, "connect: connection refused") || strings.Contains(msg, "no such file") || strings.Contains(msg, "broken pipe") || strings.Contains(msg, "connection reset") || strings.Contains(msg, "eof") {
		return StatusStaleSocket
	}
	return StatusStaleSocket
}
