package session

import (
	"claude-squad/session/tmux"
	"path/filepath"
	"time"
)

// AdoptablePanes returns the panes whose foreground program is one of programs and whose
// tmux session was not created by claude-squad. Program names are compared by base name,
// so "claude" matches "/home/me/.local/bin/claude".
func AdoptablePanes(panes []tmux.Pane, programs []string) []tmux.Pane {
	wanted := make(map[string]bool, len(programs))
	for _, p := range programs {
		wanted[filepath.Base(p)] = true
	}
	var out []tmux.Pane
	for _, p := range panes {
		if p.Managed() || !wanted[filepath.Base(p.Command)] {
			continue
		}
		out = append(out, p)
	}
	return out
}

// NewAdoptedInstance wraps a foreign tmux pane running an agent so it shows up in the list
// with a live status and can be attached to. It is never persisted, killed or paused.
func NewAdoptedInstance(p tmux.Pane) *Instance {
	now := time.Now()
	return &Instance{
		Title:       p.Session,
		Path:        p.Path,
		Program:     p.Command,
		Status:      Ready,
		CreatedAt:   now,
		UpdatedAt:   now,
		Adopted:     true,
		started:     true,
		paneID:      p.ID,
		tmuxSession: tmux.NewTmuxSessionRaw(p.Session, p.Command),
	}
}
