package app

import (
	"claude-squad/cmd"
	"claude-squad/config"
	"claude-squad/hooks"
	"claude-squad/keys"
	"claude-squad/log"
	"claude-squad/session"
	"claude-squad/session/git"
	"claude-squad/session/history"
	"claude-squad/session/tmux"
	"claude-squad/sidebar"
	"claude-squad/ui"
	"claude-squad/ui/overlay"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// Options tunes how the application runs.
type Options struct {
	// Sidebar runs the session list alone in a narrow tmux pane and shows the selected
	// session, attached for real, in the pane beside it (see package sidebar).
	Sidebar bool
}

// Run is the main entrypoint into the application: the classic dashboard layout.
func Run(ctx context.Context, program string, autoYes bool) error {
	return RunWithOptions(ctx, program, autoYes, Options{})
}

// RunWithOptions runs the application with the given options.
func RunWithOptions(ctx context.Context, program string, autoYes bool, opts Options) error {
	if opts.Sidebar {
		// The viewer client sizes the sessions; the connection claude-squad keeps to each one
		// must not, or sessions jump between sizes and their programs redraw.
		tmux.BackgroundClientsIgnoreSize = true
	}
	h := newHome(ctx, program, autoYes)
	if opts.Sidebar {
		ctl, err := sidebar.New(os.Getenv("TMUX_PANE"), cmd.MakeExecutor(), h.appConfig.GetSidebarWidth())
		if err != nil {
			return err
		}
		h.sidebar = ctl
		// New sessions start at the viewer's size, and running ones are brought to it, so
		// showing any of them needs no resize.
		tmux.NewSessionSize = ctl.ViewerSize
		var running []string
		for _, inst := range h.list.GetInstances() {
			if inst.Started() && !inst.Paused() && !inst.Adopted && inst.TmuxName() != "" {
				running = append(running, inst.TmuxName())
			}
		}
		ctl.FitSessions(running)
		h.menu.SetSidebar(true)
		h.list.SetCompact(true)
	}
	p := tea.NewProgram(
		h,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(), // Mouse scroll
	)
	_, err := p.Run()
	return err
}

type state int

const (
	stateDefault state = iota
	// stateNew is the state when the user is creating a new instance.
	stateNew
	// statePrompt is the state when the user is entering a prompt.
	statePrompt
	// stateHelp is the state when a help screen is displayed.
	stateHelp
	// stateConfirm is the state when a confirmation modal is displayed.
	stateConfirm
	// stateFilter is the state when the user is typing a text filter for the list.
	stateFilter
)

type home struct {
	ctx context.Context

	// -- Storage and Configuration --

	program string
	autoYes bool

	// storage is the interface for saving/loading data to/from the app's state
	storage *session.Storage
	// appConfig stores persistent application configuration
	appConfig *config.Config
	// appState stores persistent application state like seen help screens
	appState config.AppState
	// statusDir holds the hook status files written by `cs hook`
	statusDir string

	// -- State --

	// state is the current discrete state of the application
	state state
	// newInstanceFinalizer is called when the state is stateNew and then you press enter.
	// It registers the new instance in the list after the instance has been started.
	newInstanceFinalizer func()

	// promptAfterName tracks if we should enter prompt mode after naming
	promptAfterName bool
	// repoChoices are the repositories offered while naming a new session; repoIdx is the
	// one currently picked.
	repoChoices []string
	repoIdx     int

	// keySent is used to manage underlining menu items
	keySent bool

	// listHidden collapses the session list so the preview takes the full width.
	listHidden bool
	// lastWaitingTotal is the waiting count last written to the window title.
	lastWaitingTotal int

	// sidebar drives the session pane in the sidebar layout; nil in the classic layout.
	sidebar *sidebar.Controller
	// shown is the tmux session currently displayed in the session pane ("" = placeholder).
	shown string
	// followVersion invalidates pending follow timers when the selection moves again.
	followVersion uint64
	// historyScanner reads Claude's transcripts, re-reading only changed files; repoRoots
	// maps a transcript's directory to its repository root ("" outside a repository). Both
	// are filled by the background scan.
	historyScanner *history.Scanner
	repoRoots      map[string]string
	// scanNow cuts the wait of the pending scan short (see requestScan).
	scanNow chan struct{}
	// titleWatcher follows the transcripts of running sessions for /rename; transcripts
	// maps each Claude session id to its transcript, as of the last scan.
	titleWatcher *history.TitleWatcher
	transcripts  map[string]string
	// width is the last known terminal width; listWidth the columns the list occupies.
	width     int
	listWidth int
	// lastClick remembers the previous click, so a second click on the same row opens it.
	lastClick     *session.Instance
	lastClickTime time.Time

	// instanceStarting is true while a background instance start is in progress.
	// Prevents double-submission and guards against interacting with a not-yet-started instance.
	instanceStarting bool
	// startingInstance holds a reference to the instance being started in the background.
	startingInstance *session.Instance

	// -- UI Components --

	// list displays the list of instances
	list *ui.List
	// menu displays the bottom menu
	menu *ui.Menu
	// tabbedWindow displays the tabbed window with preview and diff panes
	tabbedWindow *ui.TabbedWindow
	// errBox displays error messages
	errBox *ui.ErrBox
	// global spinner instance. we plumb this down to where it's needed
	spinner spinner.Model
	// textInputOverlay handles text input with state
	textInputOverlay *overlay.TextInputOverlay
	// textOverlay displays text information
	textOverlay *overlay.TextOverlay
	// confirmationOverlay displays confirmation modals
	confirmationOverlay *overlay.ConfirmationOverlay
	// confirmResult is the message produced by the last confirmed action, delivered to Update
	// once the modal closes.
	confirmResult tea.Msg
}

func newHome(ctx context.Context, program string, autoYes bool) *home {
	// Load application config
	appConfig := config.LoadConfig()

	// Load application state
	appState := config.LoadState()

	// Initialize storage
	storage, err := session.NewStorage(appState)
	if err != nil {
		fmt.Printf("Failed to initialize storage: %v\n", err)
		os.Exit(1)
	}

	statusDir, err := hooks.StatusDir()
	if err != nil {
		log.WarningLog.Printf("hook status disabled: %v", err)
	}

	h := &home{
		ctx:          ctx,
		statusDir:    statusDir,
		spinner:      spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		menu:         ui.NewMenu(),
		tabbedWindow: ui.NewTabbedWindow(ui.NewPreviewPane(), ui.NewDiffPane(), ui.NewTerminalPane()),
		errBox:       ui.NewErrBox(),
		storage:      storage,
		appConfig:    appConfig,
		program:      program,
		autoYes:      autoYes,
		state:        stateDefault,
		appState:     appState,
	}
	h.list = ui.NewList(&h.spinner, autoYes)

	// Load saved instances
	instances, err := storage.LoadInstances()
	if err != nil {
		fmt.Printf("Failed to load instances: %v\n", err)
		os.Exit(1)
	}

	// Add loaded instances to the list. A session that ran in place and is now paused had its
	// program exit (in-place sessions cannot be paused otherwise): it is listed again from
	// its transcript instead.
	kept := instances[:0]
	for _, instance := range instances {
		if instance.InPlace && instance.Paused() {
			continue
		}
		kept = append(kept, instance)
	}
	instances = kept
	for _, instance := range instances {
		instance.Project = appConfig.GroupFor(instance.ProjectPath())
		// Call the finalizer immediately.
		h.list.AddInstance(instance)()
		if autoYes {
			instance.AutoYes = true
		}
	}
	h.list.SetProjectColors(projectColors(appConfig))
	h.list.SetFolded(appState.GetUIPrefs().FoldedProjects)
	h.list.SetLabelSegments(appConfig.GetGroupLabelSegments())
	if prefs := appState.GetUIPrefs(); prefs.FilterMode != "" {
		h.list.SetMode(ui.ParseViewMode(prefs.FilterMode))
	} else {
		h.list.SetActiveOnly(prefs.ActiveOnly)
	}
	h.listHidden = appState.GetUIPrefs().ListHidden

	return h
}

// projectColors returns the configured header color per project.
func projectColors(cfg *config.Config) map[string]string {
	colors := make(map[string]string, len(cfg.Projects))
	for _, p := range cfg.Projects {
		if p.Color != "" {
			colors[p.Name] = p.Color
		}
	}
	return colors
}

// saveUIPrefs persists the view preferences (folds etc.).
func (m *home) saveUIPrefs() {
	prefs := m.appState.GetUIPrefs()
	prefs.FoldedProjects = m.list.FoldedProjects()
	prefs.ActiveOnly = m.list.ActiveOnly()
	prefs.FilterMode = m.list.Mode().String()
	prefs.ListHidden = m.listHidden
	if err := m.appState.SetUIPrefs(prefs); err != nil {
		log.WarningLog.Printf("failed to save UI preferences: %v", err)
	}
}

// updateHandleWindowSizeEvent sets the sizes of the components.
// The components will try to render inside their bounds.
func (m *home) updateHandleWindowSizeEvent(msg tea.WindowSizeMsg) {
	// List takes 30% of width, preview takes 70%; a hidden list gives the preview everything.
	// In the sidebar layout the list is the whole pane.
	listWidth := int(float32(msg.Width) * 0.3)
	if m.listHidden {
		listWidth = 0
	}
	if m.sidebar != nil {
		listWidth = msg.Width - 1 // the last column is the list's edge
	}
	m.listWidth = listWidth
	tabsWidth := msg.Width - listWidth

	// Menu takes 10% of height, list and window take 90%; the compact sidebar menu needs a
	// fixed number of lines.
	contentHeight := int(float32(msg.Height) * 0.9)
	menuHeight := msg.Height - contentHeight - 1 // minus 1 for error box
	if m.sidebar != nil {
		menuHeight = ui.SidebarMenuHeight
		contentHeight = msg.Height - menuHeight - 1
	}
	m.errBox.SetSize(int(float32(msg.Width)*0.9), 1) // error box takes 1 row

	m.tabbedWindow.SetSize(tabsWidth, contentHeight)
	m.list.SetSize(listWidth, contentHeight)

	m.width = msg.Width
	overlayWidth := int(float32(msg.Width) * 0.6)
	if m.sidebar != nil {
		overlayWidth = msg.Width - 2 // the pane is narrow: use all of it
	}
	if m.textInputOverlay != nil {
		m.textInputOverlay.SetSize(overlayWidth, int(float32(msg.Height)*0.4))
	}
	if m.textOverlay != nil {
		m.textOverlay.SetWidth(int(float32(msg.Width) * 0.6))
	}

	if m.sidebar == nil {
		// The session pane's own tmux client sizes the sessions in the sidebar layout.
		previewWidth, previewHeight := m.tabbedWindow.GetPreviewSize()
		if err := m.list.SetSessionPreviewSize(previewWidth, previewHeight); err != nil {
			log.ErrorLog.Print(err)
		}
	}
	if m.sidebar != nil {
		m.menu.SetSize(msg.Width-1, menuHeight)
	} else {
		m.menu.SetSize(msg.Width, menuHeight)
	}
}

func (m *home) Init() tea.Cmd {
	// Upon starting, we want to start the spinner. Whenever we get a spinner.TickMsg, we
	// update the spinner, which sends a new spinner.TickMsg. I think this lasts forever lol.
	cmds := []tea.Cmd{
		m.spinner.Tick,
		tickUpdateMetadataCmd(m.snapshotActiveInstances(), m.list.GetSelectedInstance(), m.statusDir),
		m.scanAdoptCmd(),
		m.scanHistoryCmd(0),
		m.watchTitlesCmd(),
	}
	if m.sidebar == nil {
		cmds = append(cmds, func() tea.Msg {
			time.Sleep(100 * time.Millisecond)
			return previewTickMsg{}
		})
	} else {
		// Start on the list: the selected session is shown beside it, nothing is focused.
		cmds = append(cmds, m.instanceChanged())
	}
	return tea.Batch(cmds...)
}

// adoptScanInterval is how often tmux is scanned for agent sessions to adopt.
const adoptScanInterval = 2 * time.Second

// adoptScanMsg carries the panes found on the tmux server.
type adoptScanMsg struct {
	panes []tmux.Pane
}

// scanAdoptCmd lists tmux panes in the background after adoptScanInterval. It is a no-op
// when adoption is disabled.
func (m *home) scanAdoptCmd() tea.Cmd {
	if m.appConfig.DisableAdopt {
		return nil
	}
	return func() tea.Msg {
		time.Sleep(adoptScanInterval)
		panes, err := tmux.ListPanes(cmd.MakeExecutor())
		if err != nil {
			log.WarningLog.Printf("could not list tmux panes: %v", err)
			return nil
		}
		return adoptScanMsg{panes: panes}
	}
}

// reconcileAdopted adds newly found foreign agent sessions to the list and drops the ones
// that went away. It reports whether the list changed.
func (m *home) reconcileAdopted(panes []tmux.Pane) bool {
	wanted := session.AdoptablePanes(panes, m.appConfig.GetAdoptPrograms())
	live := make(map[string]bool, len(wanted))
	for _, p := range wanted {
		live[p.Session] = true
	}

	changed := false
	known := make(map[string]bool)
	for _, inst := range m.list.GetInstances() {
		if !inst.Adopted {
			continue
		}
		if !live[inst.Title] {
			m.list.Remove(inst)
			changed = true
			continue
		}
		known[inst.Title] = true
	}
	for _, p := range wanted {
		if known[p.Session] {
			continue
		}
		inst := session.NewAdoptedInstance(p)
		inst.Project = m.appConfig.GroupFor(p.Path)
		m.list.AddInstance(inst)()
		changed = true
	}
	return changed
}

func (m *home) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case hideErrMsg:
		m.errBox.Clear()
	case previewTickMsg:
		cmd := m.instanceChanged()
		return m, tea.Batch(
			cmd,
			func() tea.Msg {
				time.Sleep(100 * time.Millisecond)
				return previewTickMsg{}
			},
		)
	case keyupMsg:
		m.menu.ClearKeydown()
		return m, nil
	case followMsg:
		if msg.version != m.followVersion {
			return m, nil // the selection moved again
		}
		return m, m.showSelected()
	case historyScanMsg:
		if m.repoRoots == nil {
			m.repoRoots = make(map[string]string)
		}
		for dir, root := range msg.roots {
			m.repoRoots[dir] = root
		}
		next := m.scanHistoryCmd(historyScanInterval)
		if m.reconcileHistory(msg.entries) {
			return m, tea.Batch(m.instanceChanged(), next)
		}
		return m, next
	case titleWatchMsg:
		m.applyTitles(msg.titles)
		return m, m.watchTitlesCmd()
	case adoptScanMsg:
		livePanes := make(map[string]bool, len(msg.panes))
		for _, p := range msg.panes {
			livePanes[p.ID] = true
		}
		if m.statusDir != "" {
			hooks.Prune(m.statusDir, livePanes)
		}
		if m.reconcileAdopted(msg.panes) {
			return m, tea.Batch(m.instanceChanged(), m.scanAdoptCmd())
		}
		return m, m.scanAdoptCmd()
	case instanceStartDoneMsg:
		m.instanceStarting = false
		inst := msg.instance
		m.startingInstance = nil

		if msg.err != nil {
			// Start failed — remove the instance from the list and show the error.
			m.list.Kill()
			return m, tea.Batch(tea.WindowSize(), m.instanceChanged(), m.handleError(msg.err))
		}

		// Save after successful start.
		if err := m.storage.SaveInstances(m.list.GetInstances()); err != nil {
			return m, m.handleError(err)
		}

		if m.promptAfterName {
			m.state = statePrompt
			m.menu.SetState(ui.StatePrompt)
			m.textInputOverlay = overlay.NewTextInputOverlay("Enter prompt", "")
			m.promptAfterName = false
		} else {
			m.showHelpScreen(helpStart(inst), nil)
		}

		return m, tea.Batch(tea.WindowSize(), m.instanceChanged())
	case metadataUpdateDoneMsg:
		newlyWaiting := 0
		ended := false
		for _, r := range msg.results {
			// Skip instances that were paused while metadata was being computed
			if r.instance.Status == session.Paused {
				continue
			}
			// A session running in place whose program exited has nothing left to keep: drop
			// it, and its transcript is listed again as a past session to resume.
			if r.gone && r.instance.InPlace && !r.instance.Adopted {
				m.list.Remove(r.instance)
				ended = true
				continue
			}
			wasWaiting := r.instance.NeedsAttention()
			r.instance.ApplyObservation(r.observation, r.hook, time.Now())
			if !wasWaiting && r.instance.NeedsAttention() {
				newlyWaiting++
			}
			if r.diffStats != nil && r.diffStats.Error != nil {
				if !strings.Contains(r.diffStats.Error.Error(), "base commit SHA not set") {
					log.WarningLog.Printf("could not update diff stats: %v", r.diffStats.Error)
				}
				r.instance.SetDiffStats(nil)
			} else {
				r.instance.SetDiffStats(r.diffStats)
			}
		}
		cmds := []tea.Cmd{
			m.notify(newlyWaiting, m.list.WaitingCount()),
			tickUpdateMetadataCmd(m.snapshotActiveInstances(), m.list.GetSelectedInstance(), m.statusDir),
		}
		if ended {
			if err := m.storage.SaveInstances(m.list.GetInstances()); err != nil {
				log.WarningLog.Printf("could not save instances: %v", err)
			}
			// Rescan now so the ended session reappears as a past one without waiting.
			m.requestScan()
			cmds = append(cmds, m.instanceChanged())
		}
		return m, tea.Batch(cmds...)
	case tea.MouseMsg:
		return m.handleMouse(msg)
	case branchSearchDebounceMsg:
		// Debounce timer fired — check if this is still the current filter version
		if m.textInputOverlay == nil {
			return m, nil
		}
		if msg.version != m.textInputOverlay.BranchFilterVersion() {
			return m, nil // stale, a newer debounce is pending
		}
		return m, m.runBranchSearch(msg.filter, msg.version)
	case branchSearchResultMsg:
		if m.textInputOverlay != nil {
			m.textInputOverlay.SetBranchResults(msg.branches, msg.version)
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKeyPress(msg)
	case tea.WindowSizeMsg:
		m.updateHandleWindowSizeEvent(msg)
		return m, nil
	case error:
		// Handle errors from confirmation actions
		return m, m.handleError(msg)
	case instanceChangedMsg:
		// Handle instance changed after confirmation action
		return m, m.instanceChanged()
	case instanceStartedMsg:
		// Select the instance that just started (or failed)
		m.list.SelectInstance(msg.instance)

		if msg.err != nil {
			m.list.Kill()
			return m, tea.Batch(m.handleError(msg.err), m.instanceChanged())
		}

		// Save after successful start
		if err := m.storage.SaveInstances(m.list.GetInstances()); err != nil {
			return m, m.handleError(err)
		}
		if m.autoYes {
			msg.instance.AutoYes = true
		}

		if msg.promptAfterName {
			m.state = statePrompt
			m.menu.SetState(ui.StatePrompt)
			m.textInputOverlay = m.newPromptOverlay()
		} else {
			// If instance has a prompt (set from Shift+N flow), send it now
			if msg.instance.Prompt != "" {
				if err := msg.instance.SendPrompt(msg.instance.Prompt); err != nil {
					log.ErrorLog.Printf("failed to send prompt: %v", err)
				}
				msg.instance.Prompt = ""
			}
			m.menu.SetState(ui.StateDefault)
			m.showHelpScreen(helpStart(msg.instance), nil)
		}

		if m.sidebar != nil {
			// A session you just created is where you want to be: open it right away.
			return m, tea.Batch(tea.WindowSize(), m.focusSession())
		}
		return m, tea.Batch(tea.WindowSize(), m.instanceChanged())
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *home) handleQuit() (tea.Model, tea.Cmd) {
	if err := m.storage.SaveInstances(m.list.GetInstances()); err != nil {
		return m, m.handleError(err)
	}
	if m.sidebar != nil {
		// Closing the layout ends this process too; the agent sessions keep running detached.
		if err := m.sidebar.Quit(); err != nil {
			log.WarningLog.Printf("could not close the sidebar layout: %v", err)
		}
	}
	return m, tea.Quit
}

func (m *home) handleMenuHighlighting(msg tea.KeyMsg) (cmd tea.Cmd, returnEarly bool) {
	// Handle menu highlighting when you press a button. We intercept it here and immediately return to
	// update the ui while re-sending the keypress. Then, on the next call to this, we actually handle the keypress.
	if m.keySent {
		m.keySent = false
		return nil, false
	}
	if m.state == statePrompt || m.state == stateHelp || m.state == stateConfirm || m.state == stateFilter {
		return nil, false
	}
	// If it's in the global keymap, we should try to highlight it.
	name, ok := keys.GlobalKeyStringsMap[msg.String()]
	if !ok {
		return nil, false
	}

	if m.list.GetSelectedInstance() != nil && m.list.GetSelectedInstance().Paused() && name == keys.KeyEnter {
		return nil, false
	}
	if name == keys.KeyShiftDown || name == keys.KeyShiftUp {
		return nil, false
	}

	// Skip the menu highlighting if the key is not in the map or we are using the shift up and down keys.
	// TODO: cleanup: when you press enter on stateNew, we use keys.KeySubmitName. We should unify the keymap.
	if name == keys.KeyEnter && m.state == stateNew {
		name = keys.KeySubmitName
	}
	m.keySent = true
	return tea.Batch(
		func() tea.Msg { return msg },
		m.keydownCallback(name)), true
}

func (m *home) handleKeyPress(msg tea.KeyMsg) (mod tea.Model, cmd tea.Cmd) {
	cmd, returnEarly := m.handleMenuHighlighting(msg)
	if returnEarly {
		return m, cmd
	}

	if m.state == stateHelp {
		return m.handleHelpState(msg)
	}

	if m.state == stateFilter {
		return m.handleFilterState(msg)
	}

	if m.state == stateNew {
		// Handle quit commands first. Don't handle q because the user might want to type that.
		if msg.String() == "ctrl+c" {
			m.state = stateDefault
			m.promptAfterName = false
			m.list.Kill()
			return m, tea.Sequence(
				tea.WindowSize(),
				func() tea.Msg {
					m.menu.SetState(ui.StateDefault)
					return nil
				},
			)
		}

		instance := m.list.GetInstances()[m.list.NumInstances()-1]
		switch msg.Type {
		// Start the instance (enable previews etc) and go back to the main menu state.
		case tea.KeyEnter:
			if len(instance.Title) == 0 {
				return m, m.handleError(fmt.Errorf("title cannot be empty"))
			}

			// If promptAfterName, show prompt+branch overlay before starting
			if m.promptAfterName {
				m.promptAfterName = false
				m.state = statePrompt
				m.menu.SetState(ui.StatePrompt)
				m.textInputOverlay = m.newPromptOverlay()
				// Fetch the picked repo, then run the initial branch search (no debounce, version 0)
				repo := instance.Path
				version := m.textInputOverlay.BranchFilterVersion()
				initialSearch := func() tea.Msg {
					git.FetchBranches(repo)
					return m.runBranchSearch("", version)()
				}
				return m, tea.Batch(tea.WindowSize(), initialSearch)
			}

			// Set Loading status and finalize into the list immediately
			instance.SetStatus(session.Loading)
			m.newInstanceFinalizer()
			m.promptAfterName = false
			m.state = stateDefault
			m.menu.SetState(ui.StateDefault)

			// Return a tea.Cmd that runs instance.Start in the background
			startCmd := func() tea.Msg {
				err := instance.Start(true)
				return instanceStartedMsg{
					instance:        instance,
					err:             err,
					promptAfterName: false,
				}
			}

			return m, tea.Batch(tea.WindowSize(), m.instanceChanged(), startCmd)
		case tea.KeyRunes:
			if runewidth.StringWidth(instance.Title) >= 32 {
				return m, m.handleError(fmt.Errorf("title cannot be longer than 32 characters"))
			}
			if err := instance.SetTitle(instance.Title + string(msg.Runes)); err != nil {
				return m, m.handleError(err)
			}
		case tea.KeyBackspace:
			runes := []rune(instance.Title)
			if len(runes) == 0 {
				return m, nil
			}
			if err := instance.SetTitle(string(runes[:len(runes)-1])); err != nil {
				return m, m.handleError(err)
			}
		case tea.KeySpace:
			if err := instance.SetTitle(instance.Title + " "); err != nil {
				return m, m.handleError(err)
			}
		case tea.KeyTab:
			instance.InPlace = !instance.InPlace
			return m, m.instanceChanged()
		case tea.KeyLeft, tea.KeyRight:
			if len(m.repoChoices) > 1 {
				step := 1
				if msg.Type == tea.KeyLeft {
					step = len(m.repoChoices) - 1
				}
				m.repoIdx = (m.repoIdx + step) % len(m.repoChoices)
				instance.Path = m.repoChoices[m.repoIdx]
				instance.Project = m.appConfig.GroupFor(instance.Path)
			}
			return m, m.instanceChanged()
		case tea.KeyEsc:
			m.list.Kill()
			m.state = stateDefault
			m.instanceChanged()

			return m, tea.Sequence(
				tea.WindowSize(),
				func() tea.Msg {
					m.menu.SetState(ui.StateDefault)
					return nil
				},
			)
		default:
		}
		return m, nil
	} else if m.state == statePrompt {
		// Handle cancel via ctrl+c before delegating to the overlay
		if msg.String() == "ctrl+c" {
			return m, m.cancelPromptOverlay()
		}

		// Use the new TextInputOverlay component to handle all key events
		shouldClose, branchFilterChanged := m.textInputOverlay.HandleKeyPress(msg)

		// Check if the form was submitted or canceled
		if shouldClose {
			selected := m.list.GetSelectedInstance()
			if selected == nil {
				return m, nil
			}

			if m.textInputOverlay.IsCanceled() {
				return m, m.cancelPromptOverlay()
			}

			if m.textInputOverlay.IsSubmitted() {
				prompt := m.textInputOverlay.GetValue()
				selectedBranch := m.textInputOverlay.GetSelectedBranch()
				selectedProgram := m.textInputOverlay.GetSelectedProgram()

				if !selected.Started() {
					// Shift+N flow: instance not started yet — set branch, start, then send prompt
					if selectedBranch != "" {
						selected.SetSelectedBranch(selectedBranch)
					}
					if selectedProgram != "" {
						selected.Program = selectedProgram
					}
					selected.Prompt = prompt

					// Finalize into list and start
					selected.SetStatus(session.Loading)
					m.newInstanceFinalizer()
					m.textInputOverlay = nil
					m.state = stateDefault
					m.menu.SetState(ui.StateDefault)

					startCmd := func() tea.Msg {
						err := selected.Start(true)
						return instanceStartedMsg{
							instance:        selected,
							err:             err,
							promptAfterName: false,
							selectedBranch:  selectedBranch,
						}
					}

					return m, tea.Batch(tea.WindowSize(), m.instanceChanged(), startCmd)
				}

				// Regular flow: instance already running, just send prompt
				if err := selected.SendPrompt(prompt); err != nil {
					return m, m.handleError(err)
				}
			}

			// Close the overlay and reset state
			m.textInputOverlay = nil
			m.state = stateDefault
			return m, tea.Sequence(
				tea.WindowSize(),
				func() tea.Msg {
					m.menu.SetState(ui.StateDefault)
					m.showHelpScreen(helpStart(selected), nil)
					return nil
				},
			)
		}

		// Schedule a debounced branch search if the filter changed
		if branchFilterChanged {
			filter := m.textInputOverlay.BranchFilter()
			version := m.textInputOverlay.BranchFilterVersion()
			return m, m.scheduleBranchSearch(filter, version)
		}

		return m, nil
	}

	// Handle confirmation state
	if m.state == stateConfirm {
		shouldClose := m.confirmationOverlay.HandleKeyPress(msg)
		if shouldClose {
			m.state = stateDefault
			m.confirmationOverlay = nil
			result := m.confirmResult
			m.confirmResult = nil
			// Deliver the action's outcome (an error, or a change notice) and refresh the view.
			return m, tea.Batch(func() tea.Msg { return result }, m.instanceChanged())
		}
		return m, nil
	}

	// Exit scrolling mode when ESC is pressed and preview pane is in scrolling mode
	// Check if Escape key was pressed and we're not in the diff tab (meaning we're in preview tab)
	// Always check for escape key first to ensure it doesn't get intercepted elsewhere
	if msg.Type == tea.KeyEsc {
		// If in preview tab and in scroll mode, exit scroll mode
		if m.tabbedWindow.IsInPreviewTab() && m.tabbedWindow.IsPreviewInScrollMode() {
			// Use the selected instance from the list
			selected := m.list.GetSelectedInstance()
			err := m.tabbedWindow.ResetPreviewToNormalMode(selected)
			if err != nil {
				return m, m.handleError(err)
			}
			return m, m.instanceChanged()
		}
		// If in terminal tab and in scroll mode, exit scroll mode
		if m.tabbedWindow.IsInTerminalTab() && m.tabbedWindow.IsTerminalInScrollMode() {
			m.tabbedWindow.ResetTerminalToNormalMode()
			return m, m.instanceChanged()
		}
	}

	// Handle quit commands first
	if msg.String() == "ctrl+c" || msg.String() == "q" {
		return m.handleQuit()
	}

	name, ok := keys.GlobalKeyStringsMap[msg.String()]
	if !ok {
		return m, nil
	}

	if m.sidebar != nil {
		if handled, cmd := m.handleSidebarKey(name); handled {
			return m, cmd
		}
	}

	switch name {
	case keys.KeyHelp:
		return m.showHelpScreen(helpTypeGeneral{}, nil)
	case keys.KeyPrompt:
		if limit := m.appConfig.GetMaxInstances(); m.runningCount() >= limit {
			return m, m.handleError(
				fmt.Errorf("you can't create more than %d instances", limit))
		}

		instance, err := m.newInstanceInPickedRepo()
		if err != nil {
			return m, m.handleError(err)
		}

		m.newInstanceFinalizer = m.list.AddInstance(instance)
		m.list.SetSelectedInstance(m.list.NumInstances() - 1)
		m.state = stateNew
		m.menu.SetState(ui.StateNewInstance)
		m.promptAfterName = true

		return m, m.instanceChanged()
	case keys.KeyNew:
		if limit := m.appConfig.GetMaxInstances(); m.runningCount() >= limit {
			return m, m.handleError(
				fmt.Errorf("you can't create more than %d instances", limit))
		}
		instance, err := m.newInstanceInPickedRepo()
		if err != nil {
			return m, m.handleError(err)
		}

		m.newInstanceFinalizer = m.list.AddInstance(instance)
		m.list.SetSelectedInstance(m.list.NumInstances() - 1)
		m.state = stateNew
		m.menu.SetState(ui.StateNewInstance)

		return m, m.instanceChanged()
	case keys.KeyUp:
		m.list.Up()
		return m, m.instanceChanged()
	case keys.KeyDown:
		m.list.Down()
		return m, m.instanceChanged()
	case keys.KeyShiftUp:
		m.tabbedWindow.ScrollUp()
		return m, m.instanceChanged()
	case keys.KeyShiftDown:
		m.tabbedWindow.ScrollDown()
		return m, m.instanceChanged()
	case keys.KeyTab:
		m.tabbedWindow.Toggle()
		m.menu.SetActiveTab(m.tabbedWindow.GetActiveTab())
		return m, m.instanceChanged()
	case keys.KeyKill:
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Status == session.Loading {
			return m, nil
		}
		if selected.Archived {
			return m, m.handleError(errArchived)
		}
		if selected.Adopted {
			return m, m.handleError(errNotManaged)
		}

		// Create the kill action as a tea.Cmd
		killAction := func() tea.Msg {
			// A worktree session must not be killed while its branch is checked out elsewhere.
			if worktree, err := selected.GetGitWorktree(); err == nil {
				checkedOut, err := worktree.IsBranchCheckedOut()
				if err != nil {
					return err
				}
				if checkedOut {
					return fmt.Errorf("instance %s is currently checked out", selected.Title)
				}
			}

			// Clean up terminal session and hook status for this instance
			m.tabbedWindow.CleanupTerminalForInstance(selected.Title)
			hooks.Remove(m.statusDir, selected.PaneID())

			// Delete from storage first
			if err := m.storage.DeleteInstance(selected.Title); err != nil {
				return err
			}

			// Then kill the instance
			m.list.Kill()
			return instanceChangedMsg{}
		}

		// Show confirmation modal
		message := fmt.Sprintf("[!] Kill session '%s'?", selected.Title)
		return m, m.confirmAction(message, killAction)
	case keys.KeySubmit:
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Status == session.Loading {
			return m, nil
		}
		if selected.Archived {
			return m, m.handleError(errArchived)
		}
		if selected.Adopted {
			return m, m.handleError(errNotManaged)
		}
		if selected.InPlace {
			return m, m.handleError(errInPlace)
		}

		// Create the push action as a tea.Cmd
		pushAction := func() tea.Msg {
			// Default commit message with timestamp
			commitMsg := fmt.Sprintf("[claudesquad] update from '%s' on %s", selected.Title, time.Now().Format(time.RFC822))
			worktree, err := selected.GetGitWorktree()
			if err != nil {
				return err
			}
			if err = worktree.PushChanges(commitMsg, true); err != nil {
				return err
			}
			return nil
		}

		// Show confirmation modal
		message := fmt.Sprintf("[!] Push changes from session '%s'?", selected.Title)
		return m, m.confirmAction(message, pushAction)
	case keys.KeyCheckout:
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Status == session.Loading {
			return m, nil
		}
		if selected.Archived {
			return m, m.handleError(errArchived)
		}
		if selected.Adopted {
			return m, m.handleError(errNotManaged)
		}
		if selected.InPlace {
			return m, m.handleError(errInPlace)
		}

		// Show help screen before pausing
		m.showHelpScreen(helpTypeInstanceCheckout{}, func() {
			if err := selected.Pause(); err != nil {
				m.handleError(err)
			}
			m.tabbedWindow.CleanupTerminalForInstance(selected.Title)
			m.instanceChanged()
		})
		return m, m.instanceChanged()
	case keys.KeyFold:
		m.list.ToggleFold()
		m.saveUIPrefs()
		return m, m.instanceChanged()
	case keys.KeyCollapse, keys.KeyExpand:
		if name == keys.KeyCollapse {
			m.list.Collapse()
		} else {
			m.list.Expand()
		}
		m.saveUIPrefs()
		return m, m.instanceChanged()
	case keys.KeyActiveOnly:
		m.list.CycleMode()
		m.saveUIPrefs()
		return m, m.instanceChanged()
	case keys.KeySearch:
		m.state = stateFilter
		m.menu.SetState(ui.StateFilter)
		return m, nil
	case keys.KeyNextWaiting:
		if !m.list.NextWaiting() {
			return m, m.handleError(fmt.Errorf("no session is waiting for you"))
		}
		m.saveUIPrefs()
		return m, m.instanceChanged()
	case keys.KeyToggleList:
		m.listHidden = !m.listHidden
		m.saveUIPrefs()
		return m, tea.Batch(tea.WindowSize(), m.instanceChanged())
	case keys.KeyResume:
		selected := m.list.GetSelectedInstance()
		if selected == nil || selected.Status == session.Loading {
			return m, nil
		}
		if selected.Archived {
			return m, m.resumeArchived(selected)
		}
		if selected.Adopted {
			return m, m.handleError(errNotManaged)
		}
		if err := selected.Resume(); err != nil {
			return m, m.handleError(err)
		}
		return m, tea.Batch(tea.WindowSize(), m.instanceChanged())
	case keys.KeyEnter:
		if m.list.NumInstances() == 0 {
			return m, nil
		}
		if _, onHeader := m.list.SelectedHeader(); onHeader {
			m.list.ToggleFold()
			m.saveUIPrefs()
			return m, m.instanceChanged()
		}
		selected := m.list.GetSelectedInstance()
		if selected != nil && selected.Archived {
			return m, m.resumeArchived(selected)
		}
		if selected == nil || selected.Paused() || selected.Status == session.Loading || !selected.TmuxAlive() {
			return m, nil
		}
		// Terminal tab: attach to terminal session
		if m.tabbedWindow.IsInTerminalTab() {
			m.showHelpScreen(helpTypeInstanceAttach{}, func() {
				ch, err := m.tabbedWindow.AttachTerminal()
				if err != nil {
					m.handleError(err)
					return
				}
				<-ch
				m.state = stateDefault
			})
			return m, nil
		}
		// Looking at a session clears its "turn finished" flag.
		selected.MarkSeen()
		// Show help screen before attaching
		m.showHelpScreen(helpTypeInstanceAttach{}, func() {
			ch, err := m.list.Attach()
			if err != nil {
				m.handleError(err)
				return
			}
			<-ch
			m.state = stateDefault
			m.instanceChanged()
		})
		return m, nil
	default:
		return m, nil
	}
}

// errNotManaged is shown when a managed-only action targets an adopted session.
var errNotManaged = fmt.Errorf("this session was not created by claude-squad: open it with enter")

// newInstanceInPickedRepo prepares an unnamed instance in the first known repository and
// records the alternatives the user can cycle through with left/right while naming it.
func (m *home) newInstanceInPickedRepo() (*session.Instance, error) {
	cwd, _ := os.Getwd()
	m.repoChoices = knownRepos(cwd, m.appConfig, m.list.GetInstances())
	m.repoIdx = 0
	if len(m.repoChoices) == 0 {
		return nil, fmt.Errorf("no repository to create a session in: run inside a git repository or configure projects")
	}
	instance, err := session.NewInstance(session.InstanceOptions{
		Title:   "",
		Path:    m.repoChoices[0],
		Program: m.program,
		InPlace: m.appConfig.InPlaceByDefault(),
	})
	if err != nil {
		return nil, err
	}
	instance.Project = m.appConfig.GroupFor(instance.Path)
	return instance, nil
}

// errArchived is shown when an action other than resume targets a past session.
var errArchived = fmt.Errorf("this is a past session: press enter to resume it")

// errInPlace is shown when a worktree-only action targets an in-place session.
var errInPlace = fmt.Errorf("this session runs in place: commit and push from the repository itself")

// bell rings the terminal bell. Overridable in tests.
var bell = func() { _, _ = os.Stdout.WriteString("") }

// notify signals sessions that started waiting: a bell when at least one session just
// started waiting, and the waiting count in the window title whenever it changes.
func (m *home) notify(newlyWaiting, total int) tea.Cmd {
	n := m.appConfig.GetNotifications()
	if newlyWaiting > 0 && n.Bell {
		bell()
	}
	if !n.Title || total == m.lastWaitingTotal {
		return nil
	}
	m.lastWaitingTotal = total
	title := "claude-squad"
	if total > 0 {
		title = fmt.Sprintf("claude-squad · %d waiting", total)
	}
	return tea.SetWindowTitle(title)
}

// handleMouse handles clicks and the wheel. Over the list, the wheel moves the selection, a
// click on a group header folds it, a click on a row selects it and a second click on the
// selected row opens it. Over the preview (classic layout) the wheel scrolls the pane.
func (m *home) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress || m.state != stateDefault {
		return m, nil
	}
	inList := m.sidebar != nil || (!m.listHidden && msg.X < m.listWidth)
	switch msg.Button {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		if !inList {
			selected := m.list.GetSelectedInstance()
			if selected == nil || selected.Status == session.Paused {
				return m, nil
			}
			if msg.Button == tea.MouseButtonWheelUp {
				m.tabbedWindow.ScrollUp()
			} else {
				m.tabbedWindow.ScrollDown()
			}
			return m, nil
		}
		if msg.Button == tea.MouseButtonWheelUp {
			m.list.Up()
		} else {
			m.list.Down()
		}
		return m, m.instanceChanged()
	case tea.MouseButtonLeft:
		if !inList {
			return m, nil
		}
		if mode, ok := m.list.ViewAt(msg.X, msg.Y-1); ok {
			m.list.SetMode(mode)
			m.saveUIPrefs()
			return m, m.instanceChanged()
		}
		row, ok := m.list.HitTest(msg.Y - 1) // the list is drawn under one line of padding
		if !ok {
			return m, nil
		}
		if row.Header {
			m.list.ToggleFoldProject(row.Project)
			m.saveUIPrefs()
			return m, m.instanceChanged()
		}
		again := m.lastClick == row.Instance && time.Since(m.lastClickTime) < 600*time.Millisecond
		m.lastClick, m.lastClickTime = row.Instance, time.Now()
		if row.Instance != m.list.GetSelectedInstance() || !again {
			m.list.SelectInstance(row.Instance)
			return m, m.instanceChanged()
		}
		// Second click on the selected row: open it.
		if row.Instance.Archived {
			return m, m.resumeArchived(row.Instance)
		}
		if m.sidebar != nil {
			row.Instance.MarkSeen()
			return m, m.focusSession()
		}
		return m, nil
	}
	return m, nil
}

