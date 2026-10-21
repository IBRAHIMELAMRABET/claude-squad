// Package sidebar lays claude-squad out like a desktop session manager: the selected agent
// session, attached for real, fills the terminal, and the session list opens over its left
// edge as a tmux popup. Opening or closing the list never resizes the session, so the
// program inside never has to redraw.
//
// Two tmux sessions make the layout: UISession holds one pane, the viewer, which shows the
// selected session (or a placeholder, a diff, a shell); ListSession runs the list app. The
// popup is a nested client of ListSession, so the list keeps its state while hidden.
//
// Everything shown in the viewer runs as `sh <script>`, where the script is written under
// the config directory. That avoids shell quoting inside tmux arguments and lets a viewer
// (diff, shell, help) hand the pane back to the session when it exits.
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
	// UISession is the tmux session the terminal is attached to: one pane, the viewer.
	UISession = tmux.TmuxPrefix + "ui"
	// ListSession runs the session list shown in the popup.
	ListSession = tmux.TmuxPrefix + "list"
	// DefaultWidth is the list width in columns when none is configured.
	DefaultWidth = 32
	// EnvSidebar marks the process running the list.
	EnvSidebar = "CLAUDE_SQUAD_SIDEBAR"
)

// viewerTarget is the viewer pane.
const viewerTarget = "=" + UISession + ":0.0"

// Controller drives the viewer and the list popup.
type Controller struct {
	exec cmd.Executor
	// self is the list's own pane id ($TMUX_PANE).
	self string
	// dir is where the viewer scripts are written.
	dir string
	// width is the list width.
	width int
}

// New returns a controller for a list running in pane self.
func New(self string, exec cmd.Executor, width int) (*Controller, error) {
	if self == "" {
		return nil, fmt.Errorf("the sidebar must run inside tmux (TMUX_PANE is empty)")
	}
	if width <= 0 {
		width = DefaultWidth
	}
	dir, err := config.GetConfigDir()
	if err != nil {
		return nil, err
	}
	return &Controller{exec: exec, self: self, dir: filepath.Join(dir, "sidebar"), width: width}, nil
}

