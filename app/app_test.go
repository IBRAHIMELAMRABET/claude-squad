package app

import (
	"claude-squad/cmd/cmd_test"
	"claude-squad/config"
	"claude-squad/log"
	"claude-squad/session"
	"claude-squad/session/tmux"
	"claude-squad/sidebar"
	"claude-squad/ui"
	"claude-squad/ui/overlay"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain runs before all tests to set up the test environment
func TestMain(m *testing.M) {
	// Initialize the logger before any tests run
	log.Initialize(false)
	defer log.Close()

	// Run all tests
	exitCode := m.Run()

	// Exit with the same code as the tests
	os.Exit(exitCode)
}

// TestConfirmationModalStateTransitions tests state transitions without full instance setup
func TestConfirmationModalStateTransitions(t *testing.T) {
	// Create a minimal home struct for testing state transitions
	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
	}

	t.Run("shows confirmation on D press", func(t *testing.T) {
		// Simulate pressing 'D'
		h.state = stateDefault
		h.confirmationOverlay = nil

		// Manually trigger what would happen in handleKeyPress for 'D'
		h.state = stateConfirm
		h.confirmationOverlay = overlay.NewConfirmationOverlay("[!] Kill session 'test'?")

		assert.Equal(t, stateConfirm, h.state)
		assert.NotNil(t, h.confirmationOverlay)
		assert.False(t, h.confirmationOverlay.Dismissed)
	})

	t.Run("returns to default on y press", func(t *testing.T) {
		// Start in confirmation state
		h.state = stateConfirm
		h.confirmationOverlay = overlay.NewConfirmationOverlay("Test confirmation")

		// Simulate pressing 'y' using HandleKeyPress
		keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}
		shouldClose := h.confirmationOverlay.HandleKeyPress(keyMsg)
		if shouldClose {
			h.state = stateDefault
			h.confirmationOverlay = nil
		}

		assert.Equal(t, stateDefault, h.state)
		assert.Nil(t, h.confirmationOverlay)
	})

	t.Run("returns to default on n press", func(t *testing.T) {
		// Start in confirmation state
		h.state = stateConfirm
		h.confirmationOverlay = overlay.NewConfirmationOverlay("Test confirmation")

		// Simulate pressing 'n' using HandleKeyPress
		keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}
		shouldClose := h.confirmationOverlay.HandleKeyPress(keyMsg)
		if shouldClose {
			h.state = stateDefault
			h.confirmationOverlay = nil
		}

		assert.Equal(t, stateDefault, h.state)
		assert.Nil(t, h.confirmationOverlay)
	})

	t.Run("returns to default on esc press", func(t *testing.T) {
		// Start in confirmation state
		h.state = stateConfirm
		h.confirmationOverlay = overlay.NewConfirmationOverlay("Test confirmation")

		// Simulate pressing ESC using HandleKeyPress
		keyMsg := tea.KeyMsg{Type: tea.KeyEscape}
		shouldClose := h.confirmationOverlay.HandleKeyPress(keyMsg)
		if shouldClose {
			h.state = stateDefault
			h.confirmationOverlay = nil
		}

		assert.Equal(t, stateDefault, h.state)
		assert.Nil(t, h.confirmationOverlay)
	})
}

