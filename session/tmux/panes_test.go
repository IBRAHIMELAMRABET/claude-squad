package tmux

import (
	"claude-squad/cmd/cmd_test"
	"errors"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParsePanes(t *testing.T) {
	out := "work\t%0\tzsh\t/home/user\n" +
		"claudesquad_fix\t%1\tclaude\t/home/user/.claude-squad/worktrees/fix_1\n" +
		"scratch\t%2\tclaude\t/home/user/repo\n" +
		"broken line without tabs\n" +
		"\n"
	panes := ParsePanes(out)
	require.Equal(t, []Pane{
		{Session: "work", ID: "%0", Command: "zsh", Path: "/home/user"},
		{Session: "claudesquad_fix", ID: "%1", Command: "claude", Path: "/home/user/.claude-squad/worktrees/fix_1"},
		{Session: "scratch", ID: "%2", Command: "claude", Path: "/home/user/repo"},
	}, panes)
	require.False(t, panes[0].Managed())
	require.True(t, panes[1].Managed())
	require.False(t, panes[2].Managed())
}

func TestListPanesNoServer(t *testing.T) {
	exitOne := cmd_test.MockCmdExec{
		OutputFunc: func(c *exec.Cmd) ([]byte, error) {
			// Emulate tmux's exit status 1 when no server is running.
			return nil, exec.Command("sh", "-c", "exit 1").Run().(*exec.ExitError)
		},
	}
	panes, err := ListPanes(exitOne)
	require.NoError(t, err)
	require.Nil(t, panes)

	broken := cmd_test.MockCmdExec{
		OutputFunc: func(c *exec.Cmd) ([]byte, error) { return nil, errors.New("tmux not installed") },
	}
	_, err = ListPanes(broken)
	require.Error(t, err)
}

func TestListPanesUsesAllSessions(t *testing.T) {
	exec := cmd_test.MockCmdExec{
		OutputFunc: func(c *exec.Cmd) ([]byte, error) {
			require.Contains(t, c.String(), "list-panes -a -F")
			return []byte("s\t%4\tclaude\t/r\n"), nil
		},
	}
	panes, err := ListPanes(exec)
	require.NoError(t, err)
	require.Len(t, panes, 1)
	require.Equal(t, "%4", panes[0].ID)
}

func TestNewTmuxSessionRawKeepsTheName(t *testing.T) {
	s := NewTmuxSessionRaw("my-session", "claude")
	require.Equal(t, "my-session", s.sanitizedName)
	require.NotNil(t, s.monitor, "observable without Restore")
	require.NoError(t, s.CloseClient(), "no client open is fine")
}
