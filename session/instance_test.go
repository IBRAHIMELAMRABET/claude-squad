package session

import (
	"claude-squad/cmd/cmd_test"
	"claude-squad/log"
	"claude-squad/session/tmux"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	log.Initialize(false)
	defer log.Close()
	os.Exit(m.Run())
}

// nullPtyFactory hands back a throwaway file instead of a real PTY.
type nullPtyFactory struct {
	t     *testing.T
	calls int
}

func (p *nullPtyFactory) Start(cmd *exec.Cmd) (*os.File, error) {
	p.calls++
	return os.OpenFile(filepath.Join(p.t.TempDir(), "pty"), os.O_CREATE|os.O_RDWR, 0644)
}

func (p *nullPtyFactory) Close() {}

// When the tmux server dies between runs, every session goes with it while the worktree
// and branch survive on disk. Restoring such an instance must park it as Paused so the
// user can resume it. Returning an error instead is not an option: LoadInstances aborts on
// the first failure, so one dead session would hide every other instance.
// See https://github.com/smtg-ai/claude-squad/issues/216.
func TestStartPausesInstanceWhenTmuxSessionNoLongerExists(t *testing.T) {
	ptyFactory := &nullPtyFactory{t: t}
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error {
			if strings.Contains(cmd.String(), "has-session") {
				return fmt.Errorf("can't find session")
			}
			return nil
		},
	}

	instance, err := NewInstance(InstanceOptions{Title: "revived", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	instance.SetTmuxSession(tmux.NewTmuxSessionWithDeps("revived", "claude", ptyFactory, cmdExec))

	require.NoError(t, instance.Start(false), "a dead tmux session is recoverable, not a startup failure")
	require.Equal(t, Paused, instance.Status)
	require.True(t, instance.Started())
	require.Zero(t, ptyFactory.calls, "should not attach to a session that does not exist")
}

// The happy path is unchanged: an instance whose session survived comes back Running.
func TestStartRestoresInstanceWhenTmuxSessionSurvives(t *testing.T) {
	ptyFactory := &nullPtyFactory{t: t}
	cmdExec := cmd_test.MockCmdExec{
		RunFunc: func(cmd *exec.Cmd) error { return nil },
	}

	instance, err := NewInstance(InstanceOptions{Title: "alive", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	instance.SetTmuxSession(tmux.NewTmuxSessionWithDeps("alive", "claude", ptyFactory, cmdExec))

	require.NoError(t, instance.Start(false))
	require.Equal(t, Running, instance.Status)
	require.Equal(t, 1, ptyFactory.calls)
}

// ApplyObservation turns what the screen says into a status. The screen wins when it is
// recognised; otherwise output changes mean Running and silence means Ready.
func TestApplyObservation(t *testing.T) {
	inst, err := NewInstance(InstanceOptions{Title: "obs", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	now := time.Now()
	apply := func(screen tmux.ScreenState, updated bool) {
		inst.ApplyObservation(tmux.Observation{Screen: screen, Updated: updated}, now)
	}

	apply(tmux.ScreenWorking, true)
	require.Equal(t, Running, inst.Status)
	require.Equal(t, now, inst.LastActivity, "output changes stamp the activity time")

	apply(tmux.ScreenIdle, true)
	require.Equal(t, Waiting, inst.Status, "working then idle means the turn finished")
	require.Equal(t, WaitDone, inst.WaitReason)

	apply(tmux.ScreenIdle, false)
	require.Equal(t, Waiting, inst.Status, "stays unread until someone looks")

	inst.MarkSeen()
	require.Equal(t, Ready, inst.Status)
	require.Empty(t, inst.WaitReason)

	apply(tmux.ScreenIdle, false)
	require.Equal(t, Ready, inst.Status, "idle after being seen is plain Ready")

	apply(tmux.ScreenPermission, true)
	require.Equal(t, Waiting, inst.Status)
	require.Equal(t, WaitPermission, inst.WaitReason)
	inst.MarkSeen()
	require.Equal(t, Waiting, inst.Status, "a permission prompt is not cleared by looking at it")

	apply(tmux.ScreenQuestion, false)
	require.Equal(t, WaitQuestion, inst.WaitReason)

	apply(tmux.ScreenWorking, true)
	require.Equal(t, Running, inst.Status)
	require.Empty(t, inst.WaitReason)

	// Unknown screens (other programs) fall back to output-change detection.
	apply(tmux.ScreenUnknown, true)
	require.Equal(t, Running, inst.Status)
	apply(tmux.ScreenUnknown, false)
	require.Equal(t, Ready, inst.Status)
}

func TestApplyObservationAutoYesAcceptsPrompts(t *testing.T) {
	inst, err := NewInstance(InstanceOptions{Title: "yes", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	inst.AutoYes = true

	inst.ApplyObservation(tmux.Observation{Screen: tmux.ScreenPermission}, time.Now())
	require.Equal(t, Running, inst.Status, "auto-yes answers the prompt instead of waiting")
	require.False(t, inst.NeedsAttention())
}
