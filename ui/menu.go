package ui

import (
	"claude-squad/keys"
	"strings"

	"claude-squad/session"

	"github.com/charmbracelet/lipgloss"
)

var keyStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{
	Light: "#655F5F",
	Dark:  "#7F7A7A",
})

var descStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{
	Light: "#7A7474",
	Dark:  "#9C9494",
})

var sepStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{
	Light: "#DDDADA",
	Dark:  "#3C3C3C",
})

var actionGroupStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("99"))

var separator = " • "
var verticalSeparator = " │ "

var menuStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("205"))

// MenuState represents different states the menu can be in
type MenuState int

const (
	StateDefault MenuState = iota
	StateEmpty
	StateNewInstance
	StatePrompt
	StateFilter
)

type Menu struct {
	options       []keys.KeyName
	height, width int
	state         MenuState
	instance      *session.Instance
	activeTab     int

	// keyDown is the key which is pressed. The default is -1.
	keyDown keys.KeyName

	// sidebar switches to the sidebar layout's labels and a compact, wrapped rendering.
	sidebar bool
}

var defaultMenuOptions = []keys.KeyName{keys.KeyNew, keys.KeyPrompt, keys.KeyHelp, keys.KeyQuit}
var newInstanceMenuOptions = []keys.KeyName{keys.KeySubmitName, keys.KeyPickRepo, keys.KeyToggleMode}
var promptMenuOptions = []keys.KeyName{keys.KeySubmitName}
var filterMenuOptions = []keys.KeyName{keys.KeyFilterKeep, keys.KeyFilterClear}

func NewMenu() *Menu {
	return &Menu{
		options:   defaultMenuOptions,
		state:     StateEmpty,
		activeTab: 0,
		keyDown:   -1,
	}
}

func (m *Menu) Keydown(name keys.KeyName) {
	m.keyDown = name
}

func (m *Menu) ClearKeydown() {
	m.keyDown = -1
}

// SetSidebar switches the menu to the sidebar layout: different labels for the keys that act
// on the session pane, and a compact rendering that wraps to the pane width.
func (m *Menu) SetSidebar(on bool) {
	m.sidebar = on
	m.updateOptions()
}

// SetState updates the menu state and options accordingly
func (m *Menu) SetState(state MenuState) {
	m.state = state
	m.updateOptions()
}

// SetInstance updates the current instance and refreshes menu options
func (m *Menu) SetInstance(instance *session.Instance) {
	m.instance = instance
	// Only change the state if we're not in a special state (NewInstance or Prompt)
	if m.state != StateNewInstance && m.state != StatePrompt && m.state != StateFilter {
		if m.instance != nil {
			m.state = StateDefault
		} else {
			m.state = StateEmpty
		}
	}
	m.updateOptions()
}

// SetActiveTab updates the currently active tab
func (m *Menu) SetActiveTab(tab int) {
	m.activeTab = tab
	m.updateOptions()
}

// updateOptions updates the menu options based on current state and instance
func (m *Menu) updateOptions() {
	switch m.state {
	case StateEmpty:
		m.options = defaultMenuOptions
	case StateDefault:
		if m.instance != nil {
			// When there is an instance, show that instance's options
			m.addInstanceOptions()
		} else {
			// When there is no instance, show the empty state
			m.options = defaultMenuOptions
		}
	case StateNewInstance:
		m.options = newInstanceMenuOptions
	case StatePrompt:
		m.options = promptMenuOptions
	case StateFilter:
		m.options = filterMenuOptions
	}
}

func (m *Menu) addInstanceOptions() {
	// Loading instances only get minimal options
	if m.instance != nil && m.instance.Status == session.Loading {
		m.options = []keys.KeyName{keys.KeyNew, keys.KeyHelp, keys.KeyQuit}
		return
	}

	if m.sidebar {
		m.options = m.sidebarOptions()
		return
	}

	// Adopted sessions can only be watched and opened.
	if m.instance.Adopted {
		m.options = []keys.KeyName{keys.KeyNew, keys.KeyEnter, keys.KeyTab, keys.KeyNextWaiting,
			keys.KeyActiveOnly, keys.KeySearch, keys.KeyToggleList, keys.KeyHelp, keys.KeyQuit}
		return
	}

	// Instance management group
	options := []keys.KeyName{keys.KeyNew, keys.KeyKill}

	// Action group. In-place sessions have no worktree to push or check out.
	actionGroup := []keys.KeyName{keys.KeyEnter}
	if !m.instance.InPlace {
		actionGroup = append(actionGroup, keys.KeySubmit)
	}
	if m.instance.Status == session.Paused {
		actionGroup = append(actionGroup, keys.KeyResume)
	} else if !m.instance.InPlace {
		actionGroup = append(actionGroup, keys.KeyCheckout)
	}

	// Navigation group (when in diff tab)
	if m.activeTab == DiffTab || m.activeTab == TerminalTab {
		actionGroup = append(actionGroup, keys.KeyShiftUp)
	}

	// System group
	systemGroup := []keys.KeyName{keys.KeyTab, keys.KeyNextWaiting, keys.KeyActiveOnly, keys.KeySearch, keys.KeyToggleList, keys.KeyHelp, keys.KeyQuit}

	// Combine all groups
	options = append(options, actionGroup...)
	options = append(options, systemGroup...)

	m.options = options
}

