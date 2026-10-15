package session

import (
	"claude-squad/config"
	"claude-squad/session/tmux"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdoptablePanes(t *testing.T) {
	panes := []tmux.Pane{
		{Session: "work", ID: "%0", Command: "zsh", Path: "/home/user"},
		{Session: "claudesquad_fix", ID: "%1", Command: "claude", Path: "/wt/fix"},
		{Session: "claudesquad_term_fix", ID: "%2", Command: "zsh", Path: "/wt/fix"},
		{Session: "scratch", ID: "%3", Command: "claude", Path: "/home/user/repo"},
		{Session: "other", ID: "%4", Command: "codex", Path: "/home/user/x"},
	}

	got := AdoptablePanes(panes, []string{"claude"})
	require.Equal(t, []tmux.Pane{panes[3]}, got, "only foreign panes running claude")

	got = AdoptablePanes(panes, []string{"/usr/local/bin/claude", "codex"})
	require.Equal(t, []tmux.Pane{panes[3], panes[4]}, got, "program names compare by base name")

	require.Empty(t, AdoptablePanes(nil, []string{"claude"}))
}

func TestNewAdoptedInstance(t *testing.T) {
	inst := NewAdoptedInstance(tmux.Pane{Session: "scratch", ID: "%3", Command: "claude", Path: "/home/user/repo"})

	require.True(t, inst.Adopted)
	require.True(t, inst.Started())
	require.Equal(t, "scratch", inst.Title)
	require.Equal(t, Ready, inst.Status)
	require.Equal(t, "%3", inst.PaneID(), "pane id comes from discovery, no tmux call needed")
	require.Equal(t, "/home/user/repo", inst.HookCWD())
	require.Equal(t, "/home/user/repo", inst.ProjectPath())
	require.Equal(t, "repo", inst.RepoLabel())
	require.Equal(t, "", inst.GetWorktreePath())

	require.NoError(t, inst.Kill(), "killing is a no-op: the session is not ours")
	require.Error(t, inst.Pause())
	require.Error(t, inst.Resume())
	require.NoError(t, inst.SetPreviewSize(80, 24), "never resizes someone else's window")
	require.Nil(t, inst.ComputeDiff())
	require.Nil(t, inst.ComputeDiffNumstat())
	require.NoError(t, inst.UpdateDiffStats())
	_, err := inst.GetGitWorktree()
	require.Error(t, err)
	_, err = inst.RepoName()
	require.Error(t, err)
}

type memoryState struct {
	data json.RawMessage
}

func (m *memoryState) SaveInstances(data json.RawMessage) error { m.data = data; return nil }
func (m *memoryState) GetInstances() json.RawMessage            { return m.data }
func (m *memoryState) DeleteAllInstances() error                { m.data = json.RawMessage("[]"); return nil }

var _ config.InstanceStorage = (*memoryState)(nil)

func TestStorageSkipsAdoptedInstances(t *testing.T) {
	state := &memoryState{data: json.RawMessage("[]")}
	storage, err := NewStorage(state)
	require.NoError(t, err)

	adopted := NewAdoptedInstance(tmux.Pane{Session: "scratch", ID: "%3", Command: "claude", Path: t.TempDir()})
	require.NoError(t, storage.SaveInstances([]*Instance{adopted}))

	var saved []InstanceData
	require.NoError(t, json.Unmarshal(state.GetInstances(), &saved))
	require.Empty(t, saved, "adopted sessions are rediscovered, not stored")
}
