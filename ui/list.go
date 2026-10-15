package ui

import (
	"claude-squad/config"
	"claude-squad/log"
	"claude-squad/session"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

const readyIcon = "● "
const pausedIcon = "⏸ "
const waitingIcon = "! "
const foldedIcon = "▸"
const unfoldedIcon = "▾"

var readyStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#51bd73", Dark: "#51bd73"})

var addedLinesStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#51bd73", Dark: "#51bd73"})

var removedLinesStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("#de613e"))

var pausedStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#888888", Dark: "#888888"})

var waitingStyle = lipgloss.NewStyle().Bold(true).
	Foreground(lipgloss.AdaptiveColor{Light: "#D08700", Dark: "#FFCC00"})

var titleStyle = lipgloss.NewStyle().
	Padding(1, 1, 0, 1).
	Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#dddddd"})

var listDescStyle = lipgloss.NewStyle().
	Padding(0, 1, 1, 1).
	Foreground(lipgloss.AdaptiveColor{Light: "#A49FA5", Dark: "#777777"})

var selectedTitleStyle = lipgloss.NewStyle().
	Padding(1, 1, 0, 1).
	Background(lipgloss.Color("#dde4f0")).
	Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#1a1a1a"})

var selectedDescStyle = lipgloss.NewStyle().
	Padding(0, 1, 1, 1).
	Background(lipgloss.Color("#dde4f0")).
	Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#1a1a1a"})

var mainTitle = lipgloss.NewStyle().
	Background(lipgloss.Color("62")).
	Foreground(lipgloss.Color("230"))

var autoYesStyle = lipgloss.NewStyle().
	Background(lipgloss.Color("#dde4f0")).
	Foreground(lipgloss.Color("#1a1a1a"))