// edgeStyle draws the right edge of the list, which floats over the session.
var edgeStyle = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#C8C8C8", Dark: "#3A3A3A"})

// withEdge pads every line of view to width-1 columns and ends it with the list's edge.
func withEdge(view string, width int) string {
	if width < 2 {
		return view
	}
	lines := strings.Split(view, "\n")
	edge := edgeStyle.Render("│")
	for i, line := range lines {
		if w := lipgloss.Width(line); w < width-1 {
			line += strings.Repeat(" ", width-1-w)
		}
		lines[i] = line + edge
	}
	return strings.Join(lines, "\n")
}

// handleFilterState edits the text filter: typed runes extend it, enter keeps it, esc
// clears it.
func (m *home) handleFilterState(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	leave := func() (tea.Model, tea.Cmd) {
		m.state = stateDefault
		m.menu.SetState(ui.StateDefault)
		return m, m.instanceChanged()
	}
	switch msg.Type {
	case tea.KeyEnter:
		return leave()
	case tea.KeyEsc, tea.KeyCtrlC:
		m.list.SetFilter("")
		return leave()
	case tea.KeyBackspace:
		if r := []rune(m.list.Filter()); len(r) > 0 {
			m.list.SetFilter(string(r[:len(r)-1]))
		}
	case tea.KeySpace:
		m.list.SetFilter(m.list.Filter() + " ")
	case tea.KeyRunes:
		m.list.SetFilter(m.list.Filter() + string(msg.Runes))
	}
	return m, m.instanceChanged()
}

