// Package sidebar lays claude-squad out like a desktop session manager: the session list in
// a narrow tmux pane on the left and the selected agent session, attached for real, on the
// right. It drives tmux with small commands; the Bubble Tea app only decides what to show.
//
// Everything shown on the right runs as `sh <script>`, where the script is written under the
// config directory. That avoids shell quoting inside tmux arguments and lets a viewer (diff,
// shell, help) hand the pane back to the session when it exits.
package sidebar

import (
	"claude-squad/cmd"
	"claude-squad/config"
	"claude-squad/session/tmux"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// UISession is the outer tmux session holding the sidebar and the session pane.
	UISession = tmux.TmuxPrefix + "ui"
	// Width is the sidebar pane width in columns.
	Width = 36
	// EnvSidebar marks the process running inside the sidebar pane.
	EnvSidebar = "CLAUDE_SQUAD_SIDEBAR"
)

// Controller drives the pane beside the sidebar.
type Controller struct {
	exec cmd.Executor
	// self is the sidebar's own pane id ($TMUX_PANE).
	self string
	// dir is where the viewer scripts are written.
	dir string
}

// New returns a controller for a sidebar running in pane self.
func New(self string, exec cmd.Executor) (*Controller, error) {
	if self == "" {
		return nil, fmt.Errorf("the sidebar must run inside tmux (TMUX_PANE is empty)")
	}
	dir, err := config.GetConfigDir()
	if err != nil {
		return nil, err
	}
	return &Controller{exec: exec, self: self, dir: filepath.Join(dir, "sidebar")}, nil
}

// InsideSidebar reports whether this process is the sidebar pane of the UI session.
func InsideSidebar() bool {
	return os.Getenv(EnvSidebar) == "1" && os.Getenv("TMUX_PANE") != ""
}

// OtherPane parses `list-panes -F "#{pane_id}<TAB>#{pane_left}"` output and returns the
// rightmost pane that is not self, or "".
func OtherPane(out, self string) string {
	best, bestLeft := "", -1
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(strings.TrimSpace(line), "\t")
		if fields[0] == "" || fields[0] == self {
			continue
		}
		left := 0
		if len(fields) > 1 {
			left, _ = strconv.Atoi(fields[1])
		}
		if left > bestLeft {
			best, bestLeft = fields[0], left
		}
	}
	return best
}

// RightPane returns the pane beside the sidebar, creating it when it went missing.
func (c *Controller) RightPane() (string, error) {
	list := func() (string, error) {
		out, err := c.exec.Output(exec.Command("tmux", "list-panes", "-t", c.self, "-F", "#{pane_id}\t#{pane_left}"))
		if err != nil {
			return "", fmt.Errorf("could not list panes: %w", err)
		}
		return OtherPane(string(out), c.self), nil
	}
	if id, err := list(); err != nil || id != "" {
		return id, err
	}
	script, err := c.writeScript("placeholder.sh", PlaceholderScript())
	if err != nil {
		return "", err
	}
	if err := c.exec.Run(exec.Command("tmux", "split-window", "-h", "-d", "-t", c.self, "sh "+shellQuote(script))); err != nil {
		return "", fmt.Errorf("could not create the session pane: %w", err)
	}
	_ = c.exec.Run(exec.Command("tmux", "resize-pane", "-t", c.self, "-x", strconv.Itoa(Width)))
	id, err := list()
	if err == nil && id == "" {
		err = fmt.Errorf("the session pane did not appear")
	}
	return id, err
}

// writeScript stores a viewer script and returns its path.
func (c *Controller) writeScript(name, body string) (string, error) {
	if err := os.MkdirAll(c.dir, 0700); err != nil {
		return "", err
	}
	path := filepath.Join(c.dir, name)
	if err := os.WriteFile(path, []byte(body), 0700); err != nil {
		return "", fmt.Errorf("could not write %s: %w", path, err)
	}
	return path, nil
}

