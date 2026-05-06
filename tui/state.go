package tui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mosaxiv/clawlet/paths"
)

const defaultSessionKey = "default"

type State struct {
	Projects []ProjectState `json:"projects"`
}

type ProjectState struct {
	Path         string    `json:"path"`
	SessionKey   string    `json:"session_key"`
	LastOpenedAt time.Time `json:"last_opened_at"`
}

func StatePath() (string, error) {
	cfgDir, err := paths.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfgDir, "tui", "projects.json"), nil
}

func LoadState() (State, error) {
	path, err := StatePath()
	if err != nil {
		return State{}, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{}, nil
		}
		return State{}, err
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return State{}, err
	}
	return normalizeState(st), nil
}

func SaveState(st State) error {
	path, err := StatePath()
	if err != nil {
		return err
	}
	st = normalizeState(st)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o600)
}

func normalizeState(st State) State {
	seen := map[string]struct{}{}
	out := make([]ProjectState, 0, len(st.Projects))
	for _, p := range st.Projects {
		abs, err := normalizeProjectPath(p.Path)
		if err != nil || abs == "" {
			continue
		}
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		p.Path = abs
		if strings.TrimSpace(p.SessionKey) == "" {
			p.SessionKey = defaultSessionKey
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ai := out[i].LastOpenedAt
		aj := out[j].LastOpenedAt
		if ai.IsZero() && aj.IsZero() {
			return out[i].Path < out[j].Path
		}
		if ai.IsZero() {
			return false
		}
		if aj.IsZero() {
			return true
		}
		return ai.After(aj)
	})
	return State{Projects: out}
}

func upsertProjectState(st State, path string, sessionKey string, openedAt time.Time) State {
	abs, err := normalizeProjectPath(path)
	if err != nil || abs == "" {
		return normalizeState(st)
	}
	if strings.TrimSpace(sessionKey) == "" {
		sessionKey = defaultSessionKey
	}
	for i := range st.Projects {
		if samePath(st.Projects[i].Path, abs) {
			st.Projects[i].Path = abs
			st.Projects[i].SessionKey = sessionKey
			if !openedAt.IsZero() {
				st.Projects[i].LastOpenedAt = openedAt
			}
			return normalizeState(st)
		}
	}
	st.Projects = append(st.Projects, ProjectState{Path: abs, SessionKey: sessionKey, LastOpenedAt: openedAt})
	return normalizeState(st)
}

func removeProjectState(st State, path string) State {
	abs, err := normalizeProjectPath(path)
	if err != nil || abs == "" {
		return normalizeState(st)
	}
	out := st.Projects[:0]
	for _, p := range st.Projects {
		if samePath(p.Path, abs) {
			continue
		}
		out = append(out, p)
	}
	st.Projects = out
	return normalizeState(st)
}

func sessionKeyForPath(st State, path string) string {
	abs, err := normalizeProjectPath(path)
	if err != nil {
		return defaultSessionKey
	}
	for _, p := range st.Projects {
		if samePath(p.Path, abs) && strings.TrimSpace(p.SessionKey) != "" {
			return p.SessionKey
		}
	}
	return defaultSessionKey
}

func normalizeProjectPath(path string) (string, error) {
	p := strings.TrimSpace(path)
	if p == "" {
		return "", nil
	}
	if strings.HasPrefix(p, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if p == "~" {
			p = home
		} else if strings.HasPrefix(p, "~/") {
			p = filepath.Join(home, p[2:])
		}
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func samePath(a, b string) bool {
	aa, errA := normalizeProjectPath(a)
	bb, errB := normalizeProjectPath(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return aa == bb
}
