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
	require.Equal(t, "home/brahim/Fawri_Infra", folderLabel("/home/brahim/Fawri_Infra", 40))
	require.Equal(t, "Fawri/Code/Wafacash", folderLabel("/mnt/c/Users/me/Projects/Fawri/Code/Wafacash", 40))
	require.Equal(t, "…ode/Wafacash", folderLabel("/mnt/c/Users/me/Projects/Fawri/Code/Wafacash", 13))
	require.Equal(t, "me/Projects/AIStudio", folderLabel(`C:\Users\me\Projects\AIStudio`, 40), "Windows separators too")
}
