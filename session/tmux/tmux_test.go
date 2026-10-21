package tmux

import (
	cmd2 "claude-squad/cmd"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"claude-squad/cmd/cmd_test"

	"github.com/stretchr/testify/require"
)

type MockPtyFactory struct {
	t *testing.T

	// Array of commands and the corresponding file handles representing PTYs.
	cmds  []*exec.Cmd
	files []*os.File
}

func (pt *MockPtyFactory) Start(cmd *exec.Cmd) (*os.File, error) {
	filePath := filepath.Join(pt.t.TempDir(), fmt.Sprintf("pty-%s-%d", pt.t.Name(), rand.Int31()))
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_RDWR, 0644)
	if err == nil {
		pt.cmds = append(pt.cmds, cmd)
		pt.files = append(pt.files, f)
	}
	return f, err
}

func (pt *MockPtyFactory) Close() {}

func NewMockPtyFactory(t *testing.T) *MockPtyFactory {
	return &MockPtyFactory{
		t: t,
	}
}

func TestSanitizeName(t *testing.T) {
	session := NewTmuxSession("asdf", "program")
	require.Equal(t, TmuxPrefix+"asdf", session.sanitizedName)

	session = NewTmuxSession("a sd f . . asdf", "program")
	require.Equal(t, TmuxPrefix+"asdf__asdf", session.sanitizedName)
}

func TestStartTmuxSession(t *testing.T) {
	ptyFactory := NewMockPtyFactory(t)

	created := false
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			if strings.Contains(cmd.String(), "has-session") && !created {
				created = true
				return fmt.Errorf("session already exists")
			}
			return nil
		},
		OutputFunc: func(cmd *exec.Cmd) ([]byte, error) {
			return []byte("output"), nil
		},
	}

	workdir := t.TempDir()
	session := newTmuxSession("test-session", "claude", ptyFactory, cmdExec)

	err := session.Start(workdir)
	require.NoError(t, err)
	require.Equal(t, 2, len(ptyFactory.cmds))
	require.Equal(t, fmt.Sprintf("tmux new-session -d -s claudesquad_test-session -c %s claude", workdir),
		cmd2.ToString(ptyFactory.cmds[0]))
	require.Equal(t, "tmux attach-session -t claudesquad_test-session",
		cmd2.ToString(ptyFactory.cmds[1]))

	require.Equal(t, 2, len(ptyFactory.files))

	// File should be closed.
	_, err = ptyFactory.files[0].Stat()
	require.Error(t, err)
	// File should be open
	_, err = ptyFactory.files[1].Stat()
	require.NoError(t, err)
}

// A tmux server that has gone away (reboot, crash, `tmux kill-server`) takes every session
// with it. attach-session against a missing session still forks successfully, so Restore
// has to check for the session itself or it reports success while attached to nothing.
func TestRestoreReturnsErrSessionNotFoundWhenSessionIsGone(t *testing.T) {
	ptyFactory := NewMockPtyFactory(t)
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			if strings.Contains(cmd.String(), "has-session") {
				return fmt.Errorf("can't find session")
			}
			return nil
		},
	}

	session := NewTmuxSessionWithDeps("gone", "program", ptyFactory, cmdExec)
	err := session.Restore()

	require.ErrorIs(t, err, ErrSessionNotFound)
	require.Empty(t, ptyFactory.cmds, "should not have opened a PTY for a session that does not exist")
}

func TestRestoreAttachesWhenSessionExists(t *testing.T) {
	ptyFactory := NewMockPtyFactory(t)
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error { return nil },
	}

	session := NewTmuxSessionWithDeps("alive", "program", ptyFactory, cmdExec)
	require.NoError(t, session.Restore())
	require.Len(t, ptyFactory.cmds, 1)
	require.Contains(t, ptyFactory.cmds[0].String(), "attach-session")
}

func TestPaneID(t *testing.T) {
	exec := cmd_test.MockCmdExec{
		OutputFunc: func(c *exec.Cmd) ([]byte, error) {
			require.Contains(t, c.String(), "list-panes")
			require.Contains(t, c.String(), "-t =claudesquad_sess")
			return []byte("%7\n"), nil
		},
	}
	s := NewTmuxSessionWithDeps("sess", "claude", &MockPtyFactory{t: t}, exec)
	id, err := s.PaneID()
	require.NoError(t, err)
	require.Equal(t, "%7", id)
}

func TestStartPassesEnv(t *testing.T) {
	var created string
	exists := false
	exec := cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error {
			if strings.Contains(c.String(), "has-session") && !exists {
				return fmt.Errorf("no session")
			}
			return nil
		},
		OutputFunc: func(c *exec.Cmd) ([]byte, error) { return nil, nil },
	}
	pty := &MockPtyFactory{t: t}
	s := NewTmuxSessionWithDeps("envtest", "claude", pty, exec)
	s.SetEnv([]string{"A=1", "B=2"})
	_ = s.Start("/tmp")
	for _, c := range pty.cmds {
		if strings.Contains(c.String(), "new-session") {
			created = c.String()
		}
	}
	require.Contains(t, created, "-c /tmp -e A=1 -e B=2 claude")
}

// In the sidebar layout the connection kept to each session ignores its size, and new
// sessions start at the viewer's size, so sessions never jump between sizes.
func TestSidebarSizing(t *testing.T) {
	BackgroundClientsIgnoreSize = true
	NewSessionSize = func() (int, int) { return 228, 62 }
	defer func() { BackgroundClientsIgnoreSize = false; NewSessionSize = nil }()

	exists := false
	exec := cmd_test.MockCmdExec{
		RunFunc: func(c *exec.Cmd) error {
			if strings.Contains(c.String(), "has-session") && !exists {
				return fmt.Errorf("no session")
			}
			return nil
		},
		OutputFunc: func(c *exec.Cmd) ([]byte, error) { return nil, nil },
	}
	pty := &MockPtyFactory{t: t}
	s := NewTmuxSessionWithDeps("sized", "claude", pty, exec)
	_ = s.Start("/tmp")
	var created, attached string
	for _, c := range pty.cmds {
		switch {
		case strings.Contains(c.String(), "new-session"):
			created = c.String()
		case strings.Contains(c.String(), "attach-session"):
			attached = c.String()
		}
	}
	require.Contains(t, created, "-x 228 -y 62")
	exists = true
	require.NoError(t, s.Restore())
	for _, c := range pty.cmds {
		if strings.Contains(c.String(), "attach-session") {
			attached = c.String()
		}
	}
	require.Contains(t, attached, "attach-session -f ignore-size -t claudesquad_sized")
}