// sidebarOptions are the keys shown under the session list in the sidebar layout.
func (m *Menu) sidebarOptions() []keys.KeyName {
	options := []keys.KeyName{keys.KeyNew, keys.KeyPrompt}
	if !m.instance.Adopted {
		options = append(options, keys.KeyKill)
	}
	options = append(options, keys.KeyFocus, keys.KeyDiffView, keys.KeyTerminalView)
	if !m.instance.Adopted && !m.instance.InPlace {
		if m.instance.Status == session.Paused {
			options = append(options, keys.KeyResume)
		} else {
			options = append(options, keys.KeyCheckout)
		}
		options = append(options, keys.KeySubmit)
	}
	return append(options, keys.KeyNextWaiting, keys.KeyActiveOnly, keys.KeySearch,
		keys.KeyZoom, keys.KeyHelp, keys.KeyQuit)
}

// SidebarMenuHeight is the number of lines reserved for the compact menu.
const SidebarMenuHeight = 6

// compactString lays the options out left-aligned on as many lines as needed to fit the
// width, for the narrow sidebar pane.
func (m *Menu) compactString() string {
	var lines []string
	// While naming a session the preview pane is not there to show where it will run.
	if m.state == StateNewInstance && m.instance != nil {
		mode := "worktree"
		if m.instance.InPlace {
			mode = "in-place"
		}
		lines = append(lines, descStyle.Render("in ")+keyStyle.Render(m.instance.RepoLabel())+descStyle.Render(" · "+mode))
	}
	line := ""
	for _, k := range m.options {
		binding := keys.GlobalkeyBindings[k]
		item := keyStyle.Render(binding.Help().Key) + " " + descStyle.Render(binding.Help().Desc)
		if m.keyDown == k {
			item = keyStyle.Underline(true).Render(binding.Help().Key) + " " + descStyle.Underline(true).Render(binding.Help().Desc)
		}
		itemWidth := lipgloss.Width(item)
		if line != "" && lipgloss.Width(line)+2+itemWidth > m.width-1 {
			lines = append(lines, line)
			line = ""
		}
		if line != "" {
			line += "  "
		}
		line += item
	}
	if line != "" {
		lines = append(lines, line)
	}
	if m.height > 0 && len(lines) > m.height {
		lines = lines[:m.height]
	}
	return lipgloss.NewStyle().PaddingLeft(1).Render(strings.Join(lines, "\n"))
}

// SetSize sets the width of the window. The menu will be centered horizontally within this width.
func (m *Menu) SetSize(width, height int) {
	m.width = width
	m.height = height
}

func (m *Menu) String() string {
	if m.sidebar {
		return m.compactString()
	}

	var s strings.Builder

	// Define group boundaries
	groups := []struct {
		start int
		end   int
	}{
		{0, 2}, // Instance management group (n, d)
		{2, 5}, // Action group (enter, submit, pause/resume)
		{6, 8}, // System group (tab, help, q)
	}

	for i, k := range m.options {
		binding := keys.GlobalkeyBindings[k]

		var (
			localActionStyle = actionGroupStyle
			localKeyStyle    = keyStyle
			localDescStyle   = descStyle
		)
		if m.keyDown == k {
			localActionStyle = localActionStyle.Underline(true)
			localKeyStyle = localKeyStyle.Underline(true)
			localDescStyle = localDescStyle.Underline(true)
		}

		var inActionGroup bool
		switch m.state {
		case StateEmpty:
			// For empty state, the action group is the first group
			inActionGroup = i <= 1
		default:
			// For other states, the action group is the second group
			inActionGroup = i >= groups[1].start && i < groups[1].end
		}

		if inActionGroup {
			s.WriteString(localActionStyle.Render(binding.Help().Key))
			s.WriteString(" ")
			s.WriteString(localActionStyle.Render(binding.Help().Desc))
		} else {
			s.WriteString(localKeyStyle.Render(binding.Help().Key))
			s.WriteString(" ")
			s.WriteString(localDescStyle.Render(binding.Help().Desc))
		}

		// Add appropriate separator
		if i != len(m.options)-1 {
			isGroupEnd := false
			for _, group := range groups {
				if i == group.end-1 {
					s.WriteString(sepStyle.Render(verticalSeparator))
					isGroupEnd = true
					break
				}
			}
			if !isGroupEnd {
				s.WriteString(sepStyle.Render(separator))
			}
		}
	}

	centeredMenuText := menuStyle.Render(s.String())
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, centeredMenuText)
}