// handleSidebarKey handles the keys that act on the session pane in the sidebar layout.
// It reports whether the key was consumed.
func (m *home) handleSidebarKey(name keys.KeyName) (bool, tea.Cmd) {
	selected := m.list.GetSelectedInstance()
	switch name {
	case keys.KeyHelp:
		return true, m.showPaneHelp()
	case keys.KeyToggleList:
		return true, m.focusSession()
	case keys.KeyEnter:
		if _, onHeader := m.list.SelectedHeader(); onHeader {
			m.list.ToggleFold()
			m.saveUIPrefs()
			return true, m.instanceChanged()
		}
		if selected != nil && selected.Archived {
			return true, m.resumeArchived(selected)
		}
		if selected != nil && selected.Paused() && !selected.Adopted {
			if err := selected.Resume(); err != nil {
				return true, m.handleError(err)
			}
			return true, m.focusSession()
		}
		if selected == nil || selected.Paused() || selected.Status == session.Loading || !selected.Started() {
			return true, nil
		}
		selected.MarkSeen()
		return true, m.focusSession()
	case keys.KeyTab, keys.KeyTerminalView:
		if selected == nil || !selected.Started() || selected.Paused() || selected.WorkDir() == "" {
			return true, nil
		}
		var err error
		if name == keys.KeyTab {
			err = m.sidebar.ShowDiff(selected.WorkDir(), selected.BaseCommitSHA(), selected.TmuxName())
		} else {
			err = m.sidebar.ShowShell(selected.WorkDir(), selected.TmuxName())
		}
		if err != nil {
			return true, m.handleError(err)
		}
		m.shown = selected.TmuxName() // the viewer returns to the session when it exits
		if err := m.sidebar.CloseList(); err != nil {
			return true, m.handleError(err)
		}
		return true, nil
	}
	return false, nil
}

