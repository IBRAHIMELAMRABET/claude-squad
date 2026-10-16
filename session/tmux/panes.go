package tmux

import (
	"claude-squad/cmd"
	"os/exec"
	"strings"
)

// Pane describes one pane on the tmux server.
type Pane struct {
	// Session is the name of the tmux session the pane belongs to.
	Session string
	// ID is the pane id, e.g. "%3".
	ID string
	// Command is the name of the process in the foreground of the pane.
	Command string
	// Path is the working directory of that process.
	Path string
}

// Managed reports whether the pane belongs to a session created by claude-squad (an agent
// session or one of the Terminal tab shells).
func (p Pane) Managed() bool {
	return strings.HasPrefix(p.Session, TmuxPrefix)
}

const listPanesFormat = "#{session_name}\t#{pane_id}\t#{pane_current_command}\t#{pane_current_path}"

// ListPanes lists every pane on the tmux server. A server that is not running yields no
// panes and no error.
func ListPanes(cmdExec cmd.Executor) ([]Pane, error) {
	out, err := cmdExec.Output(exec.Command("tmux", "list-panes", "-a", "-F", listPanesFormat))
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return nil, nil // no server running
		}
		return nil, err
	}
	return ParsePanes(string(out)), nil
}

// ParsePanes parses the output of list-panes with listPanesFormat.
func ParsePanes(out string) []Pane {
	var panes []Pane
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 4 || fields[0] == "" {
			continue
		}
		panes = append(panes, Pane{Session: fields[0], ID: fields[1], Command: fields[2], Path: fields[3]})
	}
	return panes
}

// NewTmuxSessionRaw wraps an existing tmux session by its exact name, without the
// claude-squad prefix. The session is observable and attachable but never created or
// killed by claude-squad.
func NewTmuxSessionRaw(sessionName string, program string) *TmuxSession {
	t := newTmuxSession("", program, MakePtyFactory(), cmd.MakeExecutor())
	t.sanitizedName = sessionName
	t.monitor = newStatusMonitor()
	t.foreign = true
	return t
}

// CloseClient closes the detached client opened by Restore while keeping the tmux
// session alive. Used for adopted sessions, so claude-squad never holds a client that
// could constrain the owner's window size.
func (t *TmuxSession) CloseClient() error {
	if t.ptmx == nil {
		return nil
	}
	err := t.ptmx.Close()
	t.ptmx = nil
	return err
}
