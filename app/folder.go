package app

import (
	"claude-squad/config"
	"claude-squad/session"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// folderInput is the folder typed for a new session (ctrl+o while naming it): any path, in
// Windows or Linux form, completed with tab. A folder that does not exist is created after
// a second enter.
type folderInput struct {
	text string
	// confirm is the missing folder the next enter creates.
	confirm string
}

// resolveFolder turns a typed folder into a path on this side: ~ expanded, Windows and
// \\wsl.localhost paths mapped, cleaned. It returns "" when nothing usable was typed.
func resolveFolder(text string) string {
	p := strings.TrimSpace(text)
	if p == "" {
		return ""
	}
	if expanded, err := config.ExpandHome(p); err == nil {
		p = expanded
	}
	return filepath.Clean(config.NormalizePath(p))
}

// completeFolder completes the last element of a typed folder from the folders on disk:
// the only match followed by a slash, or else the longest start the matches share. Hidden
// folders are offered only once a dot is typed. The text is returned unchanged when
// nothing matches.
func completeFolder(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return text
	}
	dir, prefix := resolveFolder(trimmed), ""
	if !strings.HasSuffix(trimmed, "/") && !strings.HasSuffix(trimmed, `\`) {
		dir, prefix = filepath.Dir(dir), filepath.Base(dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return text
	}
	var matches []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || (strings.HasPrefix(name, ".") && !strings.HasPrefix(prefix, ".")) {
			continue
		}
		if strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix)) {
			matches = append(matches, name)
		}
	}
	switch len(matches) {
	case 0:
		return text
	case 1:
		return filepath.Join(dir, matches[0]) + "/"
	}
	common := matches[0]
	for _, m := range matches[1:] {
		for !strings.HasPrefix(strings.ToLower(m), strings.ToLower(common)) {
			common = common[:len(common)-1]
		}
	}
	if len(common) <= len(prefix) {
		return text
	}
	return filepath.Join(dir, common)
}

// startFolderInput lets the user type the folder of the session being named, starting
// from its current folder.
func (m *home) startFolderInput(inst *session.Instance) {
	m.folder = &folderInput{text: strings.TrimSuffix(inst.Path, "/") + "/"}
	m.menu.SetFolderInput(m.folder.text, "", true)
}

// handleFolderKey edits the typed folder. Enter uses it (creating it after a second enter
// when it does not exist), esc goes back to the name.
func (m *home) handleFolderKey(msg tea.KeyMsg, inst *session.Instance) tea.Cmd {
	f := m.folder
	switch msg.Type {
	case tea.KeyEsc:
		m.folder = nil
		m.menu.SetFolderInput("", "", false)
		return nil
	case tea.KeyTab:
		f.text = completeFolder(f.text)
	case tea.KeyBackspace:
		if r := []rune(f.text); len(r) > 0 {
			f.text = string(r[:len(r)-1])
		}
	case tea.KeyCtrlU:
		f.text = ""
	case tea.KeySpace:
		f.text += " "
	case tea.KeyRunes:
		f.text += string(msg.Runes)
	case tea.KeyEnter:
		path := resolveFolder(f.text)
		if !filepath.IsAbs(path) {
			return m.handleError(fmt.Errorf("type a full path: C:\\work\\new or ~/new"))
		}
		if !dirExists(path) {
			if f.confirm != path {
				f.confirm = path
				m.menu.SetFolderInput(f.text, "new folder: ↵ again creates", true)
				return nil
			}
			if err := os.MkdirAll(path, 0755); err != nil {
				return m.handleError(err)
			}
		}
		m.folder = nil
		m.menu.SetFolderInput("", "", false)
		m.repoChoices, m.repoIdx = withFirst(path, m.repoChoices), 0
		inst.Path = path
		inst.Project = m.appConfig.GroupFor(path)
		inst.InPlace = m.newInPlace || !isRepo(path)
		return m.instanceChanged()
	default:
		return nil
	}
	f.confirm = ""
	m.menu.SetFolderInput(f.text, "", true)
	return nil
}