// followDebounce is how long the selection must rest before the pane follows it.
const followDebounce = 150 * time.Millisecond

// followMsg fires when the selection has rested long enough to be shown in the pane.
type followMsg struct {
	version uint64
}

// followTarget returns the tmux session the pane should show for the current selection, or
// "" for the placeholder.
func (m *home) followTarget() string {
	sel := m.list.GetSelectedInstance()
	if sel == nil || !sel.Started() || sel.Paused() || sel.Status == session.Loading {
		return ""
	}
	return sel.TmuxName()
}

// followKey identifies what the pane should show: a tmux session name, "card:<id>" for a
// past session, or "" for the placeholder.
func (m *home) followKey() string {
	if sel := m.list.GetSelectedInstance(); sel != nil && sel.Archived {
		return "card:" + sel.ResumeID
	}
	return m.followTarget()
}

// scheduleFollow shows the selected session in the pane after a short pause, so moving
// through the list does not respawn the pane on every keystroke.
func (m *home) scheduleFollow() tea.Cmd {
	if _, onHeader := m.list.SelectedHeader(); onHeader {
		return nil // moving over a group header leaves the pane as it is
	}
	if m.followKey() == m.shown {
		return nil
	}
	m.followVersion++
	version := m.followVersion
	return func() tea.Msg {
		time.Sleep(followDebounce)
		return followMsg{version: version}
	}
}

