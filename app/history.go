package app

import (
	"claude-squad/config"
	"claude-squad/log"
	"claude-squad/session"
	"claude-squad/session/git"
	"claude-squad/session/history"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

// historyScanInterval is how often Claude's transcript store is rescanned.
const historyScanInterval = 60 * time.Second

// historyScanMsg carries the past sessions found in the transcript store, and the
// repository roots of directories seen for the first time.
type historyScanMsg struct {
	entries []history.Entry
	roots   map[string]string
}

// scanHistoryCmd lists past sessions in the background. The first scan runs at once, later
// ones after historyScanInterval. It is a no-op when history is disabled.
func (m *home) scanHistoryCmd(delay time.Duration) tea.Cmd {
	if m.appConfig.DisableHistory {
		return nil
	}
	limit, maxAge := m.appConfig.GetHistoryLimit(), m.appConfig.GetHistoryMaxAge()
	if m.historyScanner == nil {
		m.historyScanner = history.NewScanner()
	}
	// The scanner is only used by this command, and scans never overlap: the next one is
	// scheduled when this one's result arrives. Starting another chain would break that.
	scanner := m.historyScanner
	known := make(map[string]bool, len(m.repoRoots))
	for dir := range m.repoRoots {
		known[dir] = true
	}
	return func() tea.Msg {
		time.Sleep(delay)
		dir, err := history.ProjectsDir()
		if err != nil {
			log.WarningLog.Printf("history disabled: %v", err)
			return nil
		}
		entries, err := scanner.Scan(dir, limit, maxAge)
		if err != nil {
			log.WarningLog.Printf("could not scan past sessions: %v", err)
			return nil
		}
		// Resolve each new directory to its repository once, here rather than on the UI
		// thread: git is slow on Windows drives.
		roots := make(map[string]string)
		for _, e := range entries {
			cwd := config.NormalizePath(e.CWD)
			if known[cwd] {
				continue
			}
			known[cwd] = true
			if root, err := git.MainRepoRoot(cwd); err == nil {
				roots[cwd] = root
			} else {
				roots[cwd] = ""
			}
		}
		return historyScanMsg{entries: entries, roots: roots}
	}
}

// titleWatchInterval is how often the transcripts of running sessions are checked for a
// new name (/rename). Only what was appended is read.
const titleWatchInterval = 2 * time.Second

// titleWatchMsg carries the names set since the previous check, by transcript path.
type titleWatchMsg struct {
	titles map[string]string
}

// watchTitlesCmd checks the transcripts of running sessions for /rename after
// titleWatchInterval. Checks never overlap: the next one is scheduled when this one's
// result arrives.
func (m *home) watchTitlesCmd() tea.Cmd {
	if m.appConfig.DisableHistory {
		return nil
	}
	if m.titleWatcher == nil {
		m.titleWatcher = history.NewTitleWatcher()
	}
	watcher := m.titleWatcher
	var paths []string
	for _, inst := range m.list.GetInstances() {
		if path, ok := m.transcripts[claudeID(inst)]; ok && !inst.Archived {
			paths = append(paths, path)
		}
	}
	return func() tea.Msg {
		time.Sleep(titleWatchInterval)
		return titleWatchMsg{titles: watcher.Check(paths)}
	}
}

// applyTitles names running sessions after the titles found in their transcripts.
func (m *home) applyTitles(titles map[string]string) {
	if len(titles) == 0 {
		return
	}
	for _, inst := range m.list.GetInstances() {
		if inst.Archived {
			continue
		}
		if title, ok := titles[m.transcripts[claudeID(inst)]]; ok {
			inst.Label = history.Shorten(title, 48)
		}
	}
}

// claudeID is the id of the Claude session a running session writes its transcript as:
// the one hooks reported, else the one it resumed.
func claudeID(inst *session.Instance) string {
	if inst.ClaudeSessionID != "" {
		return inst.ClaudeSessionID
	}
	return inst.ResumeID
}

// reconcileHistory merges the scanned past sessions into the list: entries already
// represented by a running session (resumed, or reporting that id through hooks) and
// entries under ignored directories are left out; vanished ones are removed; existing rows
// are refreshed. Running sessions take the name set with /rename in their transcript. It
// reports whether the list changed.
func (m *home) reconcileHistory(entries []history.Entry) bool {
	m.claimTranscripts(entries)
	byID := make(map[string]history.Entry, len(entries))
	m.transcripts = make(map[string]string, len(entries))
	for _, e := range entries {
		byID[e.ID] = e
		m.transcripts[e.ID] = e.Path
	}
	live := make(map[string]bool)
	for _, inst := range m.list.GetInstances() {
		if inst.Archived {
			continue
		}
		if inst.ResumeID != "" {
			live[inst.ResumeID] = true
		}
		if inst.ClaudeSessionID != "" {
			live[inst.ClaudeSessionID] = true
		}
		if e, ok := byID[claudeID(inst)]; ok && e.CustomTitle != "" {
			inst.Label = history.Shorten(e.CustomTitle, 48)
		}
	}

	wanted := make(map[string]history.Entry, len(entries))
	for _, e := range entries {
		cwd := config.NormalizePath(e.CWD)
		if live[e.ID] || m.appConfig.HistoryIgnored(cwd) || !dirExists(cwd) ||
			(!m.appConfig.ShowScratchpadSessions && config.IsScratchpad(cwd)) ||
			(!m.appConfig.ShowHeadlessSessions && e.Headless) {
			continue // a session cannot be resumed where its directory is gone
		}
		e.CWD = cwd
		wanted[e.ID] = e
	}

	hidden := make(map[string]bool)
	if m.appState != nil {
		for _, id := range m.appState.GetUIPrefs().HiddenSessions {
			hidden[id] = true
		}
	}

	changed := false
	known := make(map[string]*session.Instance)
	for _, inst := range m.list.GetInstances() {
		if !inst.Archived {
			continue
		}
		if _, ok := wanted[inst.ResumeID]; !ok {
			m.list.Remove(inst)
			changed = true
			continue
		}
		known[inst.ResumeID] = inst
	}
	for id, e := range wanted {
		if inst, ok := known[id]; ok {
			inst.Title, inst.Summary, inst.LastActivity = e.Title, e.LastPrompt, e.Updated
			inst.Hidden = hidden[id]
			continue
		}
		inst := session.NewArchivedInstance(e.ID, e.Title, e.CWD, e.LastPrompt, e.Updated)
		// Group by the repository the session worked in (worktrees resolved to their main
		// repository by the scan); directories outside any repository group by themselves.
		groupPath := e.CWD
		if root := m.repoRoots[e.CWD]; root != "" {
			groupPath = root
		}
		inst.Project = m.appConfig.GroupFor(groupPath)
		inst.GroupPath = groupPath
		inst.Hidden = hidden[e.ID]
		m.list.AddInstance(inst)()
		changed = true
	}
	return changed
}

// errHidePast is shown when x targets something other than a past session.
var errHidePast = fmt.Errorf("x hides past (○) sessions; D deletes one")

// setHidden hides a past session from the list, or brings it back, and remembers it.
func (m *home) setHidden(inst *session.Instance, hidden bool) {
	inst.Hidden = hidden
	prefs := m.appState.GetUIPrefs()
	var ids []string
	for _, id := range prefs.HiddenSessions {
		if id != inst.ResumeID {
			ids = append(ids, id)
		}
	}
	if hidden {
		ids = append(ids, inst.ResumeID)
	}
	prefs.HiddenSessions = ids
	if err := m.appState.SetUIPrefs(prefs); err != nil {
		log.WarningLog.Printf("failed to save hidden sessions: %v", err)
	}
	m.list.Refresh()
}

// dirExists reports whether path is an existing directory. Overridable in tests.
var dirExists = func(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// claimTranscripts links running sessions whose Claude session id is unknown (no hooks, as
// with the Windows claude.exe, or adopted ones) to their transcript, so it is not listed a
// second time as a past session: the newest transcript recorded in the session's directory
// since it started (any time, for adopted sessions) that no other session claims.
func (m *home) claimTranscripts(entries []history.Entry) {
	claimed := make(map[string]bool)
	var unknown []*session.Instance
	for _, inst := range m.list.GetInstances() {
		if inst.Archived {
			continue
		}
		switch {
		case inst.ResumeID != "":
			claimed[inst.ResumeID] = true
		case inst.ClaudeSessionID != "":
			claimed[inst.ClaudeSessionID] = true
		case inst.Started() && !inst.Paused():
			unknown = append(unknown, inst)
		}
	}
	for _, inst := range unknown {
		dir := config.NormalizePath(inst.WorkDir())
		if dir == "" {
			continue
		}
		since := inst.CreatedAt.Add(-time.Minute)
		var best *history.Entry
		for idx := range entries {
			e := &entries[idx]
			if claimed[e.ID] || config.NormalizePath(e.CWD) != dir {
				continue
			}
			if !inst.Adopted && e.Updated.Before(since) {
				continue
			}
			if best == nil || e.Updated.After(best.Updated) {
				best = e
			}
		}
		if best != nil {
			inst.ClaudeSessionID = best.ID
			claimed[best.ID] = true
		}
	}
}

// runningCount is the number of sessions that count against max_instances: everything
// but past sessions, which are only listed.
func (m *home) runningCount() int {
	n := 0
	for _, inst := range m.list.GetInstances() {
		if !inst.Archived {
			n++
		}
	}
	return n
}

// resumeArchived turns a past session into a running one: `claude --resume <id>` in the
// directory it was recorded in. The archived row is replaced by the new instance, which is
// selected and started in the background like a new session.
func (m *home) resumeArchived(archived *session.Instance) tea.Cmd {
	if limit := m.appConfig.GetMaxInstances(); m.runningCount() >= limit {
		return m.handleError(fmt.Errorf("you can't create more than %d instances", limit))
	}
	// The past row goes first, so its title is free for the resumed session.
	m.list.Remove(archived)
	fresh, err := session.NewInstance(session.InstanceOptions{
		Title:    m.uniqueTitle(sessionTitle(archived.Title)),
		Path:     archived.Path,
		Program:  m.program,
		ResumeID: archived.ResumeID,
	})
	if err != nil {
		m.list.AddInstance(archived)()
		return m.handleError(err)
	}
	fresh.Project = archived.Project
	fresh.Summary = archived.Summary
	// Keep showing the name of the row it replaces; Title may have been cut or rewritten.
	fresh.Label = archived.Title

	m.newInstanceFinalizer = m.list.AddInstance(fresh)
	fresh.SetStatus(session.Loading)
	m.newInstanceFinalizer()
	m.list.SelectInstance(fresh)

	startCmd := func() tea.Msg {
		err := fresh.Start(true)
		return instanceStartedMsg{instance: fresh, err: err}
	}
	return tea.Batch(tea.WindowSize(), m.instanceChanged(), startCmd)
}

// sessionTitle makes a transcript title usable as an instance title: no characters tmux
// rejects in session names, at most 32 runes.
func sessionTitle(title string) string {
	title = strings.NewReplacer(":", "-", ".", "-").Replace(strings.TrimSpace(title))
	if title == "" {
		title = "resumed"
	}
	if utf8.RuneCountInString(title) > 32 {
		title = strings.TrimSpace(string([]rune(title)[:32]))
	}
	return title
}

// uniqueTitle appends a counter when another instance already uses the title.
func (m *home) uniqueTitle(title string) string {
	taken := make(map[string]bool)
	for _, inst := range m.list.GetInstances() {
		taken[inst.Title] = true
	}
	candidate := title
	for n := 2; taken[candidate]; n++ {
		candidate = fmt.Sprintf("%s %d", title, n)
	}
	return candidate
}

// archivedCard is the text shown in the session pane for a past session.
func archivedCard(inst *session.Instance) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n  %s\n\n", inst.Title)
	fmt.Fprintf(&b, "  Project    %s\n", displayProject(inst))
	fmt.Fprintf(&b, "  Directory  %s\n", inst.Path)
	if !inst.LastActivity.IsZero() {
		fmt.Fprintf(&b, "  Last used  %s\n", inst.LastActivity.Local().Format("2006-01-02 15:04"))
	}
	fmt.Fprintf(&b, "  Session    %s\n", inst.ResumeID)
	if inst.Summary != "" {
		fmt.Fprintf(&b, "\n  Last prompt\n  %s\n", wrap(inst.Summary, 70, "  "))
	}
	fmt.Fprintf(&b, "\n  Press Enter to resume this session here (%s).\n", filepath.Base(inst.Path))
	return b.String()
}

// newSessionCard is shown in the session pane while a new session is being named.
func newSessionCard(inst *session.Instance) string {
	mode := "worktree: its own branch and folder"
	if inst.InPlace {
		mode = "in place: runs in the folder itself"
	}
	var b strings.Builder
	b.WriteString("\n  New session\n\n")
	fmt.Fprintf(&b, "  Folder  %s\n", inst.Path)
	fmt.Fprintf(&b, "  Mode    %s\n\n", mode)
	b.WriteString("  Type its name in the list, then press Enter.\n")
	b.WriteString("  ←/→ another folder · tab in place or worktree · esc cancel\n\n")
	b.WriteString("  In a folder Claude has not opened before, it first asks whether to\n")
	b.WriteString("  trust it, with No preselected: pick \"Yes, I trust this folder\".\n")
	return b.String()
}

// displayProject names the group a session is listed under.
func displayProject(inst *session.Instance) string {
	if inst.Project != "" {
		return inst.Project
	}
	if g := config.FolderGroupName(inst.ProjectPath(), 1); g != "" {
		return g
	}
	return config.OtherProject
}

// wrap breaks s into lines of at most width runes, indenting continuation lines.
func wrap(s string, width int, indent string) string {
	words := strings.Fields(s)
	var lines []string
	line := ""
	for _, w := range words {
		if line != "" && utf8.RuneCountInString(line)+1+utf8.RuneCountInString(w) > width {
			lines = append(lines, line)
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += w
	}
	if line != "" {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n"+indent)
}