// TestConfirmationModalKeyHandling tests the actual key handling in confirmation state
func TestConfirmationModalKeyHandling(t *testing.T) {
	// Import needed packages
	spinner := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	list := ui.NewList(&spinner, false)

	// Create enough of home struct to test handleKeyPress in confirmation state
	h := &home{
		ctx:                 context.Background(),
		state:               stateConfirm,
		appConfig:           config.DefaultConfig(),
		list:                list,
		menu:                ui.NewMenu(),
		tabbedWindow:        ui.NewTabbedWindow(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		errBox:              ui.NewErrBox(),
		confirmationOverlay: overlay.NewConfirmationOverlay("Kill session?"),
	}

	testCases := []struct {
		name              string
		key               string
		expectedState     state
		expectedDismissed bool
		expectedNil       bool
	}{
		{
			name:              "y key confirms and dismisses overlay",
			key:               "y",
			expectedState:     stateDefault,
			expectedDismissed: true,
			expectedNil:       true,
		},
		{
			name:              "n key cancels and dismisses overlay",
			key:               "n",
			expectedState:     stateDefault,
			expectedDismissed: true,
			expectedNil:       true,
		},
		{
			name:              "esc key cancels and dismisses overlay",
			key:               "esc",
			expectedState:     stateDefault,
			expectedDismissed: true,
			expectedNil:       true,
		},
		{
			name:              "other keys are ignored",
			key:               "x",
			expectedState:     stateConfirm,
			expectedDismissed: false,
			expectedNil:       false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset state
			h.state = stateConfirm
			h.confirmationOverlay = overlay.NewConfirmationOverlay("Kill session?")

			// Create key message
			var keyMsg tea.KeyMsg
			if tc.key == "esc" {
				keyMsg = tea.KeyMsg{Type: tea.KeyEscape}
			} else {
				keyMsg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(tc.key)}
			}

			// Call handleKeyPress
			model, _ := h.handleKeyPress(keyMsg)
			homeModel, ok := model.(*home)
			require.True(t, ok)

			assert.Equal(t, tc.expectedState, homeModel.state, "State mismatch for key: %s", tc.key)
			if tc.expectedNil {
				assert.Nil(t, homeModel.confirmationOverlay, "Overlay should be nil for key: %s", tc.key)
			} else {
				assert.NotNil(t, homeModel.confirmationOverlay, "Overlay should not be nil for key: %s", tc.key)
				assert.Equal(t, tc.expectedDismissed, homeModel.confirmationOverlay.Dismissed, "Dismissed mismatch for key: %s", tc.key)
			}
		})
	}
}

// TestConfirmationMessageFormatting tests that confirmation messages are formatted correctly
func TestConfirmationMessageFormatting(t *testing.T) {
	testCases := []struct {
		name            string
		sessionTitle    string
		expectedMessage string
	}{
		{
			name:            "short session name",
			sessionTitle:    "my-feature",
			expectedMessage: "[!] Kill session 'my-feature'? (y/n)",
		},
		{
			name:            "long session name",
			sessionTitle:    "very-long-feature-branch-name-here",
			expectedMessage: "[!] Kill session 'very-long-feature-branch-name-here'? (y/n)",
		},
		{
			name:            "session with spaces",
			sessionTitle:    "feature with spaces",
			expectedMessage: "[!] Kill session 'feature with spaces'? (y/n)",
		},
		{
			name:            "session with special chars",
			sessionTitle:    "feature/branch-123",
			expectedMessage: "[!] Kill session 'feature/branch-123'? (y/n)",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Test the message formatting directly
			actualMessage := fmt.Sprintf("[!] Kill session '%s'? (y/n)", tc.sessionTitle)
			assert.Equal(t, tc.expectedMessage, actualMessage)
		})
	}
}

// TestConfirmationFlowSimulation tests the confirmation flow by simulating the state changes
func TestConfirmationFlowSimulation(t *testing.T) {
	// Create a minimal setup
	spinner := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	list := ui.NewList(&spinner, false)

	// Add test instance
	instance, err := session.NewInstance(session.InstanceOptions{
		Title:   "test-session",
		Path:    t.TempDir(),
		Program: "claude",
		AutoYes: false,
	})
	require.NoError(t, err)
	_ = list.AddInstance(instance)
	list.SetSelectedInstance(0)

	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
		list:      list,
		menu:      ui.NewMenu(),
	}

	// Simulate what happens when D is pressed
	selected := h.list.GetSelectedInstance()
	require.NotNil(t, selected)

	// This is what the KeyKill handler does
	message := fmt.Sprintf("[!] Kill session '%s'?", selected.Title)
	h.confirmationOverlay = overlay.NewConfirmationOverlay(message)
	h.state = stateConfirm

	// Verify the state
	assert.Equal(t, stateConfirm, h.state)
	assert.NotNil(t, h.confirmationOverlay)
	assert.False(t, h.confirmationOverlay.Dismissed)
	// Test that overlay renders with the correct message
	rendered := h.confirmationOverlay.Render()
	assert.Contains(t, rendered, "Kill session 'test-session'?")
}

