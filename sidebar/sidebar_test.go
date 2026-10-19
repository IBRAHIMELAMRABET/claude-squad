package sidebar

import (
	"claude-squad/cmd/cmd_test"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOtherPane(t *testing.T) {
	out := "%0\t0\n%3\t37\n%1\t120\n"
	require.Equal(t, "%1", OtherPane(out, "%0"), "the rightmost other pane")
	require.Equal(t, "%1", OtherPane(out, "%3"))
	require.Equal(t, "", OtherPane("%0\t0\n", "%0"), "alone in the window")
	require.Equal(t, "", OtherPane("", "%0"))
	require.Equal(t, "%2", OtherPane("%2\n", "%0"), "a missing left column still counts")
}

func TestScripts(t *testing.T) {
	require.Equal(t, "TMUX= exec tmux attach-session -t '=claudesquad_fix'\n", AttachScript("claudesquad_fix"))
	require.Contains(t, PlaceholderScript(), "No session selected")

	diff := DiffScript("/wt/fix", "abc123", "claudesquad_fix")
	require.Contains(t, diff, "git -C '/wt/fix' --no-pager diff --color=always 'abc123' | less -R")
	require.True(t, strings.HasSuffix(diff, AttachScript("claudesquad_fix")), "returns to the session")

	workingTree := DiffScript("/repo", "", "")
	require.Contains(t, workingTree, "diff --color=always | less -R")
	require.Contains(t, workingTree, "No session selected", "no session to return to: placeholder")

	shell := ShellScript("/it's here", "s")
	require.Contains(t, shell, `cd '/it'\''s here' && "${SHELL:-sh}"`)

	require.Contains(t, HelpScript("/h/help.txt", "s"), "less -R '/h/help.txt'")
}

func newTestController(t *testing.T, exec cmd_test.MockCmdExec) *Controller {
	t.Setenv("HOME", t.TempDir())
	c, err := New("%0", exec, 36)
	require.NoError(t, err)
	return c
}

func TestNewRequiresTmux(t *testing.T) {
	_, err := New("", cmd_test.MockCmdExec{}, 36)
	require.Error(t, err)
}

func TestShowSessionRespawnsTheRightPane(t *testing.T) {
	var ran []string
	mock := cmd_test.MockCmdExec{
		OutputFunc: func(c *exec.Cmd) ([]byte, error) { return []byte("%0\t0\n%1\t37\n"), nil },
		RunFunc:    func(c *exec.Cmd) error { ran = append(ran, strings.Join(c.Args, " ")); return nil },
	}
	c := newTestController(t, mock)

	require.NoError(t, c.ShowSession("claudesquad_fix"))
	require.Len(t, ran, 1)
	require.Contains(t, ran[0], "tmux respawn-pane -k -t %1 sh ")
	script := strings.TrimPrefix(ran[0], "tmux respawn-pane -k -t %1 sh ")
	script = strings.Trim(script, "'")
	data, err := os.ReadFile(script)
	require.NoError(t, err)
	require.Equal(t, AttachScript("claudesquad_fix"), string(data))
	require.Equal(t, "attach.sh", filepath.Base(script))

	require.NoError(t, c.FocusRight())
	require.Equal(t, "tmux select-pane -t %1", ran[1])
	require.NoError(t, c.ToggleZoom())
	require.Equal(t, "tmux resize-pane -Z -t %1", ran[2])
	require.NoError(t, c.Quit())
	require.Equal(t, "tmux set-hook -u -t ="+UISession+" pane-died", ran[3], "the hook goes first")
	require.Contains(t, ran[4], "tmux respawn-pane -k -t %1 sh ", "then the nested client is replaced")
	require.Equal(t, "tmux kill-session -t ="+UISession, ran[5], "and only then the session dies")
}

func TestRightPaneIsRecreatedWhenMissing(t *testing.T) {
	calls := 0
	var ran []string
	mock := cmd_test.MockCmdExec{
		OutputFunc: func(c *exec.Cmd) ([]byte, error) {
			calls++
			if calls == 1 {
				return []byte("%0\t0\n"), nil // alone
			}
			return []byte("%0\t0\n%5\t37\n"), nil
		},
		RunFunc: func(c *exec.Cmd) error { ran = append(ran, strings.Join(c.Args, " ")); return nil },
	}
	c := newTestController(t, mock)

	pane, err := c.RightPane()
	require.NoError(t, err)
	require.Equal(t, "%5", pane)
	require.Contains(t, ran[0], "tmux split-window -h -d -t %0 sh ")
	require.Equal(t, "tmux resize-pane -t %0 -x 36", ran[1])
}

func TestShowTextWritesCard(t *testing.T) {
	var ran []string
	mock := cmd_test.MockCmdExec{
		OutputFunc: func(c *exec.Cmd) ([]byte, error) { return []byte("%0\t0\n%1\t37\n"), nil },
		RunFunc:    func(c *exec.Cmd) error { ran = append(ran, strings.Join(c.Args, " ")); return nil },
	}
	c := newTestController(t, mock)
	require.NoError(t, c.ShowText("hello\nworld"))
	require.Contains(t, ran[0], "respawn-pane -k -t %1 sh ")
	data, err := os.ReadFile(filepath.Join(c.dir, "card.txt"))
	require.NoError(t, err)
	require.Equal(t, "hello\nworld", string(data))
	require.Contains(t, TextScript("/x/card.txt"), "cat '/x/card.txt'")
}

func TestKeyBindings(t *testing.T) {
	b := KeyBindings(true)
	require.Len(t, b, 3)
	require.Equal(t, "M-s", b[0][2], "Alt+S toggles the list")
	require.Equal(t, "select-pane -t =claudesquad_ui:0.1 ; resize-pane -Z -t =claudesquad_ui:0.1", b[0][6], "from the list: back to the session")
	require.Contains(t, b[0][7], "select-layout", "elsewhere in the layout: show the list")
	require.Contains(t, b[0][7], "send-keys M-s", "outside the layout the key reaches the program")
	b = b[1:]
	require.Equal(t, []string{"bind-key", "-n", "M-Left", "if-shell", "-F", "#{==:#{session_name},claudesquad_ui}",
		"select-pane -t =claudesquad_ui:0.0 ; select-layout -t =claudesquad_ui:0 main-vertical", "send-keys M-Left"}, b[0])
	require.Equal(t, "M-Right", b[1][2])
	require.Contains(t, b[1][5], "#{==:#{pane_index},0}", "only from the list")
	require.Equal(t, "select-pane -t =claudesquad_ui:0.1 ; resize-pane -Z -t =claudesquad_ui:0.1", b[1][6], "back to the session, zoomed")
	require.Equal(t, "send-keys M-Right", b[1][7], "inside the session the key reaches the program")

	require.Equal(t, "select-pane -t =claudesquad_ui:0.1", KeyBindings(false)[2][6], "no zoom without the drawer")
}

func TestTrueColorTerminal(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	require.True(t, TrueColorTerminal(env(map[string]string{"COLORTERM": "truecolor"})))
	require.True(t, TrueColorTerminal(env(map[string]string{"COLORTERM": "24bit"})))
	require.True(t, TrueColorTerminal(env(map[string]string{"WT_SESSION": "abc"})), "Windows Terminal")
	require.True(t, TrueColorTerminal(env(map[string]string{"TERM_PROGRAM": "vscode"})))
	require.False(t, TrueColorTerminal(env(map[string]string{"TERM": "xterm-256color"})))
}

func TestFixWidth(t *testing.T) {
	var ran []string
	zoomed := "0"
	mock := cmd_test.MockCmdExec{
		OutputFunc: func(c *exec.Cmd) ([]byte, error) { return []byte(zoomed + "\n"), nil },
		RunFunc:    func(c *exec.Cmd) error { ran = append(ran, strings.Join(c.Args, " ")); return nil },
	}
	c := newTestController(t, mock)

	require.NoError(t, c.FixWidth(36))
	require.Empty(t, ran, "already the right width")
	require.NoError(t, c.FixWidth(44))
	require.Equal(t, []string{"tmux resize-pane -t %0 -x 36"}, ran)

	zoomed = "1"
	ran = nil
	require.NoError(t, c.FixWidth(44))
	require.Empty(t, ran, "never while the session is zoomed")
}
