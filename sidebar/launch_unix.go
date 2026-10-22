//go:build !windows

package sidebar

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"claude-squad/config"

	"golang.org/x/term"
)

// Launch creates the layout sessions when needed, running sidebarCommand as the list, and
// attaches the current terminal with the list open. On success it does not return: the
// process is replaced by tmux. When already inside tmux, the client is switched to the
// layout instead and Launch returns nil.
func Launch(sidebarCommand string, width int) error {
	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		return fmt.Errorf("tmux is required: %w", err)
	}
	if width <= 0 {
		width = DefaultWidth
	}
	if err := ensureLayout(sidebarCommand, width); err != nil {
		return err
	}
	restartStaleList(sidebarCommand)
	if TrueColorTerminal(os.Getenv) {
		enableTrueColor()
	}
	for _, args := range KeyBindings(width) {
		_ = run("tmux", args...)
	}
	// The previous layout kept the list in a split pane and re-applied it on resize.
	_ = run("tmux", "set-hook", "-gu", "client-resized")

	open := OpenList(width)
	if os.Getenv("TMUX") != "" {
		return run(tmuxPath, append([]string{"switch-client", "-t", "=" + UISession, ";"}, open...)...)
	}
	argv := append([]string{"tmux", "attach-session", "-t", "=" + UISession, ";"}, open...)
	return syscall.Exec(tmuxPath, argv, os.Environ())
}

// enableTrueColor tells tmux the outer terminal renders 24-bit colour and tells programs in
// new panes so too, so Claude Code draws its theme exactly as it does outside tmux.
func enableTrueColor() {
	_ = run("tmux", "set-environment", "-g", "COLORTERM", "truecolor")
	out, _ := exec.Command("tmux", "show-options", "-s", "terminal-features").Output()
	if !strings.Contains(string(out), "RGB") {
		_ = run("tmux", "set-option", "-sa", "terminal-features", "xterm*:RGB")
		_ = run("tmux", "set-option", "-sa", "terminal-features", "tmux*:RGB")
	}
}

func hasSession(name string) bool {
	return exec.Command("tmux", "has-session", "-t", "="+name).Run() == nil
}

// ensureLayout creates the viewer session and the list session when they are missing. A
// viewer session from the earlier split layout (two panes, the list in the first) is closed
// first, the same careful way the list's quit does.
func ensureLayout(sidebarCommand string, width int) error {
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
	if err := os.WriteFile(placeholder, []byte(PlaceholderScript(width+2)), 0700); err != nil {
		return err
	}

	if hasSession(UISession) {
		out, _ := exec.Command("tmux", "list-panes", "-t", "="+UISession, "-F", "#{pane_id}").Output()
		if len(strings.Fields(string(out))) > 1 {
			_ = run("tmux", "set-hook", "-u", "-t", "="+UISession, "pane-died")
			_ = run("tmux", "respawn-pane", "-k", "-t", "="+UISession+":0.1", "sh "+shellQuote(placeholder))
			_ = run("tmux", "kill-session", "-t", "="+UISession)
		}
	}

	if !hasSession(UISession) {
		viewer := UISession + ":0.0"
		steps := [][]string{
			{"new-session", "-d", "-s", UISession, "-x", strconv.Itoa(cols), "-y", strconv.Itoa(rows), "-c", cwd,
				"sh " + shellQuote(placeholder)},
			{"set-option", "-t", UISession, "status", "off"},
			{"set-option", "-t", UISession, "mouse", "on"},
			{"set-option", "-w", "-t", viewer, "remain-on-exit", "on"},
			// A viewer whose command ended (the session was killed or exited) shows the
			// placeholder instead of "Pane is dead".
			{"set-hook", "-t", UISession, "pane-died", "respawn-pane -k -t " + viewer + " " + shellQuote("sh "+placeholder)},
		}
		if err := runAll(steps, UISession); err != nil {
			return err
		}
	}

	if !hasSession(ListSession) {
		steps := [][]string{
			{"new-session", "-d", "-s", ListSession, "-x", strconv.Itoa(width), "-y", strconv.Itoa(rows), "-c", cwd,
				"-e", EnvSidebar + "=1", sidebarCommand},
			{"set-option", "-t", ListSession, "status", "off"},
			{"set-option", "-t", ListSession, "mouse", "on"},
		}
		if err := runAll(steps, ListSession); err != nil {
			return err
		}
	}
	return nil
}

// restartStaleList restarts the list when it runs a cs binary that was replaced since it
// started (an upgrade): attaching again would otherwise keep showing the old version. Agent
// sessions are not touched. Linux only: elsewhere /proc is missing and nothing happens.
func restartStaleList(sidebarCommand string) {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", "="+ListSession+":", "#{pane_pid}").Output()
	if err != nil {
		return
	}
	exe, err := os.Readlink(filepath.Join("/proc", strings.TrimSpace(string(out)), "exe"))
	if err != nil || !ReplacedBinary(exe) {
		return
	}
	_ = run("tmux", "respawn-pane", "-k", "-t", "="+ListSession+":", "-e", EnvSidebar+"=1", sidebarCommand)
}

// runAll runs tmux commands in order, killing the session being built when one fails.
func runAll(steps [][]string, session string) error {
	for _, args := range steps {
		if err := run("tmux", args...); err != nil {
			_ = exec.Command("tmux", "kill-session", "-t", "="+session).Run()
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
