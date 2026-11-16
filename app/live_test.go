package app

import (
	"claude-squad/config"
	"claude-squad/session"
	"claude-squad/session/history"
	"claude-squad/ui"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/stretchr/testify/require"
)

func TestMatchLive(t *testing.T) {
	t0 := time.Date(2026, 11, 16, 10, 34, 31, 0, time.UTC)
	at := func(d time.Duration) int64 { return t0.Add(d).UnixMilli() }
	created := map[string]time.Time{"cleared": t0, "fresh": t0.Add(time.Hour)}
	createdAt := func(name string) (time.Time, error) {
		if c, ok := created[name]; ok {
			return c, nil
		}
		return time.Time{}, fmt.Errorf("no tmux session %s", name)
	}
	n := 0
	inst := func() *session.Instance { n++; return &session.Instance{Title: fmt.Sprint("s", n)} }

	t.Run("the process matched before, whatever conversation it is in now", func(t *testing.T) {
		a := inst()
		live := []history.Live{
			{PID: 7, ProcStart: "old", SessionID: "x", CWD: "/w", StartedAt: at(0)},
			{PID: 7, ProcStart: "now", SessionID: "after-clear", CWD: "/w", StartedAt: at(time.Hour)},
		}
		found := matchLive([]liveProbe{{inst: a, id: "before-clear", dir: "/w", tmux: "gone", link: liveLink{7, "now"}}}, live, createdAt)
		require.Equal(t, "after-clear", found[a].SessionID, "same pid and process start")
	})

	t.Run("by conversation: the newest process in it", func(t *testing.T) {
		a := inst()
		live := []history.Live{
			{PID: 1, ProcStart: "1", SessionID: "conv", StartedAt: at(-48 * time.Hour)}, // exited long ago
			{PID: 2, ProcStart: "2", SessionID: "conv", StartedAt: at(6 * time.Second)},
		}
		found := matchLive([]liveProbe{{inst: a, id: "conv", dir: "/elsewhere", tmux: "gone"}}, live, createdAt)
		require.Equal(t, 2, found[a].PID)
	})

	t.Run("by folder and start time when the conversation changed unseen", func(t *testing.T) {
		a, b, c := inst(), inst(), inst()
		live := []history.Live{
			{PID: 10, ProcStart: "a", SessionID: "after-clear", CWD: "/home", StartedAt: at(6 * time.Second)},
			{PID: 11, ProcStart: "b", SessionID: "someone-else", CWD: "/home", StartedAt: at(40 * time.Minute)},
			{PID: 12, ProcStart: "c", SessionID: "other-dir", CWD: "/srv", StartedAt: at(5 * time.Second)},
		}
		found := matchLive([]liveProbe{
			{inst: a, id: "before-clear", dir: "/home", tmux: "cleared"},
			{inst: b, id: "nothing", dir: "/home", tmux: "cleared"},
			{inst: c, id: "nothing", dir: "/home", tmux: "fresh"},
		}, live, createdAt)
		require.Equal(t, "after-clear", found[a].SessionID, "started in its folder right after its tmux session")
		require.NotContains(t, found, b, "a process goes to one session only")
		require.NotContains(t, found, c, "a process started 20 minutes before the session is not its")
	})

	t.Run("nothing to go on", func(t *testing.T) {
		a := inst()
		found := matchLive([]liveProbe{{inst: a, id: "conv", dir: "/w", tmux: "gone"}}, []history.Live{
			{PID: 7, ProcStart: "x", SessionID: "other", CWD: "/w", StartedAt: at(0)},
		}, createdAt)
		require.Empty(t, found, "no link, no conversation, no tmux session to date it")
	})
}

// A session cleared with /clear follows its new conversation (and resumes it), and takes
// the name given with /rename.
func TestApplyLiveFollowsClearAndRename(t *testing.T) {
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	h := &home{
		ctx:       context.Background(),
		appConfig: &config.Config{},
		list:      ui.NewList(&sp, false),
	}
	resumed, err := session.NewInstance(session.InstanceOptions{Title: "stats", Path: t.TempDir(), Program: "claude", ResumeID: "before-clear"})
	require.NoError(t, err)
	fresh, err := session.NewInstance(session.InstanceOptions{Title: "api", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	fresh.ClaudeSessionID = "started-as"
	ended, err := session.NewInstance(session.InstanceOptions{Title: "ended", Path: t.TempDir(), Program: "claude", ResumeID: "kept"})
	require.NoError(t, err)
	ended.SetStatus(session.Paused)
	for _, inst := range []*session.Instance{resumed, fresh, ended} {
		h.list.AddInstance(inst)()
	}

	moved := h.applyLive(map[*session.Instance]history.Live{
		resumed: {PID: 1, ProcStart: "a", SessionID: "after-clear", Name: "Me", NameSource: "user"},
		fresh:   {PID: 2, ProcStart: "b", SessionID: "started-as", Name: "auto", NameSource: "auto"},
		ended:   {PID: 3, ProcStart: "c", SessionID: "elsewhere", Name: "No", NameSource: "user"},
	})
	require.True(t, moved)
	require.Equal(t, "after-clear", resumed.ResumeID, "resuming continues the conversation it is in now")
	require.Equal(t, "after-clear", claudeID(resumed), "its transcript is the new one")
	require.Equal(t, "Me", resumed.DisplayTitle())
	require.Equal(t, "api", fresh.DisplayTitle(), "no /rename: the name typed in cs stays")
	require.Equal(t, "kept", ended.ResumeID, "an ended session has no process to follow")
	require.Equal(t, "ended", ended.DisplayTitle())
	require.Equal(t, liveLink{1, "a"}, h.liveLinks[resumed], "linked for the next check")
	require.NotContains(t, h.liveLinks, ended)

	require.False(t, h.applyLive(map[*session.Instance]history.Live{
		resumed: {PID: 1, ProcStart: "a", SessionID: "after-clear", Name: "Me", NameSource: "user"},
	}), "nothing moved: nothing to save")
}
