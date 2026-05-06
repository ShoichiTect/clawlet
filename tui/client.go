package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
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
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		return ChatResponse{}, &GatewayError{Status: StatusError, Err: fmt.Errorf("gateway API error: %s", msg)}
	}
	var out ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return ChatResponse{}, &GatewayError{Status: StatusError, Err: fmt.Errorf("decode chat response: %w", err)}
	}
	return out, nil
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
