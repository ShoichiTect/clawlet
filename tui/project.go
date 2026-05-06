package tui

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/mosaxiv/clawlet/paths"
)

type ProjectView struct {
	Path       string
	SessionKey string
	SocketPath string
	Status     GatewayStatus
	Health     HealthResponse
	Error      string
	Saved      bool
}

func DiscoverProjects(st State) []ProjectView {
	seen := map[string]*ProjectView{}
	order := []string{}
	add := func(path string, sessionKey string, saved bool) {
		abs, err := normalizeProjectPath(path)
		if err != nil || abs == "" {
			return
		}
		if sessionKey == "" {
			sessionKey = sessionKeyForPath(st, abs)
		}
		if existing, ok := seen[abs]; ok {
			if saved {
				existing.Saved = true
			}
			if existing.SessionKey == "" || existing.SessionKey == defaultSessionKey {
				existing.SessionKey = sessionKey
			}
			return
		}
		seen[abs] = &ProjectView{
			Path:       abs,
			SessionKey: sessionKey,
			SocketPath: SocketPath(abs),
			Status:     StatusOffline,
			Saved:      saved,
		}
		order = append(order, abs)
	}

	for _, p := range st.Projects {
		add(p.Path, p.SessionKey, true)
	}
	if cwd, err := os.Getwd(); err == nil {
		add(cwd, sessionKeyForPath(st, cwd), false)
	}
	if ws, err := filepath.Abs(paths.WorkspaceDir()); err == nil {
		add(ws, sessionKeyForPath(st, ws), false)
	}

	out := make([]ProjectView, 0, len(order))
	for _, path := range order {
		out = append(out, *seen[path])
	}
	return out
}

func ScanProjects(ctx context.Context, st State) []ProjectView {
	projects := DiscoverProjects(st)
	for i := range projects {
		result := CheckHealth(ctx, projects[i].Path, 800*time.Millisecond)
		projects[i].Status = result.Status
		projects[i].Health = result.Health
		projects[i].Error = result.Error
	}
	sort.SliceStable(projects, func(i, j int) bool {
		if projects[i].Status == StatusOnline && projects[j].Status != StatusOnline {
			return true
		}
		if projects[i].Status != StatusOnline && projects[j].Status == StatusOnline {
			return false
		}
		if projects[i].Saved != projects[j].Saved {
			return projects[i].Saved
		}
		return projects[i].Path < projects[j].Path
	})
	return projects
}