// show runs a viewer script in the right pane, replacing whatever runs there.
func (c *Controller) show(name, body string) error {
	pane, err := c.RightPane()
	if err != nil {
		return err
	}
	script, err := c.writeScript(name, body)
	if err != nil {
		return err
	}
	return c.exec.Run(exec.Command("tmux", "respawn-pane", "-k", "-t", pane, "sh "+shellQuote(script)))
}

// ShowSession attaches the right pane to a tmux session.
func (c *Controller) ShowSession(sessionName string) error {
	return c.show("attach.sh", AttachScript(sessionName))
}

// ShowPlaceholder shows the "no session" hint.
func (c *Controller) ShowPlaceholder() error {
	return c.show("placeholder.sh", PlaceholderScript())
}

// ShowDiff pages the diff of workdir (against base when given) and then returns to the session.
func (c *Controller) ShowDiff(workdir, base, sessionName string) error {
	return c.show("diff.sh", DiffScript(workdir, base, sessionName))
}

// ShowShell opens an interactive shell in workdir; leaving it returns to the session.
func (c *Controller) ShowShell(workdir, sessionName string) error {
	return c.show("shell.sh", ShellScript(workdir, sessionName))
}

// ShowHelp pages a help text and then returns to the session (or the placeholder).
func (c *Controller) ShowHelp(text, sessionName string) error {
	path, err := c.writeScript("help.txt", text)
	if err != nil {
		return err
	}
	return c.show("help.sh", HelpScript(path, sessionName))
}

// FocusRight moves the cursor into the session pane.
func (c *Controller) FocusRight() error {
	pane, err := c.RightPane()
	if err != nil {
		return err
	}
	return c.exec.Run(exec.Command("tmux", "select-pane", "-t", pane))
}

// Zoomed reports whether the window is currently zoomed on one pane.
func (c *Controller) Zoomed() bool {
	out, err := c.exec.Output(exec.Command("tmux", "display-message", "-p", "-t", c.self, "#{window_zoomed_flag}"))
	return err == nil && strings.TrimSpace(string(out)) == "1"
}

// ZoomSession zooms the session pane to the full window if it is not already.
func (c *Controller) ZoomSession() error {
	if c.Zoomed() {
		return nil
	}
	return c.ToggleZoom()
}

// ToggleZoom zooms the session pane to the full window, or back.
func (c *Controller) ToggleZoom() error {
	pane, err := c.RightPane()
	if err != nil {
		return err
	}
	return c.exec.Run(exec.Command("tmux", "resize-pane", "-Z", "-t", pane))
}

// Quit closes the whole layout. Agent sessions keep running detached.
func (c *Controller) Quit() error {
	return c.exec.Run(exec.Command("tmux", "kill-session", "-t", "="+UISession))
}

// AttachScript shows a tmux session inside the pane. TMUX is cleared so tmux accepts nesting.
func AttachScript(sessionName string) string {
	return "TMUX= exec tmux attach-session -t " + shellQuote("="+sessionName) + "\n"
}

// PlaceholderScript shows a hint and waits.
func PlaceholderScript() string {
	return "clear\n" +
		"printf '\\n\\n   No session selected.\\n\\n   Pick one on the left, or press n to create one.\\n'\n" +
		"while :; do sleep 3600; done\n"
}

// returnScript hands the pane back to the session, or to the placeholder.
func returnScript(sessionName string) string {
	if sessionName == "" {
		return PlaceholderScript()
	}
	return AttachScript(sessionName)
}

// DiffScript pages the diff of workdir, against base when given, then returns to the session.
func DiffScript(workdir, base, sessionName string) string {
	diff := "git -C " + shellQuote(workdir) + " --no-pager diff --color=always"
	if base != "" {
		diff += " " + shellQuote(base)
	}
	return diff + " | less -R\n" + returnScript(sessionName)
}

// ShellScript opens the user's shell in workdir, then returns to the session.
func ShellScript(workdir, sessionName string) string {
	return "cd " + shellQuote(workdir) + " && \"${SHELL:-sh}\"\n" + returnScript(sessionName)
}

// HelpScript pages a text file, then returns to the session.
func HelpScript(path, sessionName string) string {
	return "less -R " + shellQuote(path) + "\n" + returnScript(sessionName)
}

// shellQuote single-quotes s for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
