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

// historyScanMsg carries the past sessions found in the transcript store.
type historyScanMsg struct {
	entries []history.Entry
}

// scanHistoryCmd lists past sessions in the background. The first scan runs at once, later
// ones after historyScanInterval. It is a no-op when history is disabled.
func (m *home) scanHistoryCmd(delay time.Duration) tea.Cmd {
	if m.appConfig.DisableHistory {
		return nil
	}
	limit, maxAge := m.appConfig.GetHistoryLimit(), m.appConfig.GetHistoryMaxAge()
	return func() tea.Msg {
		time.Sleep(delay)
		dir, err := history.ProjectsDir()
		if err != nil {
			log.WarningLog.Printf("history disabled: %v", err)
			return nil
		}
		entries, err := history.Scan(dir, limit, maxAge)
		if err != nil {
			log.WarningLog.Printf("could not scan past sessions: %v", err)
			return nil
		}
		return historyScanMsg{entries: entries}
	}
}

// reconcileHistory merges the scanned past sessions into the list: entries already
// represented by a running session (resumed, or reporting that id through hooks) and
// entries under ignored directories are left out; vanished ones are removed; existing rows
// are refreshed. It reports whether the list changed.
func (m *home) reconcileHistory(entries []history.Entry) bool {
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
	}

	wanted := make(map[string]history.Entry, len(entries))
	for _, e := range entries {
		cwd := config.NormalizePath(e.CWD)
		if live[e.ID] || m.appConfig.HistoryIgnored(cwd) || !dirExists(cwd) {
			continue // a session cannot be resumed where its directory is gone
		}
		e.CWD = cwd
		wanted[e.ID] = e
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
			continue
		}
		inst := session.NewArchivedInstance(e.ID, e.Title, e.CWD, e.LastPrompt, e.Updated)
		// Group by the repository the session worked in, resolving worktrees to their main
		// repository; directories outside any repository group by themselves.
		groupPath := e.CWD
		if root, err := git.MainRepoRoot(e.CWD); err == nil {
			groupPath = root
		}
		inst.Project = m.appConfig.ProjectFor(groupPath)
		inst.GroupPath = groupPath
		m.list.AddInstance(inst)()
		changed = true
	}
	return changed
}

// dirExists reports whether path is an existing directory. Overridable in tests.
var dirExists = func(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
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
	fresh, err := session.NewInstance(session.InstanceOptions{
		Title:    m.uniqueTitle(sessionTitle(archived.Title)),
		Path:     archived.Path,
		Program:  m.program,
		ResumeID: archived.ResumeID,
	})
	if err != nil {
		return m.handleError(err)
	}
	fresh.Project = archived.Project
	fresh.Summary = archived.Summary

	m.list.Remove(archived)
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