// showSelected puts the selected session, or the placeholder, in the pane.
func (m *home) showSelected() tea.Cmd {
	if _, onHeader := m.list.SelectedHeader(); onHeader {
		return nil
	}
	key := m.followKey()
	if key == m.shown {
		return nil
	}
	var err error
	switch sel := m.list.GetSelectedInstance(); {
	case sel != nil && sel.Archived:
		err = m.sidebar.ShowText(archivedCard(sel))
	case key == "":
		err = m.sidebar.ShowPlaceholder()
	default:
		err = m.sidebar.ShowSession(key)
	}
	if err != nil {
		return m.handleError(err)
	}
	m.shown = key
	return nil
}

// focusSession shows the selected session right away and moves the cursor into its pane.
// With the drawer behaviour the session also takes the full window; moving back to the
// list (Alt+Left) unzooms it again.
func (m *home) focusSession() tea.Cmd {
	if cmd := m.showSelected(); cmd != nil {
		return cmd
	}
	if m.shown == "" {
		return nil
	}
	// Closing the list hands the keyboard to the session, which already fills the terminal.
	if err := m.sidebar.CloseList(); err != nil {
		return m.handleError(err)
	}
	return nil
}

// showPaneHelp pages the help text in the session pane.
func (m *home) showPaneHelp() tea.Cmd {
	if err := m.sidebar.ShowHelp(sidebarHelp(), m.shown); err != nil {
		return m.handleError(err)
	}
	if err := m.sidebar.CloseList(); err != nil {
		return m.handleError(err)
	}
	return nil
}