// InsideSidebar reports whether this process is the list of the layout.
func InsideSidebar() bool {
	return os.Getenv(EnvSidebar) == "1" && os.Getenv("TMUX_PANE") != ""
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

// show runs a viewer script in the viewer pane, replacing whatever runs there.
func (c *Controller) show(name, body string) error {
	script, err := c.writeScript(name, body)
	if err != nil {
		return err
	}
	return c.exec.Run(exec.Command("tmux", "respawn-pane", "-k", "-t", viewerTarget, "sh "+shellQuote(script)))
}

// ShowSession attaches the viewer to a tmux session.
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

// ShowText shows a static text card in the viewer (for a session that is not running).
func (c *Controller) ShowText(text string) error {
	path, err := c.writeScript("card.txt", text)
	if err != nil {
		return err
	}
	return c.show("card.sh", TextScript(path))
}

// TextScript prints a text file and waits.
func TextScript(path string) string {
	return "clear\ncat " + shellQuote(path) + "\nwhile :; do sleep 3600; done\n"
}

// CloseList closes the list popup, which hands the keyboard back to the viewer.
func (c *Controller) CloseList() error {
	return c.exec.Run(exec.Command("tmux", "detach-client", "-s", "="+ListSession))
}

// ViewerSize returns the size of the viewer pane, or zeros when it cannot be read. New
// sessions are created at this size so showing them needs no resize.
func (c *Controller) ViewerSize() (int, int) {
	out, err := c.exec.Output(exec.Command("tmux", "display-message", "-p", "-t", viewerTarget, "#{pane_width} #{pane_height}"))
	if err != nil {
		return 0, 0
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return 0, 0
	}
	w, errW := strconv.Atoi(fields[0])
	h, errH := strconv.Atoi(fields[1])
	if errW != nil || errH != nil {
		return 0, 0
	}
	return w, h
}

// FitSessions gives the windows of the named sessions the viewer's size, so showing any of
// them needs no resize (and no redraw by the program inside). The window keeps following
// its clients afterwards: resize-window switches window-size to manual, which is undone.
func (c *Controller) FitSessions(names []string) {
	w, h := c.ViewerSize()
	if w <= 0 || h <= 0 {
		return
	}
	for _, name := range names {
		target := "=" + name
		_ = c.exec.Run(exec.Command("tmux", "resize-window", "-t", target, "-x", strconv.Itoa(w), "-y", strconv.Itoa(h)))
		_ = c.exec.Run(exec.Command("tmux", "set-option", "-w", "-t", target, "window-size", "latest"))
	}
}

// Quit closes the whole layout. Agent sessions keep running detached. The order matters:
// the pane-died hook is removed, the nested client in the viewer is replaced by the
// placeholder so it exits cleanly, and only then are the layout sessions killed. Killing a
// session while it still hosts a client of the same server has taken the server, and every
// agent session with it, down. The list session, which runs this process, goes last.
func (c *Controller) Quit() error {
	_ = c.exec.Run(exec.Command("tmux", "set-hook", "-u", "-t", "="+UISession, "pane-died"))
	_ = c.ShowPlaceholder()
	err := c.exec.Run(exec.Command("tmux", "kill-session", "-t", "="+UISession))
	_ = c.exec.Run(exec.Command("tmux", "kill-session", "-t", "="+ListSession))
	return err
}

// ListAttach is the shell command the popup runs: a nested client of the list session.
// TMUX is cleared so tmux accepts nesting.
func ListAttach() string {
	return "TMUX= exec tmux attach-session -t =" + ListSession
}

// OpenList returns the tmux command (as arguments) opening the list over the left edge of
// the current client. -B draws no border: the list draws its own edge.
func OpenList(width int) []string {
	if width <= 0 {
		width = DefaultWidth
	}
	return []string{"display-popup", "-E", "-B", "-x", "0", "-y", "0", "-w", strconv.Itoa(width), "-h", "100%", ListAttach()}
}

// KeyBindings returns the tmux commands binding the layout keys. Alt+S opens the list when
// pressed in the viewer and closes it when pressed in the list; elsewhere it reaches the
// program unchanged. Alt+Left opens and Alt+Right closes, for terminals that pass them on
// (Windows Terminal keeps them for its own panes).
func KeyBindings(width int) [][]string {
	inUI := "#{==:#{session_name}," + UISession + "}"
	inList := "#{==:#{session_name}," + ListSession + "}"
	open := strings.Join(quoteLast(OpenList(width)), " ")
	return [][]string{
		{"bind-key", "-n", "M-s", "if-shell", "-F", inUI, open, "if-shell -F '" + inList + "' 'detach-client' 'send-keys M-s'"},
		{"bind-key", "-n", "M-Left", "if-shell", "-F", inUI, open, "send-keys M-Left"},
		{"bind-key", "-n", "M-Right", "if-shell", "-F", inList, "detach-client", "send-keys M-Right"},
	}
}

// quoteLast double-quotes the last argument (the popup's shell command) so the command can
// be written as one tmux command string.
func quoteLast(args []string) []string {
	out := append([]string(nil), args...)
	out[len(out)-1] = `"` + out[len(out)-1] + `"`
	return out
}

// TrueColorTerminal reports whether the terminal described by the environment renders
// 24-bit colour: COLORTERM says so, or it is Windows Terminal, VS Code, iTerm2 or WezTerm.
func TrueColorTerminal(getenv func(string) string) bool {
	switch strings.ToLower(getenv("COLORTERM")) {
	case "truecolor", "24bit":
		return true
	}
	if getenv("WT_SESSION") != "" {
		return true
	}
	switch getenv("TERM_PROGRAM") {
	case "vscode", "iTerm.app", "WezTerm":
		return true
	}
	return false
}

// AttachScript shows a tmux session inside the pane. TMUX is cleared so tmux accepts nesting.
func AttachScript(sessionName string) string {
	return "TMUX= exec tmux attach-session -t " + shellQuote("="+sessionName) + "\n"
}

// PlaceholderScript shows a hint and waits.
func PlaceholderScript() string {
	return "clear\n" +
		"printf '\\n\\n   No session selected.\\n\\n   Alt+S opens the list: pick a session, or press n to create one.\\n'\n" +
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