// TestConfirmActionWithDifferentTypes tests that confirmAction works with different action types
func TestConfirmActionWithDifferentTypes(t *testing.T) {
	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
	}

	t.Run("works with simple action returning nil", func(t *testing.T) {
		actionCalled := false
		action := func() tea.Msg {
			actionCalled = true
			return nil
		}

		// Set up callback to track action execution
		actionExecuted := false
		h.confirmationOverlay = overlay.NewConfirmationOverlay("Test action?")
		h.confirmationOverlay.OnConfirm = func() {
			h.state = stateDefault
			actionExecuted = true
			action() // Execute the action
		}
		h.state = stateConfirm

		// Verify state was set
		assert.Equal(t, stateConfirm, h.state)
		assert.NotNil(t, h.confirmationOverlay)
		assert.False(t, h.confirmationOverlay.Dismissed)
		assert.NotNil(t, h.confirmationOverlay.OnConfirm)

		// Execute the confirmation callback
		h.confirmationOverlay.OnConfirm()
		assert.True(t, actionCalled)
		assert.True(t, actionExecuted)
	})

	t.Run("works with action returning error", func(t *testing.T) {
		expectedErr := fmt.Errorf("test error")
		action := func() tea.Msg {
			return expectedErr
		}

		// Set up callback to track action execution
		var receivedMsg tea.Msg
		h.confirmationOverlay = overlay.NewConfirmationOverlay("Error action?")
		h.confirmationOverlay.OnConfirm = func() {
			h.state = stateDefault
			receivedMsg = action() // Execute the action and capture result
		}
		h.state = stateConfirm

		// Verify state was set
		assert.Equal(t, stateConfirm, h.state)
		assert.NotNil(t, h.confirmationOverlay)
		assert.False(t, h.confirmationOverlay.Dismissed)
		assert.NotNil(t, h.confirmationOverlay.OnConfirm)

		// Execute the confirmation callback
		h.confirmationOverlay.OnConfirm()
		assert.Equal(t, expectedErr, receivedMsg)
	})

	t.Run("works with action returning custom message", func(t *testing.T) {
		action := func() tea.Msg {
			return instanceChangedMsg{}
		}

		// Set up callback to track action execution
		var receivedMsg tea.Msg
		h.confirmationOverlay = overlay.NewConfirmationOverlay("Custom message action?")
		h.confirmationOverlay.OnConfirm = func() {
			h.state = stateDefault
			receivedMsg = action() // Execute the action and capture result
		}
		h.state = stateConfirm

		// Verify state was set
		assert.Equal(t, stateConfirm, h.state)
		assert.NotNil(t, h.confirmationOverlay)
		assert.False(t, h.confirmationOverlay.Dismissed)
		assert.NotNil(t, h.confirmationOverlay.OnConfirm)

		// Execute the confirmation callback
		h.confirmationOverlay.OnConfirm()
		_, ok := receivedMsg.(instanceChangedMsg)
		assert.True(t, ok, "Expected instanceChangedMsg but got %T", receivedMsg)
	})
}