// instanceChanged updates the preview pane, menu, and diff pane based on the selected instance. It returns an error
// Cmd if there was any error.
func (m *home) instanceChanged() tea.Cmd {
	// selected may be nil
	selected := m.list.GetSelectedInstance()

	if m.sidebar != nil {
		m.menu.SetInstance(selected)
		return m.scheduleFollow()
	}

	m.tabbedWindow.UpdateDiff(selected)
	m.tabbedWindow.SetInstance(selected)
	// Update menu with current instance
	m.menu.SetInstance(selected)

	// If there's no selected instance, we don't need to update the preview.
	if err := m.tabbedWindow.UpdatePreview(selected); err != nil {
		return m.handleError(err)
	}
	if err := m.tabbedWindow.UpdateTerminal(selected); err != nil {
		return m.handleError(err)
	}
	return nil
}

type keyupMsg struct{}

// keydownCallback clears the menu option highlighting after 500ms.
func (m *home) keydownCallback(name keys.KeyName) tea.Cmd {
	m.menu.Keydown(name)
	return func() tea.Msg {
		select {
		case <-m.ctx.Done():
		case <-time.After(500 * time.Millisecond):
		}

		return keyupMsg{}
	}
}

// hideErrMsg implements tea.Msg and clears the error text from the screen.
type hideErrMsg struct{}

