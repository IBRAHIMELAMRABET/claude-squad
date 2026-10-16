package session

import (
	"claude-squad/cmd/cmd_test"
	"claude-squad/hooks"
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
		inst.ApplyObservation(tmux.Observation{Screen: screen, Updated: updated}, nil, now)
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

	inst.ApplyObservation(tmux.Observation{Screen: tmux.ScreenPermission}, nil, time.Now())
	require.Equal(t, Running, inst.Status, "auto-yes answers the prompt instead of waiting")
	require.False(t, inst.NeedsAttention())
}

// Hook events recorded by `cs hook` take precedence over the screen, as long as they belong
// to this session (same working directory).
func TestApplyObservationPrefersHooks(t *testing.T) {
	dir := t.TempDir()
	inst, err := NewInstance(InstanceOptions{Title: "hooked", Path: dir, Program: "claude"})
	require.NoError(t, err)
	now := time.Now()
	idle := tmux.Observation{Screen: tmux.ScreenIdle}

	inst.ApplyObservation(idle, &hooks.Status{Event: "PreToolUse", CWD: dir, At: now}, now)
	require.Equal(t, Running, inst.Status, "the hook says working even though the screen looks idle")
	require.Equal(t, now, inst.LastActivity, "hook events count as activity")

	inst.ApplyObservation(idle, &hooks.Status{Event: "PermissionRequest", CWD: dir, At: now}, now)
	require.Equal(t, Waiting, inst.Status)
	require.Equal(t, WaitPermission, inst.WaitReason)

	stop := &hooks.Status{Event: "Stop", CWD: dir, At: now}
	inst.ApplyObservation(idle, stop, now)
	require.Equal(t, WaitDone, inst.WaitReason)

	inst.MarkSeen()
	inst.ApplyObservation(idle, stop, now)
	require.Equal(t, Ready, inst.Status, "a Stop older than the last look is already read")

	later := &hooks.Status{Event: "Stop", CWD: dir, At: time.Now().Add(time.Minute)}
	inst.ApplyObservation(idle, later, now)
	require.Equal(t, WaitDone, inst.WaitReason, "a newer Stop is unread again")

	inst.ApplyObservation(idle, &hooks.Status{Event: "SessionEnd", CWD: dir, At: now}, now)
	require.Equal(t, Ready, inst.Status)
}

func TestApplyObservationIgnoresForeignHooks(t *testing.T) {
	inst, err := NewInstance(InstanceOptions{Title: "mine", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	now := time.Now()

	foreign := &hooks.Status{Event: "PermissionRequest", CWD: "/somewhere/else", At: now}
	inst.ApplyObservation(tmux.Observation{Screen: tmux.ScreenWorking}, foreign, now)
	require.Equal(t, Running, inst.Status, "a hook from another directory is ignored; the screen decides")

	unknown := &hooks.Status{Event: "Notification", Type: "auth_success", CWD: inst.Path, At: now}
	inst.ApplyObservation(tmux.Observation{Screen: tmux.ScreenPermission}, unknown, now)
	require.Equal(t, WaitPermission, inst.WaitReason, "an uninformative hook defers to the screen")
}

func TestHookCWDAndPaneIDBeforeStart(t *testing.T) {
	dir := t.TempDir()
	inst, err := NewInstance(InstanceOptions{Title: "cold", Path: dir, Program: "claude"})
	require.NoError(t, err)
	require.Equal(t, dir, inst.HookCWD(), "no worktree yet: the session path")
	require.Equal(t, "", inst.PaneID(), "not started: no pane")
}

func initRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		require.NoError(t, cmd.Run(), strings.Join(args, " "))
	}
	run("init", "-q", "-b", "trunk")
	run("commit", "-q", "--allow-empty", "-m", "init")
	resolved, err := filepath.EvalSymlinks(repo)
	require.NoError(t, err)
	return resolved
}

// An in-place session starts the agent in the repository itself, on the current branch,
// and never creates a worktree.
func TestStartInPlace(t *testing.T) {
	repo := initRepo(t)
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "sub"), 0755))
	inst, err := NewInstance(InstanceOptions{Title: "here", Path: filepath.Join(repo, "sub"), Program: "claude", InPlace: true})
	require.NoError(t, err)
	// The session exists only once the PTY factory has launched tmux new-session.
	pty := &nullPtyFactory{t: t}
	inst.SetTmuxSession(tmux.NewTmuxSessionWithDeps("here", "claude", pty,
		cmd_test.MockCmdExec{RunFunc: func(cmd *exec.Cmd) error {
			if strings.Contains(cmd.String(), "has-session") && pty.calls == 0 {
				return fmt.Errorf("can't find session")
			}
			return nil
		}}))

	require.NoError(t, inst.Start(true))
	require.Equal(t, Running, inst.Status)
	require.Equal(t, repo, inst.Path, "the session runs at the repository root")
	require.Equal(t, "trunk", inst.Branch)
	require.Equal(t, "", inst.GetWorktreePath())
	require.Equal(t, repo, inst.WorkDir())
	require.Equal(t, repo, inst.HookCWD())
	require.True(t, inst.ToInstanceData().InPlace)
	require.Error(t, inst.Pause(), "checkout does not apply in place")
}

func TestFromInstanceDataKeepsInPlaceWithoutWorktree(t *testing.T) {
	inst, err := FromInstanceData(InstanceData{Title: "paused", Path: "/repo", Status: Paused, InPlace: true, Program: "claude"})
	require.NoError(t, err)
	require.True(t, inst.InPlace)
	require.True(t, inst.Started())
	require.Equal(t, "", inst.GetWorktreePath())
	require.Nil(t, inst.ComputeDiff())
}

func TestExtraArgsQuotesAndExpands(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	got := extraArgs([]string{"--plugin-dir", "~/mods/guard", "--append-system-prompt", "be brief; it's fine"})
	require.Equal(t, " --plugin-dir "+home+`/mods/guard --append-system-prompt 'be brief; it'\''s fine'`, got)
	require.Equal(t, "", extraArgs(nil))
}
