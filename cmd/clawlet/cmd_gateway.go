package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/mosaxiv/clawlet/agent"
	"github.com/mosaxiv/clawlet/cron"
	"github.com/mosaxiv/clawlet/heartbeat"
	"github.com/urfave/cli/v3"
)

func cmdGateway() *cli.Command {
	return &cli.Command{
		Name:  "gateway",
		Usage: "run the long-lived agent gateway (Unix socket HTTP + cron + heartbeat)",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "dir", Usage: "project directory (default: ~/.clawlet/workspace)"},
			&cli.IntFlag{Name: "max-iters", Value: 20, Usage: "max tool-call iterations"},
			&cli.BoolFlag{Name: "verbose", Aliases: []string{"v"}, Usage: "verbose"},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, _, err := loadConfig()
			if err != nil {
				return err
			}

			dirFlag := strings.TrimSpace(cmd.String("dir"))
			wsAbs, sessionsDir, err := resolveDir(dirFlag)
			if err != nil {
				return err
			}
			defaultSessionKey := "gateway:default"
			if dirFlag != "" {
				defaultSessionKey = "default"
			}
			if err := os.MkdirAll(filepath.Join(wsAbs, ".clawlet"), 0o700); err != nil {
				return err
			}
			if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
				return err
			}

			ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
			defer stop()

			var loop *agent.Loop
			cronStore := resolveCronStorePath(wsAbs, cfg.Cron.StorePath)
			var cronSvc *cron.Service
			if cfg.Cron.EnabledValue() {
				cronSvc = cron.NewService(cronStore, func(ctx context.Context, job cron.Job) (string, error) {
					if job.Payload.Kind != "" && job.Payload.Kind != "agent_turn" {
						return "", nil
					}
					if loop == nil {
						return "", fmt.Errorf("gateway loop is not ready")
					}
					sessionKey := strings.TrimSpace(job.Payload.SessionKey)
					if sessionKey == "" {
						sessionKey = defaultSessionKey
					}
					return loop.ProcessDirect(ctx, job.Payload.Message, sessionKey, "cron", job.ID)
				})
			}

			loop, err = agent.NewLoop(agent.LoopOptions{
				Config:       cfg,
				WorkspaceDir: wsAbs,
				SessionDir:   sessionsDir,
				Model:        cfg.LLM.Model,
				MaxIters:     cmd.Int("max-iters"),
				Cron:         cronSvc,
				Verbose:      cmd.Bool("verbose"),
			})
			if err != nil {
				return err
			}

			if cronSvc != nil {
				if err := cronSvc.Start(ctx); err != nil {
					return err
				}
			}

			hb := heartbeat.New(wsAbs, heartbeat.Options{
				Enabled:     cfg.Heartbeat.EnabledValue(),
				IntervalSec: cfg.Heartbeat.IntervalSec,
				OnHeartbeat: func(ctx context.Context, prompt string) (string, error) {
					return loop.ProcessDirect(ctx, prompt, "heartbeat", "heartbeat", "default")
				},
			})
			hb.Start(ctx)
			defer hb.Stop()
			if cronSvc != nil {
				defer cronSvc.Stop()
			}

			sockPath := filepath.Join(wsAbs, ".clawlet", "gateway.sock")
			if err := os.Remove(sockPath); err != nil && !os.IsNotExist(err) {
				return err
			}
			ln, err := net.Listen("unix", sockPath)
			if err != nil {
				return err
			}
			defer func() { _ = os.Remove(sockPath) }()

			mux := http.NewServeMux()
			mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
				writeJSON(w, http.StatusOK, map[string]any{
					"status":    "ok",
					"workspace": wsAbs,
					"pid":       os.Getpid(),
				})
			})
			mux.HandleFunc("GET /api/sessions", func(w http.ResponseWriter, r *http.Request) {
				sessions, err := loop.ListSessions()
				if err != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
			})
			mux.HandleFunc("POST /api/sessions", func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Key string `json:"key"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON: " + err.Error()})
					return
				}
				detail, err := loop.CreateSession(req.Key)
				if err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
					return
				}
				writeJSON(w, http.StatusOK, detail)
			})
			mux.HandleFunc("GET /api/session", func(w http.ResponseWriter, r *http.Request) {
				key := strings.TrimSpace(r.URL.Query().Get("key"))
				if key == "" {
					writeJSON(w, http.StatusBadRequest, map[string]any{"error": "session key is required"})
					return
				}
				detail, err := loop.GetSession(key)
				if err != nil {
					writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
					return
				}
				writeJSON(w, http.StatusOK, detail)
			})
			mux.HandleFunc("POST /api/chat", func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Message    string `json:"message"`
					SessionKey string `json:"session_key"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON: " + err.Error()})
					return
				}
				if strings.TrimSpace(req.Message) == "" {
					writeJSON(w, http.StatusBadRequest, map[string]any{"error": "message is required"})
					return
				}
				sessionKey := strings.TrimSpace(req.SessionKey)
				if sessionKey == "" {
					sessionKey = defaultSessionKey
				}
				turnCtx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
				defer cancel()
				out, err := loop.ProcessTurn(turnCtx, req.Message, sessionKey, "tui", "default")
				if err != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{
					"content":    out.Final,
					"tools_used": out.ToolsUsed,
				})
			})
			mux.HandleFunc("POST /api/chat/stream", func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Message    string `json:"message"`
					SessionKey string `json:"session_key"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON: " + err.Error()})
					return
				}
				if strings.TrimSpace(req.Message) == "" {
					writeJSON(w, http.StatusBadRequest, map[string]any{"error": "message is required"})
					return
				}
				flusher, ok := w.(http.Flusher)
				if !ok {
					writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "streaming is not supported"})
					return
				}

				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("Connection", "keep-alive")
				w.Header().Set("X-Accel-Buffering", "no")
				w.WriteHeader(http.StatusOK)
				flusher.Flush()

				sessionKey := strings.TrimSpace(req.SessionKey)
				if sessionKey == "" {
					sessionKey = defaultSessionKey
				}
				writeEvent := func(event string, v map[string]any) {
					if _, ok := v["type"]; !ok {
						v["type"] = event
					}
					_ = writeSSE(w, event, v)
					flusher.Flush()
				}
				observer := func(ev agent.ToolEvent) {
					switch ev.Phase {
					case agent.ToolStart:
						writeEvent("tool_start", map[string]any{
							"name": ev.Name,
							"args": ev.Args,
						})
					case agent.ToolEnd:
						writeEvent("tool_end", map[string]any{
							"name":        ev.Name,
							"output":      ev.Output,
							"error":       ev.Error,
							"duration_ms": ev.Duration.Milliseconds(),
						})
					}
				}

				turnCtx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
				defer cancel()
				out, err := loop.ProcessTurnWithObserver(turnCtx, req.Message, sessionKey, "tui", "default", observer)
				if err != nil {
					writeEvent("error", map[string]any{"error": err.Error()})
					writeEvent("done", map[string]any{})
					return
				}
				writeEvent("assistant_final", map[string]any{
					"content":    out.Final,
					"tools_used": out.ToolsUsed,
				})
				writeEvent("done", map[string]any{})
			})

			srv := &http.Server{Handler: mux}
			serveErr := make(chan error, 1)
			go func() {
				serveErr <- srv.Serve(ln)
			}()

			fmt.Printf("gateway running\n- workspace: %s\n- sessions: %s\n- socket: %s\n", wsAbs, sessionsDir, sockPath)
			fmt.Println("stop: Ctrl+C")

			select {
			case <-ctx.Done():
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = srv.Shutdown(shutdownCtx)
				if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
					return err
				}
				return nil
			case err := <-serveErr:
				if errors.Is(err, http.ErrServerClosed) {
					return nil
				}
				return err
			}
		},
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeSSE(w http.ResponseWriter, event string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
		return err
	}
	return nil
}
