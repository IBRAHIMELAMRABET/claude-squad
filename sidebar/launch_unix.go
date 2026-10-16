//go:build !windows

package sidebar

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"

	"claude-squad/config"

	"golang.org/x/term"
)

// Launch creates the UI session when needed, running sidebarCommand in its left pane, and
// attaches the current terminal to it. On success it does not return: the process is
// replaced by tmux. When already inside tmux, the client is switched to the UI session
// instead and Launch returns nil.
func Launch(sidebarCommand string, width int) error {
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return fmt.Errorf("tmux is required: %w", err)
	}
	if exec.Command(tmuxPath, "has-session", "-t", "="+UISession).Run() != nil {
		if err := create(sidebarCommand, width); err != nil {
			return err
		}
	}
	if os.Getenv("TMUX") != "" {
		return run(tmuxPath, "switch-client", "-t", "="+UISession)
	}
	return syscall.Exec(tmuxPath, []string{"tmux", "attach-session", "-t", "=" + UISession}, os.Environ())
}

// create builds the two-pane layout: the sidebar on the left, a placeholder on the right.
func create(sidebarCommand string, width int) error {
	if width <= 0 {
		width = DefaultWidth
	}
	cols, rows := 160, 45
	if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 && h > 0 {
		cols, rows = w, h
	}
	cwd, _ := os.Getwd()

	configDir, err := config.GetConfigDir()
	if err != nil {
		return err
	}
	scriptDir := filepath.Join(configDir, "sidebar")
	if err := os.MkdirAll(scriptDir, 0700); err != nil {
		return err
	}
	placeholder := filepath.Join(scriptDir, "placeholder.sh")
	if err := os.WriteFile(placeholder, []byte(PlaceholderScript()), 0700); err != nil {
		return err
	}

	target := UISession + ":0.0"
	steps := [][]string{
		{"new-session", "-d", "-s", UISession, "-x", strconv.Itoa(cols), "-y", strconv.Itoa(rows), "-c", cwd,
			"-e", EnvSidebar + "=1", sidebarCommand},
		{"set-option", "-t", UISession, "status", "off"},
		{"set-option", "-t", UISession, "mouse", "on"},
		{"set-option", "-w", "-t", target, "remain-on-exit", "on"},
		{"split-window", "-h", "-d", "-t", target, "-c", cwd, "sh " + shellQuote(placeholder)},
		// main-vertical keeps the sidebar as the fixed-width main pane on the left; the global
		// hook re-applies it when the terminal is resized (hooks on client events are global).
		{"set-option", "-w", "-t", target, "main-pane-width", strconv.Itoa(width)},
		{"select-layout", "-t", target, "main-vertical"},
		{"set-hook", "-g", "client-resized",
			"if-shell -F '#{==:#{session_name}," + UISession + "}' 'select-layout -t " + UISession + ":0 main-vertical'"},
		// A session pane whose command ended (the session was killed or exited) shows the
		// placeholder instead of "Pane is dead".
		{"set-hook", "-t", UISession, "pane-died", "respawn-pane -k -t " + UISession + ":0.1 " + shellQuote("sh "+placeholder)},
		// Alt+arrows move between the panes, only inside the UI session.
		{"bind-key", "-n", "M-Left", "if-shell", "-F", "#{==:#{session_name}," + UISession + "}", "select-pane -L", "send-keys M-Left"},
		{"bind-key", "-n", "M-Right", "if-shell", "-F", "#{==:#{session_name}," + UISession + "}", "select-pane -R", "send-keys M-Right"},
	}
	for _, args := range steps {
		if err := run("tmux", args...); err != nil {
			_ = exec.Command("tmux", "kill-session", "-t", "="+UISession).Run()
			return fmt.Errorf("tmux %s: %w", args[0], err)
		}
	}
	return nil
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, string(out))
	}
	return nil
}