// previewTickMsg implements tea.Msg and triggers a preview update
type previewTickMsg struct{}

type instanceChangedMsg struct{}

type instanceStartedMsg struct {
	instance        *session.Instance
	err             error
	promptAfterName bool
	selectedBranch  string
}

// branchSearchDebounceMsg fires after the debounce interval to trigger a search.
type branchSearchDebounceMsg struct {
	filter  string
	version uint64
}

// branchSearchResultMsg carries search results back to Update.
type branchSearchResultMsg struct {
	branches []string
	version  uint64
}

const branchSearchDebounce = 150 * time.Millisecond

// scheduleBranchSearch returns a debounced tea.Cmd: sleeps, then triggers a search message.
func (m *home) scheduleBranchSearch(filter string, version uint64) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(branchSearchDebounce)
		return branchSearchDebounceMsg{filter: filter, version: version}
	}
}

// runBranchSearch returns a tea.Cmd that performs the git search in the background.
func (m *home) runBranchSearch(filter string, version uint64) tea.Cmd {
	repo, _ := os.Getwd()
	if selected := m.list.GetSelectedInstance(); selected != nil && selected.Path != "" {
		repo = selected.Path
	}
	return func() tea.Msg {
		branches, err := git.SearchBranches(repo, filter)
		if err != nil {
			log.WarningLog.Printf("branch search failed: %v", err)
			return nil
		}
		return branchSearchResultMsg{branches: branches, version: version}
	}
}