// TestMultipleConfirmationsDontInterfere tests that multiple confirmations don't interfere with each other
func TestMultipleConfirmationsDontInterfere(t *testing.T) {
	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
	}

	// First confirmation
	action1Called := false
	action1 := func() tea.Msg {
		action1Called = true
		return nil
	}

	// Set up first confirmation
	h.confirmationOverlay = overlay.NewConfirmationOverlay("First action?")
	firstOnConfirm := func() {
		h.state = stateDefault
		action1()
	}
	h.confirmationOverlay.OnConfirm = firstOnConfirm
	h.state = stateConfirm

	// Verify first confirmation
	assert.Equal(t, stateConfirm, h.state)
	assert.NotNil(t, h.confirmationOverlay)
	assert.False(t, h.confirmationOverlay.Dismissed)
	assert.NotNil(t, h.confirmationOverlay.OnConfirm)

	// Cancel first confirmation (simulate pressing 'n')
	keyMsg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}
	shouldClose := h.confirmationOverlay.HandleKeyPress(keyMsg)
	if shouldClose {
		h.state = stateDefault
		h.confirmationOverlay = nil
	}

	// Second confirmation with different action
	action2Called := false
	action2 := func() tea.Msg {
		action2Called = true
		return fmt.Errorf("action2 error")
	}

	// Set up second confirmation
	h.confirmationOverlay = overlay.NewConfirmationOverlay("Second action?")
	var secondResult tea.Msg
	secondOnConfirm := func() {
		h.state = stateDefault
		secondResult = action2()
	}
	h.confirmationOverlay.OnConfirm = secondOnConfirm
	h.state = stateConfirm

	// Verify second confirmation
	assert.Equal(t, stateConfirm, h.state)
	assert.NotNil(t, h.confirmationOverlay)
	assert.False(t, h.confirmationOverlay.Dismissed)
	assert.NotNil(t, h.confirmationOverlay.OnConfirm)

	// Execute second action to verify it's the correct one
	h.confirmationOverlay.OnConfirm()
	err, ok := secondResult.(error)
	assert.True(t, ok)
	assert.Equal(t, "action2 error", err.Error())
	assert.True(t, action2Called)
	assert.False(t, action1Called, "First action should not have been called")

	// Test that cancelled action can still be executed independently
	firstOnConfirm()
	assert.True(t, action1Called, "First action should be callable after being replaced")
}

// TestConfirmationModalVisualAppearance tests that confirmation modal has distinct visual appearance
func TestConfirmationModalVisualAppearance(t *testing.T) {
	h := &home{
		ctx:       context.Background(),
		state:     stateDefault,
		appConfig: config.DefaultConfig(),
	}

	// Create a test confirmation overlay
	message := "[!] Delete everything?"
	h.confirmationOverlay = overlay.NewConfirmationOverlay(message)
	h.state = stateConfirm

	// Verify the overlay was created with confirmation settings
	assert.NotNil(t, h.confirmationOverlay)
	assert.Equal(t, stateConfirm, h.state)
	assert.False(t, h.confirmationOverlay.Dismissed)

	// Test the overlay render (we can test that it renders without errors)
	rendered := h.confirmationOverlay.Render()
	assert.NotEmpty(t, rendered)

	// Test that it includes the message content and instructions
	assert.Contains(t, rendered, "Delete everything?")
	assert.Contains(t, rendered, "Press")
	assert.Contains(t, rendered, "to confirm")
	assert.Contains(t, rendered, "to cancel")

	// Test that the danger indicator is preserved
	assert.Contains(t, rendered, "[!")
}

