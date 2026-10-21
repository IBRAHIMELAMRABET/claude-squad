package app

import (
	"claude-squad/config"
	"claude-squad/session"
	"claude-squad/session/history"
	"claude-squad/session/tmux"
	"claude-squad/ui"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/stretchr/testify/require"
)

func historyHome(t *testing.T) *home {
	t.Helper()
	spinner := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	return &home{
		ctx:       context.Background(),
		appConfig: &config.Config{ProjectsRoot: "/mnt/c/Projects"},
		list:      ui.NewList(&spinner, false),
		program:   "claude",
	}
}

func TestReconcileHistory(t *testing.T) {
	dirExists = func(string) bool { return true }
	defer func() { dirExists = func(p string) bool { info, err := os.Stat(p); return err == nil && info.IsDir() } }()
	h := historyHome(t)
	h.appConfig.HistoryIgnore = []string{"/AppData/Local/Temp/"}
	now := time.Now()
	entries := []history.Entry{
		{ID: "aaaa", CWD: "/mnt/c/Projects/Fawri/Code/Backend", Title: "fix auth", LastPrompt: "redo the tests", Updated: now},
		{ID: "bbbb", CWD: "/home/me/repo", Title: "docs", Updated: now.Add(-time.Hour)},
		{ID: "cccc", CWD: "/home/me/AppData/Local/Temp/claude/x", Title: "scratch", Updated: now},
	}

	require.True(t, h.reconcileHistory(entries))
	require.Equal(t, 2, h.list.NumInstances(), "the scratch folder is ignored by history_ignore")
	var fawri *session.Instance
	for _, inst := range h.list.GetInstances() {
		require.True(t, inst.Archived)
		require.Equal(t, session.Stopped, inst.Status)
		if inst.ResumeID == "aaaa" {
			fawri = inst
		}
	}
	require.NotNil(t, fawri)
	require.Equal(t, "Fawri", fawri.Project, "projects resolve from the recorded cwd")
	require.Equal(t, "redo the tests", fawri.Summary)

	require.False(t, h.reconcileHistory(entries), "same entries: no change")

	entries[0].Title = "fix auth (renamed)"
	require.False(t, h.reconcileHistory(entries), "a refreshed title is not a structural change")
	require.Equal(t, "fix auth (renamed)", fawri.Title)

	// A running instance that resumed "bbbb" hides its archived row.
	running, err := session.NewInstance(session.InstanceOptions{Title: "docs", Path: t.TempDir(), Program: "claude", ResumeID: "bbbb"})
	require.NoError(t, err)
	h.list.AddInstance(running)()
	require.True(t, h.reconcileHistory(entries))
	for _, inst := range h.list.GetInstances() {
		require.False(t, inst.Archived && inst.ResumeID == "bbbb", "archived copy of a running session must go")
	}

	require.True(t, h.reconcileHistory(nil), "everything vanished")
	require.Equal(t, 1, h.list.NumInstances(), "only the running instance remains")
}

// A rescan request wakes the one pending scan; it never starts a second scan chain.
func TestRequestScanWakesThePendingScan(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	h := historyHome(t)
	h.requestScan() // nothing pending yet: a no-op

	cmd := h.scanHistoryCmd(time.Hour)
	done := make(chan struct{})
	go func() { cmd(); close(done) }()
	h.requestScan()
	h.requestScan() // a second request while one is pending is dropped, not blocking
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the pending scan did not wake up")
	}
}

// A running session renamed in Claude (/rename) shows the new name: from the scan, and
// sooner from the watcher, which follows its transcript.
func TestRunningSessionTakesItsClaudeName(t *testing.T) {
	dirExists = func(string) bool { return true }
	defer func() { dirExists = func(p string) bool { info, err := os.Stat(p); return err == nil && info.IsDir() } }()
	h := historyHome(t)
	running, err := session.NewInstance(session.InstanceOptions{Title: "resumed", Path: t.TempDir(), Program: "claude", ResumeID: "bbbb"})
	require.NoError(t, err)
	h.list.AddInstance(running)()

	h.reconcileHistory([]history.Entry{{ID: "bbbb", CWD: "/home/me/repo", Title: "first prompt", Path: "/p/bbbb.jsonl", Updated: time.Now()}})
	require.Equal(t, "resumed", running.DisplayTitle(), "no /rename yet")
	require.Equal(t, map[string]string{"bbbb": "/p/bbbb.jsonl"}, h.transcripts, "its transcript is known")
	require.NotNil(t, h.watchTitlesCmd())

	h.applyTitles(map[string]string{"/p/bbbb.jsonl": "CMI BackOffice", "/p/other.jsonl": "x"})
	require.Equal(t, "CMI BackOffice", running.DisplayTitle())
	require.Equal(t, "resumed", running.Title, "the key does not change")

	h.reconcileHistory([]history.Entry{{ID: "bbbb", CWD: "/home/me/repo", Title: "CMI v2", CustomTitle: "CMI v2", Path: "/p/bbbb.jsonl", Updated: time.Now()}})
	require.Equal(t, "CMI v2", running.DisplayTitle(), "the scan applies the latest rename too")
}

func TestReconcileHistorySkipsMissingDirectories(t *testing.T) {
	h := historyHome(t)
	require.False(t, h.reconcileHistory([]history.Entry{{ID: "gone", CWD: "/definitely/not/here", Title: "x", Updated: time.Now()}}))
	require.Zero(t, h.list.NumInstances())
}

