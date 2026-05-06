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