// instanceMetaResult holds the results of a single instance's metadata update,
// computed in a background goroutine.
type instanceMetaResult struct {
	instance    *session.Instance
	observation tmux.Observation
	// gone is true when the instance's tmux session no longer exists (its program exited).
	gone      bool
	hook      *hooks.Status
	diffStats *git.DiffStats
}

// metadataUpdateDoneMsg is sent when the background metadata update completes.
type metadataUpdateDoneMsg struct {
	results []instanceMetaResult
}

// instanceStartDoneMsg is sent when the background instance start completes.
type instanceStartDoneMsg struct {
	instance *session.Instance
	err      error
}

// runInstanceStartCmd returns a Cmd that performs the expensive instance.Start(true)
// in a background goroutine so the main event loop stays responsive.
func runInstanceStartCmd(instance *session.Instance) tea.Cmd {
	return func() tea.Msg {
		err := instance.Start(true)
		return instanceStartDoneMsg{instance: instance, err: err}
	}
}

// snapshotActiveInstances returns the currently active (started, not paused)
// instances. Called on the main thread so the filtering doesn't race with
// state mutations.
func (m *home) snapshotActiveInstances() []*session.Instance {
	var out []*session.Instance
	for _, inst := range m.list.GetInstances() {
		if inst.Started() && !inst.Paused() {
			out = append(out, inst)
		}
	}
	return out
}

// tickUpdateMetadataCmd returns a self-chaining Cmd that sleeps 500ms, then performs
// expensive metadata I/O (tmux capture, git diff) in parallel background goroutines.
// Because it only re-schedules after completing, overlapping ticks are impossible.
// The active instances slice should be snapshotted on the main thread via
// snapshotActiveInstances() before being passed here.
//
// Only the selected instance gets a full diff (with Content); the rest get a
// lightweight numstat-only summary. This keeps per-instance memory bounded
// since the diff pane only ever renders the selected one.
func tickUpdateMetadataCmd(active []*session.Instance, selected *session.Instance, statusDir string) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(500 * time.Millisecond)

		if len(active) == 0 {
			return metadataUpdateDoneMsg{}
		}

		results := make([]instanceMetaResult, len(active))
		var wg sync.WaitGroup
		for idx, inst := range active {
			wg.Add(1)
			go func(i int, instance *session.Instance) {
				defer wg.Done()
				r := &results[i]
				r.instance = instance
				r.observation = instance.Observe()
				r.gone = !instance.TmuxAlive()
				if statusDir != "" {
					if st, ok := hooks.Read(statusDir, instance.PaneID()); ok {
						r.hook = &st
					}
				}
				if instance == selected {
					r.diffStats = instance.ComputeDiff()
				} else {
					r.diffStats = instance.ComputeDiffNumstat()
				}
			}(idx, inst)
		}
		wg.Wait()

		return metadataUpdateDoneMsg{results: results}
	}
}

// handleError handles all errors which get bubbled up to the app. sets the error message. We return a callback tea.Cmd that returns a hideErrMsg message
// which clears the error message after 3 seconds.
func (m *home) handleError(err error) tea.Cmd {
	log.ErrorLog.Printf("%v", err)
	m.errBox.SetError(err)
	return func() tea.Msg {
		select {
		case <-m.ctx.Done():
		case <-time.After(3 * time.Second):
		}

		return hideErrMsg{}
	}
}

func (m *home) newPromptOverlay() *overlay.TextInputOverlay {
	return overlay.NewTextInputOverlayWithBranchPicker("Enter prompt", "", m.appConfig.GetProfiles())
}

// cancelPromptOverlay cancels the prompt overlay, cleaning up unstarted instances.
func (m *home) cancelPromptOverlay() tea.Cmd {
	selected := m.list.GetSelectedInstance()
	if selected != nil && !selected.Started() {
		m.list.Kill()
	}
	m.textInputOverlay = nil
	m.state = stateDefault
	return tea.Sequence(
		tea.WindowSize(),
		func() tea.Msg {
			m.menu.SetState(ui.StateDefault)
			return nil
		},
	)
}

// confirmAction shows a confirmation modal and stores the action to execute on confirm
func (m *home) confirmAction(message string, action tea.Cmd) tea.Cmd {
	m.state = stateConfirm

	// Create and show the confirmation overlay using ConfirmationOverlay
	m.confirmationOverlay = overlay.NewConfirmationOverlay(message)
	// Set a fixed width for consistent appearance, within the pane in the sidebar layout
	width := 50
	if m.sidebar != nil && m.width > 2 && m.width-2 < width {
		width = m.width - 2
	}
	m.confirmationOverlay.SetWidth(width)

	// Set callbacks for confirmation and cancellation
	m.confirmationOverlay.OnConfirm = func() {
		m.state = stateDefault
		// Execute the action if it exists; its result is delivered when the modal closes.
		if action != nil {
			m.confirmResult = action()
		}
	}

	m.confirmationOverlay.OnCancel = func() {
		m.state = stateDefault
	}

	return nil
}

func (m *home) View() string {
	previewWithPadding := lipgloss.NewStyle().PaddingTop(1).Render(m.tabbedWindow.String())
	listAndPreview := previewWithPadding
	if m.sidebar != nil {
		listAndPreview = lipgloss.NewStyle().PaddingTop(1).Render(m.list.String())
	} else if !m.listHidden {
		listWithPadding := lipgloss.NewStyle().PaddingTop(1).Render(m.list.String())
		listAndPreview = lipgloss.JoinHorizontal(lipgloss.Top, listWithPadding, previewWithPadding)
	}

	align := lipgloss.Center
	if m.sidebar != nil {
		align = lipgloss.Left // the compact menu must not be centered under a wider list
	}
	mainView := lipgloss.JoinVertical(
		align,
		listAndPreview,
		m.menu.String(),
		m.errBox.String(),
	)
	if m.sidebar != nil {
		mainView = withEdge(mainView, m.width)
	}

	if m.state == statePrompt {
		if m.textInputOverlay == nil {
			log.ErrorLog.Printf("text input overlay is nil")
		}
		return overlay.PlaceOverlay(0, 0, m.textInputOverlay.Render(), mainView, true, true)
	} else if m.state == stateHelp {
		if m.textOverlay == nil {
			log.ErrorLog.Printf("text overlay is nil")
		}
		return overlay.PlaceOverlay(0, 0, m.textOverlay.Render(), mainView, true, true)
	} else if m.state == stateConfirm {
		if m.confirmationOverlay == nil {
			log.ErrorLog.Printf("confirmation overlay is nil")
		}
		return overlay.PlaceOverlay(0, 0, m.confirmationOverlay.Render(), mainView, true, true)
	}

	return mainView
}
