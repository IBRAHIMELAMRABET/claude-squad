package ui

import (
	"claude-squad/config"
	"claude-squad/log"
	"claude-squad/session"
	"errors"
	"fmt"
	"path/filepath"
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
const stoppedIcon = "○ "

// archivedIcon marks past sessions that can be resumed.
const archivedIcon = "↺"
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

var badgeStyle = lipgloss.NewStyle().
	Background(lipgloss.Color("62")).
	Foreground(lipgloss.Color("230"))

var groupHeaderStyle = lipgloss.NewStyle().
	Bold(true).
	Padding(0, 1).
	Foreground(lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"})

var groupCountStyle = lipgloss.NewStyle().
	Foreground(lipgloss.AdaptiveColor{Light: "#A49FA5", Dark: "#777777"})

var moreStyle = lipgloss.NewStyle().
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
	// offset is the first block (header or session) drawn, so the selection stays visible
	// when the list is taller than the pane.
	offset int
	// layout records which line of the last render belongs to which row, for mouse clicks.
	layout []rowSpan
	// compact draws the sidebar layout's rows: no title badge, no numbering, no padding.
	compact bool

	// headerCursor is the key of the group header the cursor is on, or "" when it is on a
	// session. GetSelectedInstance returns nil while the cursor is on a header.
	headerCursor string

	// mode selects which sessions are shown (all, active, waiting).
	mode ViewMode
	// labelSegments is how many trailing path segments name a folder group.
	labelSegments int
	// modeSpans records where each view of the filter bar was drawn, for mouse clicks.
	modeSpans []modeSpan
	// filter is a case-insensitive substring matched against title, project, repo and branch.
	filter string
}

func NewList(spinner *spinner.Model, autoYes bool) *List {
	return &List{
		items:         []*session.Instance{},
		renderer:      &InstanceRenderer{spinner: spinner},
		autoyes:       autoYes,
		folded:        make(map[string]bool),
		projectColors: make(map[string]string),
		labelSegments: config.DefaultGroupLabelSegments,
	}
}

// SetSize sets the height and width of the list.
func (l *List) SetSize(width, height int) {
	l.width = width
	l.height = height
	if l.compact {
		l.renderer.width = width - 1
	} else {
		l.renderer.setWidth(width)
	}
}

// SetCompact switches to the compact rows of the sidebar layout.
func (l *List) SetCompact(on bool) {
	l.compact = on
	l.SetSize(l.width, l.height)
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

// ViewMode selects which sessions the list shows.
type ViewMode int

const (
	// ViewAll shows every session, running or past.
	ViewAll ViewMode = iota
	// ViewActive shows live sessions only: running, idle or waiting, not paused or past.
	ViewActive
	// ViewWaiting shows the sessions waiting for the user.
	ViewWaiting
)

var viewNames = []string{"All", "Active", "Waiting"}

func (v ViewMode) String() string {
	if v < 0 || int(v) >= len(viewNames) {
		return viewNames[0]
	}
	return viewNames[v]
}

// ParseViewMode maps a saved name back to a view mode, defaulting to ViewAll.
func ParseViewMode(name string) ViewMode {
	for i, n := range viewNames {
		if strings.EqualFold(n, name) {
			return ViewMode(i)
		}
	}
	return ViewAll
}

// SetMode selects which sessions are shown.
func (l *List) SetMode(mode ViewMode) {
	l.mode = mode
	l.ensureVisibleSelection()
}

// Mode returns which sessions are shown.
func (l *List) Mode() ViewMode {
	return l.mode
}

// CycleMode switches to the next view: All, Active, Waiting, then All again.
func (l *List) CycleMode() {
	l.SetMode((l.mode + 1) % ViewMode(len(viewNames)))
}

// SetActiveOnly switches between the Active and All views.
func (l *List) SetActiveOnly(on bool) {
	if on {
		l.SetMode(ViewActive)
	} else {
		l.SetMode(ViewAll)
	}
}

// ActiveOnly reports whether the Active view is selected.
func (l *List) ActiveOnly() bool {
	return l.mode == ViewActive
}

// SetLabelSegments sets how many trailing path segments name a folder group.
func (l *List) SetLabelSegments(n int) {
	if n < 1 {
		n = 1
	}
	l.labelSegments = n
}

// isLive reports whether a session is running (or idle, or waiting), as opposed to paused
// or only known from its transcript.
func isLive(i *session.Instance) bool {
	return !i.Archived && !i.Paused() && i.Status != session.Stopped
}

// inView reports whether a session belongs to a view.
func inView(i *session.Instance, mode ViewMode) bool {
	switch mode {
	case ViewActive:
		return isLive(i)
	case ViewWaiting:
		return i.NeedsAttention()
	}
	return true
}

// viewCount returns how many sessions a view holds, ignoring the text filter.
func (l *List) viewCount(mode ViewMode) int {
	n := 0
	for _, inst := range l.items {
		if inView(inst, mode) {
			n++
		}
	}
	return n
}

// SetFilter sets the text filter; empty clears it.
func (l *List) SetFilter(text string) {
	l.filter = text
	l.ensureVisibleSelection()
}

// Filter returns the current text filter.
func (l *List) Filter() string {
	return l.filter
}

// matches reports whether the instance passes the active and text filters.
func (l *List) matches(i *session.Instance) bool {
	if !inView(i, l.mode) {
		return false
	}
	if l.filter == "" {
		return true
	}
	needle := strings.ToLower(l.filter)
	for _, hay := range []string{i.DisplayTitle(), i.Title, groupLabel(groupKey(i), l.labelSegments), i.RepoLabel(), i.Branch} {
		if strings.Contains(strings.ToLower(hay), needle) {
			return true
		}
	}
	return false
}

// WaitingCount returns how many listed sessions are waiting for the user.
func (l *List) WaitingCount() int {
	n := 0
	for _, inst := range l.items {
		if l.matches(inst) && inst.NeedsAttention() {
			n++
		}
	}
	return n
}

// ensureVisibleSelection moves the selection to the first visible session when the
// current one is filtered out or folded away. A cursor on a header stays there while that
// header is drawn.
func (l *List) ensureVisibleSelection() {
	if l.headerCursor != "" {
		for _, r := range l.rows() {
			if r.header && r.project == l.headerCursor {
				return
			}
		}
		l.headerCursor = ""
	}
	vis := l.visible()
	if len(vis) == 0 || indexOf(vis, l.GetSelectedInstance()) != -1 {
		return
	}
	l.SelectInstance(vis[0])
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

// adoptedIcon marks sessions found in tmux that claude-squad did not create.
const adoptedIcon = "⇄"

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
	case session.Stopped:
		join = pausedStyle.Render(stoppedIcon)
	default:
	}

	// Cut the title if it's too long
	titleText := i.DisplayTitle()
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
		age = " " + age + " "
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
	icon := branchIcon
	if i.Adopted {
		icon = adoptedIcon
		branch = "tmux"
	}
	if i.Archived {
		icon = archivedIcon
		branch = "resume"
	}
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

	branchLine := fmt.Sprintf("%s %s-%s%s%s%s", strings.Repeat(" ", len(prefix)), icon, branch, spaces, age, diff)

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
	header bool
	// project is the group key (see groupKey); name is the label drawn for it.
	project  string
	name     string
	count    int
	waiting  int
	folded   bool
	instance *session.Instance
}

// groupKey returns the key of an instance's group: its project name, else the path of its
// folder (sessions no project claims are grouped by folder), else the catch-all name.
func groupKey(i *session.Instance) string {
	if i.Project != "" {
		return i.Project
	}
	if p := config.FolderGroupPath(i.ProjectPath()); p != "" {
		return p
	}
	return config.OtherProject
}

// isFolderKey reports whether a group key is a folder path rather than a project name.
func isFolderKey(key string) bool {
	return filepath.IsAbs(key)
}

// groupLabel is the label of a group: the project name, or the folder's name with the
// given number of path segments.
func groupLabel(key string, segments int) string {
	if !isFolderKey(key) {
		return key
	}
	return config.FolderGroupName(key, segments)
}

// groupLabels names every group, adding parent folders to folder groups whose name would
// otherwise collide with another group's.
func groupLabels(keys []string, base int) map[string]string {
	labels := make(map[string]string, len(keys))
	segments := make(map[string]int, len(keys))
	for _, k := range keys {
		segments[k] = base
		labels[k] = groupLabel(k, base)
	}
	for round := 0; round < 8; round++ {
		count := make(map[string]int, len(keys))
		for _, k := range keys {
			count[strings.ToLower(labels[k])]++
		}
		changed := false
		for _, k := range keys {
			if count[strings.ToLower(labels[k])] < 2 || !isFolderKey(k) {
				continue
			}
			segments[k]++
			if next := groupLabel(k, segments[k]); next != labels[k] {
				labels[k] = next
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return labels
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
	case session.Stopped:
		return 4
	}
	return 2
}

// groups returns the group keys in display order (named projects, then folder groups, each
// alphabetically by label), the sessions of each group sorted by status, then most recent
// activity, then list order, and the label of each group.
func (l *List) groups() ([]string, map[string][]*session.Instance, map[string]string) {
	byProject := make(map[string][]*session.Instance)
	order := make(map[*session.Instance]int, len(l.items))
	for idx, inst := range l.items {
		order[inst] = idx
		if !l.matches(inst) {
			continue
		}
		p := groupKey(inst)
		byProject[p] = append(byProject[p], inst)
	}

	names := make([]string, 0, len(byProject))
	for name := range byProject {
		names = append(names, name)
	}
	labels := groupLabels(names, l.labelSegments)
	sort.Slice(names, func(a, b int) bool {
		if fa, fb := isFolderKey(names[a]), isFolderKey(names[b]); fa != fb {
			return fb
		}
		return strings.ToLower(labels[names[a]]) < strings.ToLower(labels[names[b]])
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
	return names, byProject, labels
}

// showHeaders reports whether group headers are drawn: always, except when every session
// sits in the catch-all group with no path to name it, where a lone header would be noise.
func showHeaders(names []string) bool {
	return !(len(names) == 1 && names[0] == config.OtherProject)
}

// rows returns the list in display order: headers followed by their sessions, with folded
// groups reduced to their header.
func (l *List) rows() []listRow {
	names, byProject, labels := l.groups()
	headers := showHeaders(names)
	var rows []listRow
	for _, name := range names {
		insts := byProject[name]
		// A text filter shows its matches even in folded groups; the folds come back when it
		// is cleared.
		folded := headers && l.folded[name] && l.filter == ""
		if headers {
			waiting := 0
			for _, inst := range insts {
				if inst.NeedsAttention() {
					waiting++
				}
			}
			rows = append(rows, listRow{header: true, project: name, name: labels[name], count: len(insts), waiting: waiting, folded: folded})
		}
		if folded {
			continue
		}
		for _, inst := range insts {
			rows = append(rows, listRow{project: name, name: labels[name], instance: inst})
		}
	}
	return rows
}

// headersShown reports whether group headers are currently drawn.
func (l *List) headersShown() bool {
	names, _, _ := l.groups()
	return showHeaders(names)
}

// ordered returns every session in display order, folded or not.
func (l *List) ordered() []*session.Instance {
	names, byProject, _ := l.groups()
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

func (l *List) renderHeader(r listRow, selected bool) string {
	icon := unfoldedIcon
	if r.folded {
		icon = foldedIcon
	}
	style := groupHeaderStyle
	if c := l.projectColors[r.project]; c != "" {
		style = style.Foreground(lipgloss.Color(c))
	}
	count := fmt.Sprintf(" %d", r.count)
	waiting := ""
	if r.waiting > 0 {
		waiting = fmt.Sprintf(" · %d waiting", r.waiting)
	}
	// Fit the name to the pane: " ▾ name count · n waiting".
	name := r.name
	if name == "" {
		name = r.project
	}
	if l.width > 0 {
		avail := l.width - 1 - runewidth.StringWidth(icon) - 1 - runewidth.StringWidth(count) - runewidth.StringWidth(waiting) - 1
		if avail > 3 && runewidth.StringWidth(name) > avail {
			name = runewidth.Truncate(name, avail-1, "…")
		}
	}
	if selected {
		// The cursor is on this header: draw it as a highlighted line.
		return compactSelectedStyle.Bold(true).Render(fitLeft(fmt.Sprintf(" %s %s%s%s", icon, name, count, waiting), l.width-1))
	}
	parts := []string{
		style.Render(fmt.Sprintf("%s %s", icon, name)),
		groupCountStyle.Render(count),
	}
	if waiting != "" {
		parts = append(parts, waitingStyle.Render(waiting))
	}
	return lipgloss.JoinHorizontal(lipgloss.Left, parts...)
}

func (l *List) String() string {
	const titleText = " Instances "
	const autoYesText = " auto-yes "

	var b strings.Builder
	if l.compact {
		// The filter bar: All, Active and Waiting with their counts, the current one
		// highlighted; then the text filter, when one is set.
		b.WriteString(l.renderModeBar() + "\n")
		if l.filter != "" {
			b.WriteString(" " + moreStyle.Render("/"+l.filter) + "\n")
		}
	} else {
		// Write the title.
		b.WriteString("\n")
		b.WriteString("\n")

		// Write title line: the title, then badges for the active filters and the number of
		// sessions waiting for the user.
		// add padding of 2 because the border on list items adds some extra characters
		titleWidth := AdjustPreviewWidth(l.width) + 2
		left := mainTitle.Render(titleText)
		if l.mode != ViewAll {
			left += " " + badgeStyle.Render(" "+strings.ToLower(l.mode.String())+" ")
		}
		if l.filter != "" {
			left += " " + badgeStyle.Render(" /"+l.filter+" ")
		}
		if n := l.WaitingCount(); n > 0 {
			left += " " + waitingStyle.Render(fmt.Sprintf("%d waiting", n))
		}
		if !l.autoyes {
			b.WriteString(lipgloss.Place(
				titleWidth, 1, lipgloss.Left, lipgloss.Bottom, left))
		} else {
			title := lipgloss.Place(
				titleWidth/2, 1, lipgloss.Left, lipgloss.Bottom, left)
			autoYes := lipgloss.Place(
				titleWidth-(titleWidth/2), 1, lipgloss.Right, lipgloss.Bottom, autoYesStyle.Render(autoYesText))
			b.WriteString(lipgloss.JoinHorizontal(
				lipgloss.Top, title, autoYes))
		}

		b.WriteString("\n")
		b.WriteString("\n")
	}

	// Render the rows as blocks (a header, or a session with its blank separator), then
	// draw the window of blocks that fits the pane and contains the selection.
	selected := l.GetSelectedInstance()
	rows := l.rows()
	blocks := make([]string, len(rows))
	heights := make([]int, len(rows))
	selIdx := -1
	n := 0
	for i, r := range rows {
		var text string
		if r.header {
			if i > 0 {
				text = "\n"
			}
			onHeader := l.headerCursor != "" && r.project == l.headerCursor
			text += l.renderHeader(r, onHeader) + "\n"
			if onHeader {
				selIdx = i
			}
		} else {
			n++
			if l.compact {
				text = l.renderer.RenderCompact(r.instance, r.instance == selected) + "\n"
			} else {
				if i > 0 && !rows[i-1].header {
					text = "\n"
				}
				text += l.renderer.Render(r.instance, n, r.instance == selected) + "\n"
			}
			if r.instance == selected {
				selIdx = i
			}
		}
		blocks[i], heights[i] = text, strings.Count(text, "\n")
	}

	titleLines := strings.Count(b.String(), "\n")
	start, end := l.window(heights, selIdx, l.height-titleLines)
	hiddenAbove, hiddenBelow := countSessions(rows[:start]), countSessions(rows[end:])
	line := titleLines
	if hiddenAbove > 0 {
		b.WriteString(moreStyle.Render(fmt.Sprintf("  ↑ %d more", hiddenAbove)) + "\n")
		line++
	}
	l.layout = l.layout[:0]
	for i := start; i < end; i++ {
		b.WriteString(blocks[i])
		l.layout = append(l.layout, rowSpan{start: line, end: line + heights[i], row: rows[i]})
		line += heights[i]
	}
	if hiddenBelow > 0 {
		b.WriteString(moreStyle.Render(fmt.Sprintf("  ↓ %d more", hiddenBelow)) + "\n")
	}
	return lipgloss.Place(l.width, l.height, lipgloss.Left, lipgloss.Top, b.String())
}

// modeSpan is the column range [start, end) of one view in the filter bar (line 0).
type modeSpan struct {
	start, end int
	mode       ViewMode
}

// renderModeBar draws " All 350  Active 2  Waiting 0" with the current view highlighted,
// recording where each view was drawn. Labels shrink when the pane is too narrow.
func (l *List) renderModeBar() string {
	labels := viewNames
	render := func(names []string) (string, []modeSpan) {
		var b strings.Builder
		var spans []modeSpan
		col := 1
		b.WriteString(" ")
		for i, name := range names {
			mode := ViewMode(i)
			text := fmt.Sprintf("%s %d", name, l.viewCount(mode))
			if i > 0 {
				b.WriteString("  ")
				col += 2
			}
			if mode == l.mode {
				text = " " + text + " "
				b.WriteString(badgeStyle.Render(text))
			} else if mode == ViewWaiting && l.viewCount(mode) > 0 {
				b.WriteString(waitingStyle.Render(text))
			} else {
				b.WriteString(moreStyle.Render(text))
			}
			w := runewidth.StringWidth(text)
			spans = append(spans, modeSpan{start: col, end: col + w, mode: mode})
			col += w
		}
		return b.String(), spans
	}
	bar, spans := render(labels)
	if l.width > 0 && spans[len(spans)-1].end > l.width {
		bar, spans = render([]string{"All", "Act", "Wait"})
	}
	l.modeSpans = spans
	return bar
}

// ViewAt returns the view drawn at column x of the given line (0 = the list's first line),
// as of the last render.
func (l *List) ViewAt(x, line int) (ViewMode, bool) {
	if !l.compact || line != 0 {
		return ViewAll, false
	}
	for _, s := range l.modeSpans {
		if x >= s.start && x < s.end {
			return s.mode, true
		}
	}
	return ViewAll, false
}

// rowSpan is the line range [start, end) a drawn row occupied in the last render.
type rowSpan struct {
	start, end int
	row        listRow
}

// Hit is what a mouse click landed on: a group header, or a session.
type Hit struct {
	Header   bool
	Project  string
	Instance *session.Instance
}

// HitTest returns the row drawn at the given line of the list (0 = the list's first line),
// as of the last render.
func (l *List) HitTest(line int) (Hit, bool) {
	for _, s := range l.layout {
		if line >= s.start && line < s.end {
			return Hit{Header: s.row.header, Project: s.row.project, Instance: s.row.instance}, true
		}
	}
	return Hit{}, false
}

// window picks the blocks [start, end) to draw in avail lines: it scrolls just enough for
// the selected block (and the header right above it) to be visible, remembering the offset
// between renders so the list does not jump.
func (l *List) window(heights []int, selIdx, avail int) (start, end int) {
	if len(heights) == 0 || avail <= 0 {
		return 0, 0
	}
	if l.offset >= len(heights) {
		l.offset = len(heights) - 1
	}
	if selIdx >= 0 {
		if selIdx < l.offset {
			l.offset = selIdx
		}
		for l.offset < selIdx && sum(heights[l.offset:selIdx+1]) > avail-1 {
			l.offset++
		}
		// Keep the group header above the selection in view when there is room.
		if l.offset == selIdx && selIdx > 0 && heights[selIdx-1] <= 2 && sum(heights[selIdx-1:selIdx+1]) <= avail-1 {
			l.offset = selIdx - 1
		}
	}
	start = l.offset
	used := 0
	if start > 0 {
		used++ // the "more above" line
	}
	end = start
	for end < len(heights) && used+heights[end] <= avail {
		used += heights[end]
		end++
	}
	if end < len(heights) {
		// Make room for the "more below" line.
		for end > start+1 && used+1 > avail {
			end--
			used -= heights[end]
		}
	}
	if end <= start {
		end = start + 1
	}
	return start, end
}

func sum(xs []int) int {
	t := 0
	for _, x := range xs {
		t += x
	}
	return t
}

func countSessions(rows []listRow) int {
	n := 0
	for _, r := range rows {
		if !r.header {
			n++
		}
	}
	return n
}

// selectedItem is the session the cursor was last on, even while it sits on a header.
func (l *List) selectedItem() *session.Instance {
	if l.selectedIdx < 0 || l.selectedIdx >= len(l.items) {
		return nil
	}
	return l.items[l.selectedIdx]
}

// cursorRow returns the index in rows of the row under the cursor, or -1.
func (l *List) cursorRow(rows []listRow) int {
	sel := l.selectedItem()
	for i, r := range rows {
		if l.headerCursor != "" {
			if r.header && r.project == l.headerCursor {
				return i
			}
			continue
		}
		if !r.header && r.instance == sel {
			return i
		}
	}
	return -1
}

// moveTo puts the cursor on a row.
func (l *List) moveTo(r listRow) {
	if r.header {
		l.headerCursor = r.project
		return
	}
	l.SelectInstance(r.instance)
}

// step moves the cursor by delta rows, headers included, wrapping around. When the selected
// session is hidden in a folded group, the cursor first lands on that group's header.
func (l *List) step(delta int) {
	rows := l.rows()
	if len(rows) == 0 {
		return
	}
	pos := l.cursorRow(rows)
	if pos == -1 {
		if sel := l.selectedItem(); sel != nil {
			key := groupKey(sel)
			for _, r := range rows {
				if r.header && r.project == key {
					l.moveTo(r)
					return
				}
			}
		}
		l.moveTo(rows[0])
		return
	}
	l.moveTo(rows[(pos+delta+len(rows))%len(rows)])
}

// Down moves the cursor to the next row.
func (l *List) Down() {
	l.step(1)
}

// Up moves the cursor to the previous row.
func (l *List) Up() {
	l.step(-1)
}

// SelectedHeader returns the key of the group header under the cursor, if any.
func (l *List) SelectedHeader() (string, bool) {
	return l.headerCursor, l.headerCursor != ""
}

// ToggleFold folds or unfolds the group under the cursor. Folding the group of the selected
// session moves the cursor onto the group's header, so the same key unfolds it again.
func (l *List) ToggleFold() {
	if key, ok := l.SelectedHeader(); ok {
		l.ToggleFoldProject(key)
		return
	}
	cur := l.GetSelectedInstance()
	if cur == nil {
		return
	}
	key := groupKey(cur)
	if l.folded[key] {
		delete(l.folded, key)
		return
	}
	if !l.headersShown() {
		return // nothing to fold into
	}
	l.folded[key] = true
	l.headerCursor = key
}

// Collapse folds the group under the cursor (moving onto its header when on a session).
func (l *List) Collapse() {
	if key, ok := l.SelectedHeader(); ok {
		if !l.folded[key] {
			l.folded[key] = true
		}
		return
	}
	if cur := l.GetSelectedInstance(); cur != nil && !l.folded[groupKey(cur)] {
		l.ToggleFold()
	}
}

// Expand unfolds the group whose header is under the cursor; on an unfolded header it
// moves to the group's first session.
func (l *List) Expand() {
	key, ok := l.SelectedHeader()
	if !ok {
		return
	}
	if l.folded[key] {
		delete(l.folded, key)
		return
	}
	for _, r := range l.rows() {
		if !r.header && r.project == key {
			l.SelectInstance(r.instance)
			return
		}
	}
}

// ToggleFoldProject folds or unfolds a group by key without moving the cursor (mouse
// clicks on headers).
func (l *List) ToggleFoldProject(project string) {
	if l.folded[project] {
		delete(l.folded, project)
		return
	}
	l.folded[project] = true
}

// NextWaiting selects the next session waiting for the user, after the current selection
// in display order and wrapping around, unfolding its group if needed. It reports whether
// one was found.
func (l *List) NextWaiting() bool {
	ordered := l.ordered()
	if len(ordered) == 0 {
		return false
	}
	pos := indexOf(ordered, l.GetSelectedInstance())
	for i := 1; i <= len(ordered); i++ {
		cand := ordered[(pos+i)%len(ordered)]
		if cand.NeedsAttention() {
			delete(l.folded, groupKey(cand))
			l.SelectInstance(cand)
			return true
		}
	}
	return false
}

// Remove drops an instance from the list without killing anything, keeping a sensible
// selection. Used for adopted sessions that disappeared from tmux.
func (l *List) Remove(target *session.Instance) {
	idx := indexOf(l.items, target)
	if idx == -1 {
		return
	}
	selected := l.GetSelectedInstance()
	l.items = append(l.items[:idx], l.items[idx+1:]...)
	if selected != nil && selected != target {
		l.SelectInstance(selected)
		return
	}
	if l.selectedIdx >= len(l.items) {
		l.selectedIdx = 0
	}
	l.ensureVisibleSelection()
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
	// A session the user just created must be visible, so unfold its group. Adopted and
	// past sessions appear on their own and must not disturb the folds.
	if !instance.Adopted && !instance.Archived {
		delete(l.folded, groupKey(instance))
	}
	return func() {
		if instance.LastActivity.IsZero() {
			instance.LastActivity = now()
		}
	}
}

// GetSelectedInstance returns the currently selected instance, or nil while the cursor is
// on a group header.
func (l *List) GetSelectedInstance() *session.Instance {
	if l.headerCursor != "" {
		return nil
	}
	return l.selectedItem()
}

// SetSelectedInstance sets the selected index and moves the cursor off any header. Noop if
// the index is out of bounds.
func (l *List) SetSelectedInstance(idx int) {
	if idx < 0 || idx >= len(l.items) {
		return
	}
	l.selectedIdx = idx
	l.headerCursor = ""
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