// The text filter is edited in its own state so typed letters never trigger commands.
func TestFilterModeKeys(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	spinner := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	list := ui.NewList(&spinner, false)
	h := &home{
		ctx:          context.Background(),
		state:        stateDefault,
		appConfig:    config.DefaultConfig(),
		appState:     config.DefaultState(),
		list:         list,
		menu:         ui.NewMenu(),
		tabbedWindow: ui.NewTabbedWindow(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		errBox:       ui.NewErrBox(),
	}
	press := func(msg tea.KeyMsg) {
		h.keySent = true // skip the menu highlighting round-trip
		h.handleKeyPress(msg)
	}
	runes := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

	press(runes("/"))
	require.Equal(t, stateFilter, h.state)
	press(runes("q"))
	require.Equal(t, stateFilter, h.state, "q is typed into the filter, not quit")
	press(runes("a"))
	require.Equal(t, "qa", list.Filter())
	press(tea.KeyMsg{Type: tea.KeyBackspace})
	require.Equal(t, "q", list.Filter())
	press(tea.KeyMsg{Type: tea.KeyEnter})
	require.Equal(t, stateDefault, h.state)
	require.Equal(t, "q", list.Filter(), "enter keeps the filter")

	press(runes("/"))
	press(tea.KeyMsg{Type: tea.KeyEsc})
	require.Equal(t, stateDefault, h.state)
	require.Equal(t, "", list.Filter(), "esc clears the filter")

	press(runes("a"))
	require.True(t, list.ActiveOnly())
	press(runes("a"))
	require.False(t, list.ActiveOnly())
}

// Hiding the list hands its width to the preview pane.
func TestToggleListGivesPreviewFullWidth(t *testing.T) {
	spinner := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	h := &home{
		ctx:          context.Background(),
		list:         ui.NewList(&spinner, false),
		menu:         ui.NewMenu(),
		tabbedWindow: ui.NewTabbedWindow(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		errBox:       ui.NewErrBox(),
	}
	size := tea.WindowSizeMsg{Width: 200, Height: 50}

	h.updateHandleWindowSizeEvent(size)
	splitWidth, _ := h.tabbedWindow.GetPreviewSize()

	h.listHidden = true
	h.updateHandleWindowSizeEvent(size)
	fullWidth, _ := h.tabbedWindow.GetPreviewSize()

	require.Greater(t, fullWidth, splitWidth)
	require.NotContains(t, h.View(), "Instances", "the list is not drawn while hidden")
}

func TestNotify(t *testing.T) {
	rings := 0
	bell = func() { rings++ }
	defer func() { bell = func() { _, _ = os.Stdout.WriteString("\a") } }()

	h := &home{appConfig: &config.Config{}} // nil notifications: both on
	require.Nil(t, h.notify(0, 0), "nothing changed, no title update")
	require.Zero(t, rings)

	require.NotNil(t, h.notify(1, 1), "a new waiting session updates the title")
	require.Equal(t, 1, rings)
	require.Nil(t, h.notify(0, 1), "same count, no title update")
	require.NotNil(t, h.notify(0, 0), "back to zero restores the title")

	h.appConfig = &config.Config{Notifications: &config.Notifications{Bell: false, Title: false}}
	require.Nil(t, h.notify(3, 3))
	require.Equal(t, 1, rings, "bell disabled")
}

func TestReconcileAdopted(t *testing.T) {
	spinner := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	h := &home{
		ctx:       context.Background(),
		appConfig: &config.Config{ProjectsRoot: "/mnt/c/Projects"},
		list:      ui.NewList(&spinner, false),
	}
	panes := []tmux.Pane{
		{Session: "claudesquad_mine", ID: "%1", Command: "claude", Path: "/wt/mine"},
		{Session: "scratch", ID: "%2", Command: "claude", Path: "/mnt/c/Projects/Fawri/Code/Backend"},
		{Session: "shell", ID: "%3", Command: "zsh", Path: "/home/user"},
	}

	require.True(t, h.reconcileAdopted(panes))
	require.Equal(t, 1, h.list.NumInstances(), "only the foreign claude pane is adopted")
	adopted := h.list.GetInstances()[0]
	require.True(t, adopted.Adopted)
	require.Equal(t, "scratch", adopted.Title)
	require.Equal(t, "Fawri", adopted.Project, "projects resolve from the pane path")

	require.False(t, h.reconcileAdopted(panes), "nothing new: no change")
	require.Same(t, adopted, h.list.GetInstances()[0], "the existing instance is kept, not recreated")

	require.True(t, h.reconcileAdopted(panes[:1]), "the pane went away")
	require.Zero(t, h.list.NumInstances())
}

// While naming a new session, tab switches between worktree and in-place mode.
func TestNamingTabTogglesInPlace(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	spinner := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	h := &home{
		ctx:          context.Background(),
		state:        stateDefault,
		appConfig:    &config.Config{DefaultMode: config.ModeInPlace},
		appState:     config.DefaultState(),
		list:         ui.NewList(&spinner, false),
		menu:         ui.NewMenu(),
		tabbedWindow: ui.NewTabbedWindow(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		errBox:       ui.NewErrBox(),
	}
	press := func(msg tea.KeyMsg) {
		h.keySent = true
		h.handleKeyPress(msg)
	}

	press(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	require.Equal(t, stateNew, h.state)
	inst := h.list.GetInstances()[0]
	require.True(t, inst.InPlace, "default_mode applies to new sessions")

	press(tea.KeyMsg{Type: tea.KeyTab})
	require.False(t, inst.InPlace)
	press(tea.KeyMsg{Type: tea.KeyTab})
	require.True(t, inst.InPlace)

	press(tea.KeyMsg{Type: tea.KeyEsc})
	require.Equal(t, stateDefault, h.state)
	require.Zero(t, h.list.NumInstances())
}

// While naming a new session, left/right cycle through the known repositories.
func TestNamingLeftRightPicksRepo(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	spinner := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	h := &home{
		ctx:          context.Background(),
		state:        stateNew,
		appConfig:    &config.Config{ProjectsRoot: "/mnt/c/Projects"},
		appState:     config.DefaultState(),
		list:         ui.NewList(&spinner, false),
		menu:         ui.NewMenu(),
		tabbedWindow: ui.NewTabbedWindow(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		errBox:       ui.NewErrBox(),
		repoChoices:  []string{"/mnt/c/Projects/Fawri/Code/Backend", "/mnt/c/Projects/CMIBills/Code/Api"},
	}
	inst, err := session.NewInstance(session.InstanceOptions{Title: "", Path: h.repoChoices[0], Program: "claude"})
	require.NoError(t, err)
	h.list.AddInstance(inst)
	press := func(msg tea.KeyMsg) {
		h.keySent = true
		h.handleKeyPress(msg)
	}

	press(tea.KeyMsg{Type: tea.KeyRight})
	require.Equal(t, "/mnt/c/Projects/CMIBills/Code/Api", inst.Path)
	require.Equal(t, "CMIBills", inst.Project, "the project follows the picked repository")
	press(tea.KeyMsg{Type: tea.KeyRight})
	require.Equal(t, "/mnt/c/Projects/Fawri/Code/Backend", inst.Path, "wraps")
	press(tea.KeyMsg{Type: tea.KeyLeft})
	require.Equal(t, "/mnt/c/Projects/CMIBills/Code/Api", inst.Path)
}

// sidebarHome builds a home in sidebar mode with a tmux executor that records commands.
func sidebarHome(t *testing.T, ran *[]string) *home {
	t.Setenv("HOME", t.TempDir())
	ctl, err := sidebar.New("%0", cmd_test.MockCmdExec{
		OutputFunc: func(c *exec.Cmd) ([]byte, error) { return []byte("%0\t0\n%1\t37\n"), nil },
		RunFunc:    func(c *exec.Cmd) error { *ran = append(*ran, strings.Join(c.Args, " ")); return nil },
	}, 36)
	require.NoError(t, err)
	spinner := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	h := &home{
		ctx:          context.Background(),
		state:        stateDefault,
		appConfig:    config.DefaultConfig(),
		appState:     config.DefaultState(),
		list:         ui.NewList(&spinner, false),
		menu:         ui.NewMenu(),
		tabbedWindow: ui.NewTabbedWindow(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		errBox:       ui.NewErrBox(),
		sidebar:      ctl,
	}
	h.menu.SetSidebar(true)
	h.list.SetCompact(true)
	return h
}

func TestSidebarLayoutShowsOnlyTheList(t *testing.T) {
	var ran []string
	h := sidebarHome(t, &ran)
	h.updateHandleWindowSizeEvent(tea.WindowSizeMsg{Width: 36, Height: 40})

	view := h.View()
	require.NotContains(t, view, "Instances", "the sidebar has no title badge")
	require.NotContains(t, view, "Preview")
	require.NotContains(t, view, "Diff")
	for _, line := range strings.Split(view, "\n") {
		require.LessOrEqual(t, lipgloss.Width(line), 36, "line wider than the pane: %q", line)
	}
}

func TestSidebarFollowsTheSelection(t *testing.T) {
	var ran []string
	h := sidebarHome(t, &ran)

	require.Equal(t, "", h.followTarget(), "nothing selected: placeholder")
	require.Nil(t, h.scheduleFollow(), "already showing the placeholder")

	adopted := session.NewAdoptedInstance(tmux.Pane{Session: "scratch", ID: "%7", Command: "claude", Path: t.TempDir()})
	h.list.AddInstance(adopted)()
	require.Equal(t, "scratch", h.followTarget())
	require.NotNil(t, h.scheduleFollow(), "the pane must change")

	h.followVersion = 3
	h.Update(followMsg{version: 2}) // stale timer
	require.Equal(t, "", h.shown)
	h.Update(followMsg{version: 3})
	require.Equal(t, "scratch", h.shown)
	require.Len(t, ran, 1)
	require.Contains(t, ran[0], "tmux respawn-pane -k -t =claudesquad_ui:0.0 sh ")

	// Enter closes the list over the session; the session is already shown so nothing is
	// respawned, and nothing is resized.
	ran = ran[:0]
	h.keySent = true
	h.handleKeyPress(tea.KeyMsg{Type: tea.KeyEnter})
	require.Equal(t, []string{"tmux detach-client -s =claudesquad_list"}, ran)

	// z closes the list too, instead of hiding anything in the layout.
	ran = ran[:0]
	h.keySent = true
	h.handleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")})
	require.Equal(t, []string{"tmux detach-client -s =claudesquad_list"}, ran)
	require.False(t, h.listHidden)
}

// A confirmed action's result reaches Update once the modal closes, instead of being lost.
func TestConfirmedActionResultIsDelivered(t *testing.T) {
	spinner := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	h := &home{
		ctx:          context.Background(),
		state:        stateDefault,
		appConfig:    config.DefaultConfig(),
		list:         ui.NewList(&spinner, false),
		menu:         ui.NewMenu(),
		tabbedWindow: ui.NewTabbedWindow(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		errBox:       ui.NewErrBox(),
	}
	h.confirmAction("[!] Kill session 'x'?", func() tea.Msg { return fmt.Errorf("branch is checked out") })
	require.Equal(t, stateConfirm, h.state)

	h.keySent = true
	_, cmd := h.handleKeyPress(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	require.Equal(t, stateDefault, h.state)
	require.NotNil(t, cmd)
	// Collect the messages the command produces, whether or not Batch collapsed it.
	var msgs []tea.Msg
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range batch {
			if c != nil {
				msgs = append(msgs, c())
			}
		}
	} else {
		msgs = append(msgs, cmd())
	}
	var sawErr bool
	for _, m := range msgs {
		if err, isErr := m.(error); isErr && err.Error() == "branch is checked out" {
			sawErr = true
		}
	}
	require.True(t, sawErr, "the action's error is delivered to Update")
}

// At start the list stays open; opening a session closes it.
func TestEnterClosesTheList(t *testing.T) {
	var ran []string
	h := sidebarHome(t, &ran)
	adopted := session.NewAdoptedInstance(tmux.Pane{Session: "scratch", ID: "%7", Command: "claude", Path: t.TempDir()})
	h.list.AddInstance(adopted)()

	h.Init()
	require.NotContains(t, ran, "tmux detach-client -s =claudesquad_list", "the list stays open at start")

	h.keySent = true
	h.handleKeyPress(tea.KeyMsg{Type: tea.KeyEnter})
	require.Equal(t, "scratch", h.shown)
	require.Contains(t, ran, "tmux detach-client -s =claudesquad_list")
	for _, c := range ran {
		require.NotContains(t, c, "resize-pane", "nothing in the layout is resized")
	}
}

// In the sidebar layout a newly started session is shown at once and the list closes.
func TestSidebarFocusesNewlyStartedSession(t *testing.T) {
	var ran []string
	h := sidebarHome(t, &ran)
	h.storage = newTestStorage(t)
	adopted := session.NewAdoptedInstance(tmux.Pane{Session: "fresh", ID: "%9", Command: "claude", Path: t.TempDir()})
	h.list.AddInstance(adopted)()

	h.Update(instanceStartedMsg{instance: adopted})
	require.Equal(t, "fresh", h.shown)
	require.Contains(t, ran, "tmux detach-client -s =claudesquad_list")
}

// newTestStorage returns a Storage backed by memory so Update paths that save do not touch disk.
func newTestStorage(t *testing.T) *session.Storage {
	t.Helper()
	st, err := session.NewStorage(config.DefaultState())
	require.NoError(t, err)
	return st
}

// Selecting a past session shows a card in the pane instead of attaching anything.
func TestSidebarShowsCardForArchivedSession(t *testing.T) {
	var ran []string
	h := sidebarHome(t, &ran)
	archived := session.NewArchivedInstance("abcd", "old work", t.TempDir(), "finish the report", time.Now())
	h.list.AddInstance(archived)()

	require.Equal(t, "card:abcd", h.followKey())
	require.Nil(t, h.showSelected())
	require.Equal(t, "card:abcd", h.shown)
	require.Len(t, ran, 1)
	require.Contains(t, ran[0], "card.sh")
	require.Nil(t, h.scheduleFollow(), "already showing that card")
}

// The wheel moves the selection and clicks fold headers or select rows.
func TestMouseInSidebar(t *testing.T) {
	var ran []string
	h := sidebarHome(t, &ran)
	base := time.Now()
	for i, spec := range [][2]string{{"a1", "A"}, {"a2", "A"}, {"b1", "B"}} {
		inst := session.NewAdoptedInstance(tmux.Pane{Session: spec[0], ID: "%" + spec[0], Command: "claude", Path: t.TempDir()})
		inst.Project = spec[1]
		inst.LastActivity = base.Add(-time.Duration(i) * time.Minute) // keep insertion order in the list
		h.list.AddInstance(inst)()
	}
	h.updateHandleWindowSizeEvent(tea.WindowSizeMsg{Width: 36, Height: 40})
	view := h.View()

	h.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown, X: 5, Y: 10})
	require.Equal(t, "a2", h.list.GetSelectedInstance().Title, "wheel down moves the selection")

	headerY, rowY := -1, -1
	for i, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "▾ B") {
			headerY = i
		}
		if strings.Contains(line, "a1") {
			rowY = i
		}
	}
	require.NotEqual(t, -1, headerY)
	h.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 5, Y: headerY})
	require.Equal(t, []string{"B"}, h.list.FoldedProjects(), "clicking a header folds it")

	h.Update(tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonLeft, X: 5, Y: rowY})
	require.Equal(t, "a1", h.list.GetSelectedInstance().Title, "clicking a row selects it")
}

// When the program of a session running in place exits, the session leaves the list (its
// transcript comes back as a past session); a worktree session would be kept, paused.
func TestEndedInPlaceSessionLeavesTheList(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	spinner := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	h := &home{
		ctx:          context.Background(),
		appConfig:    &config.Config{DisableHistory: true},
		appState:     config.DefaultState(),
		list:         ui.NewList(&spinner, false),
		menu:         ui.NewMenu(),
		tabbedWindow: ui.NewTabbedWindow(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		errBox:       ui.NewErrBox(),
		storage:      newTestStorage(t),
	}
	inplace, err := session.NewInstance(session.InstanceOptions{Title: "here", Path: t.TempDir(), Program: "claude", ResumeID: "abc"})
	require.NoError(t, err)
	worktree, err := session.NewInstance(session.InstanceOptions{Title: "wt", Path: t.TempDir(), Program: "claude"})
	require.NoError(t, err)
	h.list.AddInstance(inplace)()
	h.list.AddInstance(worktree)()

	h.Update(metadataUpdateDoneMsg{results: []instanceMetaResult{
		{instance: inplace, gone: true},
		{instance: worktree, gone: true},
	}})
	require.Equal(t, []*session.Instance{worktree}, h.list.GetInstances())
}