func TestReconcileHistoryIgnoresSessionsReportedByHooks(t *testing.T) {
	dirExists = func(string) bool { return true }
	defer func() { dirExists = func(p string) bool { info, err := os.Stat(p); return err == nil && info.IsDir() } }()
	h := historyHome(t)
	adopted := session.NewAdoptedInstance(tmux.Pane{Session: "scratch", ID: "%1", Command: "claude", Path: "/home/me/repo"})
	adopted.ClaudeSessionID = "live-id"
	h.list.AddInstance(adopted)()

	h.reconcileHistory([]history.Entry{{ID: "live-id", CWD: "/home/me/repo", Title: "same session", Updated: time.Now()}})
	require.Equal(t, 1, h.list.NumInstances(), "the transcript of a live session is not listed twice")
}

func TestResumeArchivedReplacesTheRow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	h := historyHome(t)
	h.appConfig.MaxInstances = 5
	h.menu = ui.NewMenu()
	h.tabbedWindow = ui.NewTabbedWindow(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane())
	h.errBox = ui.NewErrBox()
	h.storage = newTestStorage(t)

	other, err := session.NewInstance(session.InstanceOptions{Title: "CMIBills CI-CD", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	h.list.AddInstance(other)()
	archived := session.NewArchivedInstance("abcd", "CMIBills CI/CD: pipeline.v2", t.TempDir(), "last", time.Now())
	archived.Project = "CMIBills"
	h.list.AddInstance(archived)()

	cmd := h.resumeArchived(archived)
	require.NotNil(t, cmd)

	var fresh *session.Instance
	for _, inst := range h.list.GetInstances() {
		require.False(t, inst.Archived, "the archived row is gone")
		if inst.ResumeID == "abcd" {
			fresh = inst
		}
	}
	require.NotNil(t, fresh)
	require.True(t, fresh.InPlace, "resumed sessions run in place")
	require.Equal(t, "CMIBills CI/CD- pipeline-v2", fresh.Title, "characters tmux rejects are replaced")
	require.Equal(t, "CMIBills CI/CD: pipeline.v2", fresh.DisplayTitle(), "the row keeps its name")
	require.Equal(t, "CMIBills", fresh.Project)
	require.Equal(t, session.Loading, fresh.Status)
	require.Same(t, fresh, h.list.GetSelectedInstance())
}

func TestSessionTitleAndUniqueTitle(t *testing.T) {
	require.Equal(t, "resumed", sessionTitle("   "))
	require.Equal(t, strings.Repeat("a", 32), sessionTitle(strings.Repeat("a", 40)))
	require.Equal(t, "a-b-c", sessionTitle("a:b.c"))

	h := historyHome(t)
	for _, title := range []string{"docs", "docs 2"} {
		inst, _ := session.NewInstance(session.InstanceOptions{Title: title, Path: ".", Program: "x"})
		h.list.AddInstance(inst)
	}
	require.Equal(t, "docs 3", h.uniqueTitle("docs"))
	require.Equal(t, "other", h.uniqueTitle("other"))
}

func TestArchivedCard(t *testing.T) {
	inst := session.NewArchivedInstance("id-1", "fix auth", "/mnt/c/Projects/Fawri/Code/Backend",
		strings.Repeat("word ", 30), time.Date(2026, 10, 15, 9, 30, 0, 0, time.Local))
	inst.Project = "Fawri"
	card := archivedCard(inst)
	require.Contains(t, card, "fix auth")
	require.Contains(t, card, "Project    Fawri")
	require.Contains(t, card, "2026-10-15 09:30")
	require.Contains(t, card, "Press Enter to resume this session here (Backend)")
	for _, line := range strings.Split(card, "\n") {
		require.LessOrEqual(t, len(line), 80, "card lines stay narrow: %q", line)
	}
}

func TestHistoryIgnore(t *testing.T) {
	cfg := &config.Config{}
	require.False(t, cfg.HistoryIgnored("/mnt/c/Users/me/AppData/Local/Temp/claude/x"), "nothing is hidden by default")

	cfg.HistoryIgnore = []string{"/AppData/Local/Temp/", "/archive/"}
	require.True(t, cfg.HistoryIgnored("/mnt/c/Users/me/AppData/Local/Temp/claude/x"))
	require.True(t, cfg.HistoryIgnored("/mnt/c/Users/me/AppData/Local/Temp"), "the folder itself, without a trailing slash")
	require.True(t, cfg.HistoryIgnored("/srv/archive/old"))
	require.False(t, cfg.HistoryIgnored("/mnt/c/Projects/Fawri"))
}

// A running session without hooks (the Windows claude.exe) claims the transcript written in
// its directory since it started, so that transcript is not listed again as a past session.
func TestRunningSessionClaimsItsTranscript(t *testing.T) {
	dirExists = func(string) bool { return true }
	defer func() { dirExists = func(p string) bool { info, err := os.Stat(p); return err == nil && info.IsDir() } }()
	h := historyHome(t)
	dir := t.TempDir()
	adopted := session.NewAdoptedInstance(tmux.Pane{Session: "win", ID: "%3", Command: "init", Path: dir})
	h.list.AddInstance(adopted)()

	now := time.Now()
	h.reconcileHistory([]history.Entry{
		{ID: "older", CWD: dir, Title: "older", Updated: now.Add(-time.Hour)},
		{ID: "mine", CWD: dir, Title: "mine", Updated: now},
		{ID: "elsewhere", CWD: "/other", Title: "x", Updated: now},
	})
	require.Equal(t, "mine", adopted.ClaudeSessionID, "the newest transcript in its directory")
	var past []string
	for _, inst := range h.list.GetInstances() {
		if inst.Archived {
			past = append(past, inst.ResumeID)
		}
	}
	require.ElementsMatch(t, []string{"older", "elsewhere"}, past, "its own transcript is not listed twice")
}
