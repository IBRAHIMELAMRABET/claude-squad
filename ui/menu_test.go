package ui

import (
	"claude-squad/session"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/require"
)

// The sidebar menu wraps its items so every line fits the narrow pane.
func TestCompactMenuWrapsToWidth(t *testing.T) {
	m := NewMenu()
	m.SetSidebar(true)
	m.SetSize(36, SidebarMenuHeight)
	inst, _ := session.NewInstance(session.InstanceOptions{Title: "x", Path: ".", Program: "claude"})
	m.SetInstance(inst)

	out := m.String()
	lines := strings.Split(out, "\n")
	require.Greater(t, len(lines), 1, "wraps onto several lines")
	require.LessOrEqual(t, len(lines), SidebarMenuHeight, "never taller than the menu area")
	require.Contains(t, out, "quit", "every option fits in the reserved lines")
	for _, l := range lines {
		require.LessOrEqual(t, lipgloss.Width(l), 36, "line too wide: %q", l)
	}
	require.Contains(t, out, "focus")
	require.Contains(t, out, "diff")
	require.Contains(t, out, "alt+s hide list", "the sidebar toggle is shown")
	require.NotContains(t, out, "switch tab", "classic labels are not used in the sidebar")
}

func TestSidebarMenuHidesManagedOnlyActionsForAdopted(t *testing.T) {
	m := NewMenu()
	m.SetSidebar(true)
	m.SetSize(80, 4)
	inst, _ := session.NewInstance(session.InstanceOptions{Title: "x", Path: ".", Program: "claude"})
	inst.Adopted = true
	m.SetInstance(inst)

	out := m.String()
	require.NotContains(t, out, "kill")
	require.NotContains(t, out, "push")
	require.Contains(t, out, "focus")
}

func TestSidebarNamingMenuShowsRepoAndMode(t *testing.T) {
	m := NewMenu()
	m.SetSidebar(true)
	m.SetSize(36, SidebarMenuHeight)
	inst, _ := session.NewInstance(session.InstanceOptions{Title: "", Path: "/repos/Backend", Program: "claude", InPlace: true})
	m.SetInstance(inst)
	m.SetState(StateNewInstance)

	out := m.String()
	require.Contains(t, out, "repos/Backend")
	require.Contains(t, out, "in-place")
	require.Contains(t, out, "folder", "the left/right hint")
	for _, l := range strings.Split(out, "\n") {
		require.LessOrEqual(t, lipgloss.Width(l), 36, "line too wide: %q", l)
	}
}

// The folder is named by its last three segments, so a WSL copy and a Windows copy of the
// same repository look different, and a long name is cut from the left.
func TestFolderLabel(t *testing.T) {
	require.Equal(t, "home/alex/Orbit_Infra", folderLabel("/home/alex/Orbit_Infra", 40))
	require.Equal(t, "Orbit/Code/Payments", folderLabel("/mnt/c/Users/me/Projects/Orbit/Code/Payments", 40))
	require.Equal(t, "…ode/Payments", folderLabel("/mnt/c/Users/me/Projects/Orbit/Code/Payments", 13))
	require.Equal(t, "me/Projects/Atelier", folderLabel(`C:\Users\me\Projects\Atelier`, 40), "Windows separators too")
}

// While naming, the list shows the name being typed, cut from the left when long.
func TestNamingMenuShowsTheName(t *testing.T) {
	m := NewMenu()
	m.SetSidebar(true)
	m.SetSize(31, SidebarMenuHeight)
	inst, _ := session.NewInstance(session.InstanceOptions{Title: "release notes for the api", Path: "/repos/Backend", Program: "claude"})
	m.SetInstance(inst)
	m.SetState(StateNewInstance)
	out := m.String()
	require.Contains(t, out, "name")
	require.Contains(t, out, "for the api▏")
	for _, l := range strings.Split(out, "\n") {
		require.LessOrEqual(t, lipgloss.Width(l), 31, "line too wide: %q", l)
	}
}

// While a folder is typed, the list shows it (cut from the left) and how to finish.
func TestNamingMenuShowsTheTypedFolder(t *testing.T) {
	m := NewMenu()
	m.SetSidebar(true)
	m.SetSize(31, SidebarMenuHeight)
	inst, _ := session.NewInstance(session.InstanceOptions{Title: "api", Path: "/repos/Backend", Program: "claude"})
	m.SetInstance(inst)
	m.SetState(StateNewInstance)
	m.SetFolderInput("/mnt/c/Users/me/Documents/Projects/brand-new", "", true)
	out := m.String()
	require.Contains(t, out, "folder")
	require.Contains(t, out, "Projects/brand-new▏")
	require.Contains(t, out, "tab complete ↵ use esc back", "the whole hint fits")
	for _, l := range strings.Split(out, "\n") {
		require.LessOrEqual(t, lipgloss.Width(l), 31, "line too wide: %q", l)
	}
	m.SetFolderInput("", "", false)
	require.Contains(t, m.String(), "ctrl+o", "the key is shown while naming")
}
