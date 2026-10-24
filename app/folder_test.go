package app

import (
	"claude-squad/config"
	"claude-squad/ui"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
)

func TestResolveFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.Equal(t, filepath.Join(home, "work"), resolveFolder(" ~/work/ "))
	require.Equal(t, "/srv/x", resolveFolder("/srv//x/"))
	require.Equal(t, "", resolveFolder("  "))
	if _, err := os.Stat("/mnt/c"); err == nil {
		require.Equal(t, "/mnt/c/Users/me/new", resolveFolder(`C:\Users\me\new`), "Windows paths map to the WSL mount")
	}
}

func TestCompleteFolder(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"alpha", "alps", "Beta", ".hidden"} {
		require.NoError(t, os.Mkdir(filepath.Join(dir, d), 0755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "beta-file"), nil, 0644))

	require.Equal(t, filepath.Join(dir, "alp"), completeFolder(filepath.Join(dir, "al")), "the start the matches share")
	require.Equal(t, filepath.Join(dir, "Beta")+"/", completeFolder(filepath.Join(dir, "be")), "one folder, case aside; files are not offered")
	require.Equal(t, filepath.Join(dir, ".hidden")+"/", completeFolder(filepath.Join(dir, ".h")), "hidden folders once a dot is typed")
	require.Equal(t, filepath.Join(dir, "zz"), completeFolder(filepath.Join(dir, "zz")), "nothing matches: unchanged")
	require.Equal(t, dir+"/", completeFolder(dir+"/"), "several matches with nothing in common: unchanged")
}

// ctrl+o while naming types any folder; one that does not exist is created after a second
// enter, and the session runs there in place.
func TestTypingAFolderForANewSession(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	spinner := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	h := &home{
		ctx:          context.Background(),
		state:        stateDefault,
		appConfig:    &config.Config{DefaultMode: config.ModeWorktree, ProjectsRoot: t.TempDir()},
		appState:     config.DefaultState(),
		list:         ui.NewList(&spinner, false),
		menu:         ui.NewMenu(),
		tabbedWindow: ui.NewTabbedWindow(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		errBox:       ui.NewErrBox(),
	}
	press := func(msg tea.KeyMsg) { h.keySent = true; h.handleKeyPress(msg) }
	typeText := func(s string) { press(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}) }

	typeText("n")
	require.Equal(t, stateNew, h.state)
	inst := h.list.GetInstances()[0]
	typeText("api")

	press(tea.KeyMsg{Type: tea.KeyCtrlO})
	require.NotNil(t, h.folder)
	target := filepath.Join(t.TempDir(), "brand", "new")
	press(tea.KeyMsg{Type: tea.KeyCtrlU})
	typeText(target)
	press(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotNil(t, h.folder, "a missing folder asks for a second enter")
	require.NoDirExists(t, target)
	press(tea.KeyMsg{Type: tea.KeyEnter})
	require.Nil(t, h.folder)
	require.DirExists(t, target, "created")
	require.Equal(t, target, inst.Path)
	require.True(t, inst.InPlace, "a new folder is no repository: in place")
	require.Equal(t, "api", inst.Title, "the name typed before is kept")
	require.Equal(t, target, h.repoChoices[0])

	press(tea.KeyMsg{Type: tea.KeyCtrlO})
	typeText("zzz")
	press(tea.KeyMsg{Type: tea.KeyEsc})
	require.Nil(t, h.folder, "esc goes back to the name")
	require.Equal(t, target, inst.Path, "and changes nothing")
	require.Equal(t, stateNew, h.state)
}
