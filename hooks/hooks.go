// Package hooks records Claude Code hook events so the UI knows when a session is
// working, blocked on a permission prompt, or waiting for input, without guessing from
// the screen. Claude runs "<cs> hook" on each event; the command writes one small JSON
// file per tmux pane under <config dir>/status, which the UI reads on every tick.
package hooks

import (
	"bytes"
	"claude-squad/config"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// SettingsFileName is the generated Claude settings file passed with --settings.
	SettingsFileName = "hooks.json"
	statusDirName    = "status"
)

// Events are the Claude Code hook events cs subscribes to.
var Events = []string{
	"UserPromptSubmit",
	"PreToolUse",
	"PostToolUse",
	"PermissionRequest",
	"Notification",
	"Stop",
	"SessionEnd",
}

// Status is the last hook event recorded for a tmux pane.
type Status struct {
	Pane      string    `json:"pane"`
	Event     string    `json:"event"`
	Type      string    `json:"type,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
	CWD       string    `json:"cwd,omitempty"`
	At        time.Time `json:"at"`
}

// State is what a Status says about the session.
type State int

const (
	// StateUnknown: the event carries no state information.
	StateUnknown State = iota
	// StateWorking: the agent is processing a prompt or running tools.
	StateWorking
	// StatePermission: the agent is blocked on a permission prompt.
	StatePermission
	// StateQuestion: the agent asked the user something.
	StateQuestion
	// StateDone: the agent finished its turn and waits for the next prompt.
	StateDone
	// StateEnded: the agent exited.
	StateEnded
)

// State maps the recorded event to a session state.
func (s Status) State() State {
	switch s.Event {
	case "UserPromptSubmit", "PreToolUse", "PostToolUse":
		return StateWorking
	case "PermissionRequest":
		return StatePermission
	case "Notification":
		switch s.Type {
		case "permission_prompt":
			return StatePermission
		case "idle_prompt":
			return StateDone
		case "elicitation_dialog", "elicitation_url_dialog", "agent_needs_input":
			return StateQuestion
		}
		return StateUnknown
	case "Stop":
		return StateDone
	case "SessionEnd":
		return StateEnded
	}
	return StateUnknown
}

// payload is the subset of the hook stdin JSON that is kept.
type payload struct {
	SessionID        string `json:"session_id"`
	CWD              string `json:"cwd"`
	HookEventName    string `json:"hook_event_name"`
	NotificationType string `json:"notification_type"`
}

// StatusDir returns the directory holding one status file per tmux pane.
func StatusDir() (string, error) {
	dir, err := config.GetConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, statusDirName), nil
}

// fileName maps a pane id such as "%3" to "pane-3.json".
func fileName(pane string) string {
	return "pane-" + strings.TrimPrefix(pane, "%") + ".json"
}

// Record parses a hook payload and stores it as the pane's status. An empty pane means
// the agent does not run inside tmux, so there is nothing to attach the event to and the
// call is a no-op.
func Record(statusDir string, r io.Reader, pane string, now time.Time) error {
	if pane == "" {
		return nil
	}
	var p payload
	if err := json.NewDecoder(r).Decode(&p); err != nil {
		return fmt.Errorf("invalid hook payload: %w", err)
	}
	st := Status{
		Pane:      pane,
		Event:     p.HookEventName,
		Type:      p.NotificationType,
		SessionID: p.SessionID,
		CWD:       p.CWD,
		At:        now,
	}
	if err := os.MkdirAll(statusDir, 0755); err != nil {
		return fmt.Errorf("failed to create status directory: %w", err)
	}
	data, err := json.Marshal(st)
	if err != nil {
		return err
	}
	// Write then rename so readers never see a partial file.
	final := filepath.Join(statusDir, fileName(pane))
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

// Read returns the status recorded for pane, if any.
func Read(statusDir, pane string) (Status, bool) {
	if pane == "" {
		return Status{}, false
	}
	data, err := os.ReadFile(filepath.Join(statusDir, fileName(pane)))
	if err != nil {
		return Status{}, false
	}
	var st Status
	if err := json.Unmarshal(data, &st); err != nil {
		return Status{}, false
	}
	return st, true
}

// Remove forgets the status recorded for pane.
func Remove(statusDir, pane string) {
	if pane == "" {
		return
	}
	_ = os.Remove(filepath.Join(statusDir, fileName(pane)))
}

// Settings returns the Claude Code settings JSON that routes every event in Events to
// the given shell command.
func Settings(command string) ([]byte, error) {
	entry := []map[string]any{{
		"hooks": []map[string]string{{"type": "command", "command": command}},
	}}
	hooks := make(map[string]any, len(Events))
	for _, ev := range Events {
		hooks[ev] = entry
	}
	return json.MarshalIndent(map[string]any{"hooks": hooks}, "", "  ")
}

// HookCommand is the shell command Claude runs on each event for the given cs executable.
func HookCommand(execPath string) string {
	return shellQuote(execPath) + " hook"
}

// EnsureSettings writes the settings file routing hook events to execPath, if it is
// missing or out of date, and returns its path.
func EnsureSettings(execPath string) (string, error) {
	dir, err := config.GetConfigDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, SettingsFileName)
	data, err := Settings(HookCommand(execPath))
	if err != nil {
		return "", err
	}
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, data) {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write %s: %w", path, err)
	}
	return path, nil
}

// IsClaude reports whether a program string launches Claude Code.
func IsClaude(program string) bool {
	fields := strings.Fields(program)
	return len(fields) > 0 && filepath.Base(fields[0]) == "claude"
}

// LaunchArgs returns the arguments to append to a claude program so its hook events reach
// cs, or "" when the program is not claude or already carries its own --settings.
func LaunchArgs(program, settingsPath string) string {
	if !IsClaude(program) || strings.Contains(program, "--settings") {
		return ""
	}
	return " --settings " + shellQuote(settingsPath)
}

// shellQuote single-quotes s when it contains characters the shell would interpret.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"$`\\*?[]()<>|&;") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
