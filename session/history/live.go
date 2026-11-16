package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Live is a running Claude Code process as it records itself under
// ~/.claude/sessions/<pid>.json: the conversation it is in now, which /clear replaces with a
// new one, and the name set with /rename. Files of processes that exited can remain.
type Live struct {
	PID int `json:"pid"`
	// ProcStart tells a process from a later one that got the same pid.
	ProcStart string `json:"procStart"`
	// SessionID is the conversation the process is in now.
	SessionID string `json:"sessionId"`
	// CWD is the directory the process runs in, as written (a Windows path for claude.exe).
	CWD string `json:"cwd"`
	// StartedAt is when the process started, in milliseconds since the epoch.
	StartedAt  int64  `json:"startedAt"`
	Name       string `json:"name"`
	NameSource string `json:"nameSource"`
}

// Started is when the process started.
func (l Live) Started() time.Time {
	return time.UnixMilli(l.StartedAt)
}

// UserName is the name given with /rename, or "" when the session has none.
func (l Live) UserName() string {
	if l.NameSource != "user" {
		return ""
	}
	return strings.TrimSpace(l.Name)
}

// LiveSessions lists the Claude processes in Claude Code's session registry. It returns
// nothing when Claude keeps no registry (older versions).
func LiveSessions() ([]Live, error) {
	dir, err := configDir()
	if err != nil {
		return nil, err
	}
	dir = filepath.Join(dir, "sessions")
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var live []Live
	for _, f := range files {
		if f.IsDir() || filepath.Ext(f.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			continue // removed as the process exited
		}
		var l Live
		if json.Unmarshal(data, &l) != nil || l.SessionID == "" {
			continue
		}
		live = append(live, l)
	}
	return live, nil
}
