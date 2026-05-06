package memory

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Store struct {
	Workspace string
	Dir       string
	LongTerm  string
	History   string
}

func New(workspace string) *Store {
	dir := filepath.Join(workspace, ".clawlet", "memory")
	return &Store{
		Workspace: workspace,
		Dir:       dir,
		LongTerm:  filepath.Join(dir, "MEMORY.md"),
		History:   filepath.Join(dir, "HISTORY.md"),
	}
}

func TodayDate() string {
	return time.Now().Format("2006-01-02")
}

func (s *Store) TodayPath() string {
	return filepath.Join(s.Dir, TodayDate()+".md")
}

func (s *Store) EnsureInitialized() error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(s.LongTerm); err != nil {
		_ = os.WriteFile(s.LongTerm, []byte("# Long-term Memory\n\n"), 0o644)
	}
	return nil
}

func (s *Store) ReadLongTerm() string {
	_ = s.EnsureInitialized()
	b, err := os.ReadFile(s.LongTerm)
	if err != nil {
		return ""
	}
	return string(b)
}

func (s *Store) WriteLongTerm(content string) error {
	if err := s.EnsureInitialized(); err != nil {
		return err
	}
	return os.WriteFile(s.LongTerm, []byte(content), 0o644)
}

func (s *Store) ReadToday() string {
	_ = s.EnsureInitialized()
	p := s.TodayPath()
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

func (s *Store) GetContext() string {
	longTerm := strings.TrimSpace(s.ReadLongTerm())
	today := strings.TrimSpace(s.ReadToday())

	var parts []string
	if longTerm != "" {
		parts = append(parts, "## Long-term Memory\n"+truncate(longTerm, 64<<10))
	}
	if today != "" {
		parts = append(parts, "## Today's Notes\n"+truncate(today, 64<<10))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n\n")
}

func (s *Store) AppendHistory(entry string) error {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return nil
	}
	if err := s.EnsureInitialized(); err != nil {
		return err
	}
	if _, err := os.Stat(s.History); err != nil {
		if os.IsNotExist(err) {
			if werr := os.WriteFile(s.History, []byte("# Session History\n\n"), 0o644); werr != nil {
				return werr
			}
		} else {
			return err
		}
	}
	f, err := os.OpenFile(s.History, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.WriteString(entry + "\n\n"); err != nil {
		return err
	}
	return nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n\n(truncated)"
}