var groupHeaderStyle = lipgloss.NewStyle().
	Bold(true).
	Padding(0, 1).
	Foreground(lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"})

var groupCountStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#A49FA5", Dark: "#777777"})

// now is overridable in tests.
var now = time.Now

type List struct {
	items         []*session.Instance
	selectedIdx   int
	height, width int
	renderer      *InstanceRenderer
	autoyes       bool

	// folded holds the projects whose sessions are hidden under their header.
	folded map[string]bool
	// projectColors maps a project name to an optional hex color for its header.
	projectColors map[string]string
}

func NewList(spinner *spinner.Model, autoYes bool) *List {
	return &List{
		items:         []*session.Instance{},
		renderer:      &InstanceRenderer{spinner: spinner},
		autoyes:       autoYes,
		folded:        make(map[string]bool),
		projectColors: make(map[string]string),
	}
}

// SetSize sets the height and width of the list.
func (l *List) SetSize(width, height int) {
	l.width = width
	l.height = height
	l.renderer.setWidth(width)
}

// SetSessionPreviewSize sets the height and width for the tmux sessions. This makes the stdout line have the correct
// width and height.
func (l *List) SetSessionPreviewSize(width, height int) (err error) {
	for i, item := range l.items {
		if !item.Started() || item.Paused() {
			continue
		}

		if innerErr := item.SetPreviewSize(width, height); innerErr != nil {
			err = errors.Join(
				err, fmt.Errorf("could not set preview size for instance %d: %v", i, innerErr))
		}
	}
	return
}

func (l *List) NumInstances() int {
	return len(l.items)
}

// SetProjectColors sets the optional header color per project name.
func (l *List) SetProjectColors(colors map[string]string) {
	l.projectColors = colors
	if l.projectColors == nil {
		l.projectColors = make(map[string]string)
	}
}

// SetFolded marks the given projects as folded.
func (l *List) SetFolded(projects []string) {
	l.folded = make(map[string]bool, len(projects))
	for _, p := range projects {
		l.folded[p] = true
	}
}

// FoldedProjects returns the folded project names, sorted.
func (l *List) FoldedProjects() []string {
	out := make([]string, 0, len(l.folded))
	for p := range l.folded {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// InstanceRenderer handles rendering of session.Instance objects
type InstanceRenderer struct {
	spinner *spinner.Model
	width   int
}

func (r *InstanceRenderer) setWidth(width int) {
	r.width = AdjustPreviewWidth(width)
}

// ɹ and ɻ are other options.
const branchIcon = "Ꮧ"

// ago formats how long ago t was, compactly ("now", "5m", "2h", "3d"). Empty for the zero time.
func ago(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now().Sub(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func (r *InstanceRenderer) Render(i *session.Instance, idx int, selected bool) string {
	prefix := fmt.Sprintf(" %d. ", idx)
	if idx >= 10 {
		prefix = prefix[:len(prefix)-1]
	}
	titleS := selectedTitleStyle
	descS := selectedDescStyle
	if !selected {
		titleS = titleStyle
		descS = listDescStyle
	}

	// add spinner next to title if it's running
	var join string
	switch i.Status {
	case session.Running, session.Loading:
		join = fmt.Sprintf("%s ", r.spinner.View())
	case session.Ready:
		join = readyStyle.Render(readyIcon)
	case session.Paused:
		join = pausedStyle.Render(pausedIcon)
	case session.Waiting:
		join = waitingStyle.Render(waitingIcon)
	default:
	}

	// Cut the title if it's too long
	titleText := i.Title
	widthAvail := r.width - 3 - runewidth.StringWidth(prefix) - 1
	if widthAvail > 0 && runewidth.StringWidth(titleText) > widthAvail {
		titleText = runewidth.Truncate(titleText, widthAvail-3, "...")
	}
	title := titleS.Render(lipgloss.JoinHorizontal(
		lipgloss.Left,
		lipgloss.Place(r.width-3, 1, lipgloss.Left, lipgloss.Center, fmt.Sprintf("%s %s", prefix, titleText)),
		" ",
		join,
	))

	stat := i.GetDiffStats()

	var diff string
	var addedDiff, removedDiff string
	if stat == nil || stat.Error != nil || stat.IsEmpty() {
		// Don't show diff stats if there's an error or if they don't exist
		addedDiff = ""
		removedDiff = ""
		diff = ""
	} else {
		addedDiff = fmt.Sprintf("+%d", stat.Added)
		removedDiff = fmt.Sprintf("-%d ", stat.Removed)
		diff = lipgloss.JoinHorizontal(
			lipgloss.Center,
			addedLinesStyle.Background(descS.GetBackground()).Render(addedDiff),
			lipgloss.Style{}.Background(descS.GetBackground()).Foreground(descS.GetForeground()).Render(","),
			removedLinesStyle.Background(descS.GetBackground()).Render(removedDiff),
		)
	}

	age := ago(i.LastActivity)
	if age != "" {
		age += " "
	}

	remainingWidth := r.width
	remainingWidth -= runewidth.StringWidth(prefix)
	remainingWidth -= runewidth.StringWidth(branchIcon)
	remainingWidth -= 2 // for the literal " " and "-" in the branchLine format string
	remainingWidth -= runewidth.StringWidth(age)

	diffWidth := runewidth.StringWidth(addedDiff) + runewidth.StringWidth(removedDiff)
	if diffWidth > 0 {
		diffWidth += 1
	}

	// Use fixed width for diff stats to avoid layout issues
	remainingWidth -= diffWidth

	branch := i.Branch
	if repo := i.RepoLabel(); repo != "" && repo != "." {
		branch += fmt.Sprintf(" (%s)", repo)
	}
	// Don't show branch if there's no space for it. Or show ellipsis if it's too long.
	branchWidth := runewidth.StringWidth(branch)
	if remainingWidth < 0 {
		branch = ""
	} else if remainingWidth < branchWidth {
		if remainingWidth < 3 {
			branch = ""
		} else {
			// We know the remainingWidth is at least 4 and branch is longer than that, so this is safe.
			branch = runewidth.Truncate(branch, remainingWidth-3, "...")
		}
	}
	remainingWidth -= runewidth.StringWidth(branch)

	// Add spaces to fill the remaining width.
	spaces := ""
	if remainingWidth > 0 {
		spaces = strings.Repeat(" ", remainingWidth)
	}

	branchLine := fmt.Sprintf("%s %s-%s%s%s%s", strings.Repeat(" ", len(prefix)), branchIcon, branch, spaces, age, diff)

	// join title and subtitle
	text := lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		descS.Render(branchLine),
	)

	return text
}

// listRow is one block of the rendered list: a project header or a session.
type listRow struct {
	header   bool
	project  string
	count    int
	waiting  int
	folded   bool
	instance *session.Instance
}

// projectOf returns the display group of an instance.
func projectOf(i *session.Instance) string {
	if i.Project == "" {
		return config.OtherProject
	}
	return i.Project
}

// statusRank orders sessions inside a group: the ones needing attention first.
func statusRank(s session.Status) int {
	switch s {
	case session.Waiting:
		return 0
	case session.Running, session.Loading:
		return 1
	case session.Ready:
		return 2
	case session.Paused:
		return 3
	}
	return 2
}

// groups returns the project names in display order (alphabetical, Other last) and the
// sessions of each group sorted by status, then most recent activity, then list order.
func (l *List) groups() ([]string, map[string][]*session.Instance) {
	byProject := make(map[string][]*session.Instance)
	order := make(map[*session.Instance]int, len(l.items))
	for idx, inst := range l.items {
		order[inst] = idx
		p := projectOf(inst)
		byProject[p] = append(byProject[p], inst)
	}

	names := make([]string, 0, len(byProject))
	for name := range byProject {
		names = append(names, name)
	}
	sort.Slice(names, func(a, b int) bool {
		if names[a] == config.OtherProject || names[b] == config.OtherProject {
			return names[b] == config.OtherProject
		}
		return names[a] < names[b]
	})

	for _, insts := range byProject {
		sort.SliceStable(insts, func(a, b int) bool {
			ra, rb := statusRank(insts[a].Status), statusRank(insts[b].Status)
			if ra != rb {
				return ra < rb
			}
			if !insts[a].LastActivity.Equal(insts[b].LastActivity) {
				return insts[a].LastActivity.After(insts[b].LastActivity)
			}
			return order[insts[a]] < order[insts[b]]
		})
	}
	return names, byProject
}

// showHeaders reports whether group headers are drawn: always, except when every session
// sits in the catch-all group, where a lone "Other" header would be noise.
func showHeaders(names []string) bool {
	return !(len(names) == 1 && names[0] == config.OtherProject)
}

// rows returns the list in display order: headers followed by their sessions, with folded
// groups reduced to their header.
func (l *List) rows() []listRow {
	names, byProject := l.groups()
	headers := showHeaders(names)
	var rows []listRow
	for _, name := range names {
		insts := byProject[name]
		folded := headers && l.folded[name]
		if headers {
			waiting := 0
			for _, inst := range insts {
				if inst.NeedsAttention() {
					waiting++
				}
			}
			rows = append(rows, listRow{header: true, project: name, count: len(insts), waiting: waiting, folded: folded})
		}
		if folded {
			continue
		}
		for _, inst := range insts {
			rows = append(rows, listRow{project: name, instance: inst})
		}
	}
	return rows
}

// ordered returns every session in display order, folded or not.
func (l *List) ordered() []*session.Instance {
	names, byProject := l.groups()
	var out []*session.Instance
	for _, name := range names {
		out = append(out, byProject[name]...)
	}
	return out
}

// visible returns the sessions currently drawn, in display order.
func (l *List) visible() []*session.Instance {
	var out []*session.Instance
	for _, r := range l.rows() {
		if !r.header {
			out = append(out, r.instance)
		}
	}
	return out
}

func indexOf(insts []*session.Instance, target *session.Instance) int {
	for i, inst := range insts {
		if inst == target {
			return i
		}
	}
	return -1
}

func (l *List) renderHeader(r listRow) string {
	icon := unfoldedIcon
	if r.folded {
		icon = foldedIcon
	}
	style := groupHeaderStyle
	if c := l.projectColors[r.project]; c != "" {
		style = style.Foreground(lipgloss.Color(c))
	}
	parts := []string{
		style.Render(fmt.Sprintf("%s %s", icon, r.project)),
		groupCountStyle.Render(fmt.Sprintf("%d", r.count)),
	}
	if r.waiting > 0 {
		parts = append(parts, waitingStyle.Render(fmt.Sprintf(" · %d waiting", r.waiting)))
	}
	return lipgloss.JoinHorizontal(lipgloss.Left, parts...)
}

func (l *List) String() string {
	const titleText = " Instances "
	const autoYesText = " auto-yes "

	// Write the title.
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("\n")

	// Write title line
	// add padding of 2 because the border on list items adds some extra characters
	titleWidth := AdjustPreviewWidth(l.width) + 2
	if !l.autoyes {
		b.WriteString(lipgloss.Place(
			titleWidth, 1, lipgloss.Left, lipgloss.Bottom, mainTitle.Render(titleText)))
	} else {
		title := lipgloss.Place(
			titleWidth/2, 1, lipgloss.Left, lipgloss.Bottom, mainTitle.Render(titleText))
		autoYes := lipgloss.Place(
			titleWidth-(titleWidth/2), 1, lipgloss.Right, lipgloss.Bottom, autoYesStyle.Render(autoYesText))
		b.WriteString(lipgloss.JoinHorizontal(
			lipgloss.Top, title, autoYes))
	}

	b.WriteString("\n")
	b.WriteString("\n")

	// Render the rows. Sessions are separated by a blank line; a header sits right above
	// its first session.
	selected := l.GetSelectedInstance()
	rows := l.rows()
	n := 0
	for i, r := range rows {
		if r.header {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(l.renderHeader(r))
			b.WriteString("\n")
			continue
		}
		n++
		if i > 0 && !rows[i-1].header {
			b.WriteString("\n")
		}
		b.WriteString(l.renderer.Render(r.instance, n, r.instance == selected))
		b.WriteString("\n")
	}
	return lipgloss.Place(l.width, l.height, lipgloss.Left, lipgloss.Top, b.String())
}

// step moves the selection by delta positions through the visible sessions, wrapping.
func (l *List) step(delta int) {
	vis := l.visible()
	if len(vis) == 0 {
		return
	}
	pos := indexOf(vis, l.GetSelectedInstance())
	if pos == -1 {
		l.SelectInstance(vis[0])
		return
	}
	l.SelectInstance(vis[(pos+delta+len(vis))%len(vis)])
}

// Down selects the next visible session.
func (l *List) Down() {
	l.step(1)
}

// Up selects the previous visible session.
func (l *List) Up() {
	l.step(-1)
}

// ToggleFold folds or unfolds the selected session's project group. When folding hides the
// selection, the next visible session is selected instead.
func (l *List) ToggleFold() {
	cur := l.GetSelectedInstance()
	if cur == nil {
		return
	}
	project := projectOf(cur)
	if l.folded[project] {
		delete(l.folded, project)
		return
	}
	// Pick the replacement before folding, searching forward with wrap-around.
	ordered := l.ordered()
	pos := indexOf(ordered, cur)
	var next *session.Instance
	for i := 1; i < len(ordered); i++ {
		cand := ordered[(pos+i)%len(ordered)]
		if p := projectOf(cand); p != project && !l.folded[p] {
			next = cand
			break
		}
	}
	l.folded[project] = true
	if next != nil {
		l.SelectInstance(next)
	}
}

// Kill removes the selected session and selects its visible neighbour.
func (l *List) Kill() {
	if len(l.items) == 0 {
		return
	}
	targetInstance := l.items[l.selectedIdx]

	// Pick the replacement in display order: the next visible session, else the previous one.
	vis := l.visible()
	var replacement *session.Instance
	if pos := indexOf(vis, targetInstance); pos != -1 && len(vis) > 1 {
		if pos+1 < len(vis) {
			replacement = vis[pos+1]
		} else {
			replacement = vis[pos-1]
		}
	}

	// Kill the tmux session
	if err := targetInstance.Kill(); err != nil {
		log.ErrorLog.Printf("could not kill instance: %v", err)
	}

	l.items = append(l.items[:l.selectedIdx], l.items[l.selectedIdx+1:]...)
	if replacement != nil {
		l.SelectInstance(replacement)
	} else if l.selectedIdx >= len(l.items) {
		l.selectedIdx = 0
	}
}

func (l *List) Attach() (chan struct{}, error) {
	targetInstance := l.items[l.selectedIdx]
	return targetInstance.Attach()
}

// AddInstance adds a new instance to the list. It returns a finalizer function that should be called when the instance
// is started. If the instance was restored from storage or is paused, you can call the finalizer immediately.
// When creating a new one and entering the name, you want to call the finalizer once the name is done.
func (l *List) AddInstance(instance *session.Instance) (finalize func()) {
	l.items = append(l.items, instance)
	// A new session must be visible, so unfold its group.
	delete(l.folded, projectOf(instance))
	return func() {
		if instance.LastActivity.IsZero() {
			instance.LastActivity = now()
		}
	}
}

// GetSelectedInstance returns the currently selected instance
func (l *List) GetSelectedInstance() *session.Instance {
	if len(l.items) == 0 || l.selectedIdx < 0 || l.selectedIdx >= len(l.items) {
		return nil
	}
	return l.items[l.selectedIdx]
}

// SetSelectedInstance sets the selected index. Noop if the index is out of bounds.
func (l *List) SetSelectedInstance(idx int) {
	if idx < 0 || idx >= len(l.items) {
		return
	}
	l.selectedIdx = idx
}

// SelectInstance finds and selects the given instance in the list.
func (l *List) SelectInstance(target *session.Instance) {
	for i, inst := range l.items {
		if inst == target {
			l.SetSelectedInstance(i)
			return
		}
	}
}

// MoveUp swaps the selected instance with the one above it in storage order. Storage order
// is the final tie-breaker of the grouped view.
func (l *List) MoveUp() bool {
	if l.selectedIdx <= 0 || len(l.items) < 2 {
		return false
	}
	l.items[l.selectedIdx], l.items[l.selectedIdx-1] = l.items[l.selectedIdx-1], l.items[l.selectedIdx]
	l.selectedIdx--
	return true
}

// MoveDown swaps the selected instance with the one below it in storage order.
func (l *List) MoveDown() bool {
	if l.selectedIdx >= len(l.items)-1 || len(l.items) < 2 {
		return false
	}
	l.items[l.selectedIdx], l.items[l.selectedIdx+1] = l.items[l.selectedIdx+1], l.items[l.selectedIdx]
	l.selectedIdx++
	return true
}

// GetInstances returns all instances in the list
func (l *List) GetInstances() []*session.Instance {
	return l.items
}
