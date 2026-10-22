package ui

import (
	"claude-squad/config"
	"claude-squad/session"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/stretchr/testify/require"
)

func newTestList(titles ...string) *List {
	s := spinner.New()
	l := NewList(&s, false)
	for _, t := range titles {
		inst, _ := session.NewInstance(session.InstanceOptions{
			Title:   t,
			Path:    ".",
			Program: "echo",
		})
		l.AddInstance(inst)
	}
	return l
}

func TestMoveUp(t *testing.T) {
	l := newTestList("a", "b", "c")
	l.SetSelectedInstance(1) // select "b"

	moved := l.MoveUp()
	require.True(t, moved)
	require.Equal(t, 0, l.selectedIdx)
	require.Equal(t, "b", l.items[0].Title)
	require.Equal(t, "a", l.items[1].Title)
	require.Equal(t, "c", l.items[2].Title)
}

func TestMoveUp_AtTop(t *testing.T) {
	l := newTestList("a", "b", "c")
	l.SetSelectedInstance(0)

	moved := l.MoveUp()
	require.False(t, moved)
	require.Equal(t, 0, l.selectedIdx)
	require.Equal(t, "a", l.items[0].Title)
}

func TestMoveDown(t *testing.T) {
	l := newTestList("a", "b", "c")
	l.SetSelectedInstance(1) // select "b"

	moved := l.MoveDown()
	require.True(t, moved)
	require.Equal(t, 2, l.selectedIdx)
	require.Equal(t, "a", l.items[0].Title)
	require.Equal(t, "c", l.items[1].Title)
	require.Equal(t, "b", l.items[2].Title)
}

func TestMoveDown_AtBottom(t *testing.T) {
	l := newTestList("a", "b", "c")
	l.SetSelectedInstance(2)

	moved := l.MoveDown()
	require.False(t, moved)
	require.Equal(t, 2, l.selectedIdx)
	require.Equal(t, "c", l.items[2].Title)
}

func TestMoveWithSingleItem(t *testing.T) {
	l := newTestList("only")
	l.SetSelectedInstance(0)

	require.False(t, l.MoveUp())
	require.False(t, l.MoveDown())
}

func titlesOf(insts []*session.Instance) []string {
	out := make([]string, 0, len(insts))
	for _, i := range insts {
		out = append(out, i.Title)
	}
	return out
}

func rowLabels(l *List) []string {
	var out []string
	for _, r := range l.rows() {
		if r.header {
			out = append(out, "#"+r.project)
		} else {
			out = append(out, r.instance.Title)
		}
	}
	return out
}

func TestGroupedOrder(t *testing.T) {
	l := newTestList("a", "b", "c", "d")
	l.items[0].Project, l.items[0].Status = "Zeta", session.Ready
	l.items[1].Project, l.items[1].Status = "Alpha", session.Paused
	l.items[2].Project, l.items[2].Status = "Alpha", session.Running
	l.items[3].Project = "" // no project: grouped by its folder, after the named projects

	require.Equal(t, []string{"#Alpha", "c", "b", "#Zeta", "a", "#" + config.FolderGroupPath(l.items[3].ProjectPath()), "d"}, rowLabels(l))
}

func TestLastActivityBreaksTies(t *testing.T) {
	l := newTestList("old", "new", "untouched")
	base := time.Now()
	l.items[0].LastActivity = base.Add(-time.Hour)
	l.items[1].LastActivity = base

	require.Equal(t, []string{"new", "old", "untouched"}, titlesOf(l.visible()))
}

func TestSessionsWithoutProjectGroupByFolder(t *testing.T) {
	l := newTestList("a", "b")
	key := config.FolderGroupPath(l.items[0].ProjectPath())
	require.NotEmpty(t, key)
	require.Equal(t, []string{"#" + key, "a", "b"}, rowLabels(l), "one folder header for both")
	// Tests run in the package directory, wherever the repository is checked out.
	parts := strings.Split(strings.Trim(filepath.ToSlash(key), "/"), "/")
	require.GreaterOrEqual(t, len(parts), 3)
	require.Equal(t, strings.Join(parts[len(parts)-3:], "/"), l.rows()[0].name, "the header shows the last three path segments")
	l.SetLabelSegments(1)
	require.Equal(t, "ui", l.rows()[0].name, "or fewer when configured")
}

func TestFolderGroupLabelsAreNamesAndDisambiguated(t *testing.T) {
	labels := groupLabels([]string{"/w/one/Backend", "/w/two/Backend", "/w/app", "Orbit", "/w/Orbit"}, 1)
	require.Equal(t, "one/Backend", labels["/w/one/Backend"], "same folder name: the parent is added")
	require.Equal(t, "two/Backend", labels["/w/two/Backend"])
	require.Equal(t, "app", labels["/w/app"])
	require.Equal(t, "Orbit", labels["Orbit"], "project names never change")
	require.Equal(t, "w/Orbit", labels["/w/Orbit"], "a folder clashing with a project gets its parent")
}

// Folding the selected session's group puts the cursor on the header, so the same key
// unfolds it again: a fold can always be undone from the keyboard.
func TestFoldPutsCursorOnHeaderAndUnfolds(t *testing.T) {
	l := newTestList("a1", "a2", "b1")
	l.items[0].Project, l.items[1].Project, l.items[2].Project = "A", "A", "B"
	l.SelectInstance(l.items[0])

	l.ToggleFold()
	require.Equal(t, []string{"A"}, l.FoldedProjects())
	require.Nil(t, l.GetSelectedInstance(), "the cursor is on the header")
	key, onHeader := l.SelectedHeader()
	require.True(t, onHeader)
	require.Equal(t, "A", key)
	require.Equal(t, []string{"#A", "#B", "b1"}, rowLabels(l))
	require.True(t, l.rows()[0].folded)
	require.Equal(t, 2, l.rows()[0].count)

	l.ToggleFold()
	require.Empty(t, l.FoldedProjects(), "same key unfolds")
	_, onHeader = l.SelectedHeader()
	require.True(t, onHeader, "the cursor stays on the header")

	l.Down()
	require.Equal(t, "a1", l.GetSelectedInstance().Title, "down enters the group")
}

func TestCollapseAndExpand(t *testing.T) {
	l := newTestList("a1", "b1")
	l.items[0].Project, l.items[1].Project = "A", "B"
	l.SelectInstance(l.items[1])

	l.Collapse()
	require.Equal(t, []string{"B"}, l.FoldedProjects())
	l.Collapse()
	require.Equal(t, []string{"B"}, l.FoldedProjects(), "collapsing twice keeps it folded")

	l.Expand()
	require.Empty(t, l.FoldedProjects())
	l.Expand()
	require.Equal(t, "b1", l.GetSelectedInstance().Title, "expand on an open header enters the group")
	l.Expand()
	require.Equal(t, "b1", l.GetSelectedInstance().Title, "expand on a session does nothing")
}

func TestNavigationVisitsHeadersAndWraps(t *testing.T) {
	l := newTestList("a1", "b1", "c1")
	l.items[0].Project, l.items[1].Project, l.items[2].Project = "A", "B", "C"
	l.SetFolded([]string{"B"})
	l.SelectInstance(l.items[0])

	l.Down()
	key, _ := l.SelectedHeader()
	require.Equal(t, "B", key, "the folded group's header is reachable")
	l.Down()
	key, _ = l.SelectedHeader()
	require.Equal(t, "C", key)
	l.Down()
	require.Equal(t, "c1", l.GetSelectedInstance().Title)
	l.Down()
	key, _ = l.SelectedHeader()
	require.Equal(t, "A", key, "wraps to the first header")
	l.Up()
	require.Equal(t, "c1", l.GetSelectedInstance().Title)
}

func TestStepFromHiddenSelectionLandsOnItsHeader(t *testing.T) {
	l := newTestList("a1", "b1")
	l.items[0].Project, l.items[1].Project = "A", "B"
	l.SelectInstance(l.items[1])
	l.SetFolded([]string{"A", "B"}) // everything folded, selection hidden

	l.Down()
	key, onHeader := l.SelectedHeader()
	require.True(t, onHeader)
	require.Equal(t, "B", key)
	l.ToggleFold()
	require.Equal(t, []string{"A"}, l.FoldedProjects(), "and can be unfolded from there")
}

func TestAddInstanceUnfoldsItsGroup(t *testing.T) {
	l := newTestList("a1")
	l.items[0].Project = "A"
	l.SetFolded([]string{"A", "B"})

	inst, _ := session.NewInstance(session.InstanceOptions{Title: "a2", Path: ".", Program: "echo"})
	inst.Project = "A"
	l.AddInstance(inst)
	require.Equal(t, []string{"B"}, l.FoldedProjects())
}

func TestKillSelectsVisibleNeighbour(t *testing.T) {
	l := newTestList("a", "b", "c")

	l.SelectInstance(l.items[2]) // last one: falls back to the previous
	l.Kill()
	require.Equal(t, "b", l.GetSelectedInstance().Title)

	l.SelectInstance(l.items[0]) // first one: takes the next
	l.Kill()
	require.Equal(t, "b", l.GetSelectedInstance().Title)

	l.Kill()
	require.Nil(t, l.GetSelectedInstance())
	require.Zero(t, l.NumInstances())
}

func TestAgo(t *testing.T) {
	fixed := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	now = func() time.Time { return fixed }
	defer func() { now = time.Now }()

	require.Equal(t, "", ago(time.Time{}))
	require.Equal(t, "now", ago(fixed.Add(-10*time.Second)))
	require.Equal(t, "5m", ago(fixed.Add(-5*time.Minute)))
	require.Equal(t, "3h", ago(fixed.Add(-3*time.Hour)))
	require.Equal(t, "2d", ago(fixed.Add(-49*time.Hour)))
}

func TestRenderShowsHeaderAndRepo(t *testing.T) {
	l := newTestList("a1")
	l.items[0].Project = "Orbit"
	l.SetSize(60, 30)

	out := l.String()
	require.Contains(t, out, "▾ Orbit")
	require.Contains(t, out, "a1")
}

func TestWaitingSortsFirstAndCountsInHeader(t *testing.T) {
	l := newTestList("busy", "needs-you", "idle")
	for _, i := range l.items {
		i.Project = "Orbit"
	}
	l.items[0].Status = session.Running
	l.items[1].Status = session.Waiting
	l.items[2].Status = session.Ready
	l.SetSize(60, 30)

	require.Equal(t, []string{"#Orbit", "needs-you", "busy", "idle"}, rowLabels(l))
	require.Equal(t, 1, l.rows()[0].waiting)
	require.Contains(t, l.String(), "1 waiting")
}

func TestActiveOnlyHidesPausedAndEmptyGroups(t *testing.T) {
	l := newTestList("live", "paused")
	l.items[0].Project, l.items[1].Project = "A", "B"
	l.items[1].Status = session.Paused
	l.SelectInstance(l.items[1])

	l.SetActiveOnly(true)
	require.Equal(t, []string{"#A", "live"}, rowLabels(l), "the paused session and its empty group disappear")
	require.Equal(t, "live", l.GetSelectedInstance().Title, "selection moves to a visible session")

	l.SetActiveOnly(false)
	require.Equal(t, []string{"#A", "live", "#B", "paused"}, rowLabels(l))
}

func TestTextFilterMatchesTitleProjectRepoAndBranch(t *testing.T) {
	l := newTestList("fix-auth", "docs", "api")
	l.items[0].Project = "Orbit"
	l.items[1].Project = "Bank"
	l.items[2].Project = "Orbit"
	l.items[2].Branch = "user/billing-api"

	cases := map[string][]string{
		"AUTH":    {"fix-auth"},
		"orbit":   {"fix-auth", "api"},
		"billing": {"api"},
		"zzz":     {},
		"":        {"docs", "fix-auth", "api"}, // groups are alphabetical
	}
	for filter, want := range cases {
		l.SetFilter(filter)
		require.Equal(t, want, titlesOf(l.visible()), "filter %q", filter)
	}
}

// A session renamed in Claude shows and is found by its new name, in both row styles.
func TestRowsShowTheClaudeName(t *testing.T) {
	l := newTestList("resumed")
	l.items[0].Label = "CMI BackOffice"
	l.SetSize(60, 30)
	require.Contains(t, l.String(), "CMI BackOffice")
	require.NotContains(t, l.String(), "resumed")
	l.SetCompact(true)
	require.Contains(t, l.String(), "CMI BackOffice")

	l.SetFilter("backoffice")
	require.Equal(t, []string{"resumed"}, titlesOf(l.visible()))
	l.SetFilter("resumed")
	require.Equal(t, []string{"resumed"}, titlesOf(l.visible()), "the original name still matches")
}

func TestTitleShowsFilterBadgesAndWaitingCount(t *testing.T) {
	l := newTestList("one", "two", "three")
	l.items[0].Status = session.Waiting
	l.items[1].Status = session.Waiting
	l.items[2].Status = session.Paused
	l.SetSize(80, 30)

	l.SetActiveOnly(true)
	l.SetFilter("tw")
	out := l.String()
	require.Contains(t, out, " active ")
	require.Contains(t, out, " /tw ")
	require.Contains(t, out, "1 waiting", "only the visible waiting sessions are counted")
	require.Equal(t, 1, l.WaitingCount())
}

func TestNextWaitingWrapsAndUnfolds(t *testing.T) {
	l := newTestList("a1", "b1", "b2", "c1")
	l.items[0].Project, l.items[1].Project, l.items[2].Project, l.items[3].Project = "A", "B", "B", "C"
	l.items[0].Status = session.Waiting
	l.items[2].Status = session.Waiting
	l.SetFolded([]string{"B"})
	l.SelectInstance(l.items[0])

	require.True(t, l.NextWaiting())
	require.Equal(t, "b2", l.GetSelectedInstance().Title)
	require.Empty(t, l.FoldedProjects(), "the target's group is unfolded")

	require.True(t, l.NextWaiting())
	require.Equal(t, "a1", l.GetSelectedInstance().Title, "wraps around")

	l.items[0].Status, l.items[2].Status = session.Ready, session.Ready
	require.False(t, l.NextWaiting())
	require.Equal(t, "a1", l.GetSelectedInstance().Title, "selection untouched when nothing waits")
}

func TestRemoveKeepsSelection(t *testing.T) {
	l := newTestList("a", "b", "c")
	l.SelectInstance(l.items[2])

	l.Remove(l.items[0])
	require.Equal(t, "c", l.GetSelectedInstance().Title, "removing another row keeps the selection")
	require.Equal(t, []string{"b", "c"}, titlesOf(l.visible()))

	l.Remove(l.items[1]) // the selected one
	require.Equal(t, "b", l.GetSelectedInstance().Title)

	l.Remove(l.items[0])
	require.Nil(t, l.GetSelectedInstance())
}

func TestAdoptedInstanceDoesNotUnfoldAndRendersMarker(t *testing.T) {
	l := newTestList("a1")
	l.items[0].Project = "A"
	l.SetFolded([]string{"A"})

	inst, _ := session.NewInstance(session.InstanceOptions{Title: "scratch", Path: ".", Program: "claude"})
	inst.Adopted = true
	inst.Project = "A"
	l.AddInstance(inst)()
	require.Equal(t, []string{"A"}, l.FoldedProjects(), "discovery must not disturb folds")

	l.SetFolded(nil)
	l.SetSize(60, 30)
	require.Contains(t, l.String(), adoptedIcon)
}

// A list taller than the pane scrolls so the selection stays visible.
func TestListScrollsToSelection(t *testing.T) {
	titles := make([]string, 30)
	for i := range titles {
		titles[i] = fmt.Sprintf("row-%02d", i)
	}
	l := newTestList(titles...)
	for _, inst := range l.items {
		inst.Project = "P"
	}
	l.SetSize(60, 24)

	l.SelectInstance(l.items[0])
	out := l.String()
	require.Contains(t, out, "row-00")
	require.Contains(t, out, "▾ P", "the header of the selected group is drawn")
	require.Contains(t, out, "↓ ")
	require.NotContains(t, out, "↑ ")

	l.SelectInstance(l.items[29])
	out = l.String()
	require.Contains(t, out, "row-29", "scrolled down to the selection")
	require.Contains(t, out, "↑ ")
	require.NotContains(t, out, "↓ ")

	l.SelectInstance(l.items[15])
	out = l.String()
	require.Contains(t, out, "row-15")
	require.Contains(t, out, "↑ ")
	require.Contains(t, out, "↓ ")
	for _, line := range strings.Split(out, "\n") {
		require.LessOrEqual(t, lipgloss.Width(line), 60)
	}
	require.LessOrEqual(t, strings.Count(out, "\n"), 24, "never taller than the pane")
}

// Clicks map back to rows through the layout of the last render.
func TestHitTestAndFoldByName(t *testing.T) {
	l := newTestList("a1", "a2", "b1")
	l.items[0].Project, l.items[1].Project, l.items[2].Project = "A", "A", "B"
	l.SetSize(60, 40)
	out := l.String()

	lines := strings.Split(out, "\n")
	headerLine, rowLine := -1, -1
	for i, line := range lines {
		if strings.Contains(line, "▾ B") {
			headerLine = i
		}
		if strings.Contains(line, "a2") {
			rowLine = i
		}
	}
	require.NotEqual(t, -1, headerLine)
	require.NotEqual(t, -1, rowLine)

	row, ok := l.HitTest(headerLine)
	require.True(t, ok)
	require.True(t, row.Header)
	require.Equal(t, "B", row.Project)

	row, ok = l.HitTest(rowLine)
	require.True(t, ok)
	require.Equal(t, "a2", row.Instance.Title)

	_, ok = l.HitTest(0)
	require.False(t, ok, "the title area is not a row")

	l.ToggleFoldProject("B")
	require.Equal(t, []string{"B"}, l.FoldedProjects())
	require.Equal(t, "a1", l.GetSelectedInstance().Title, "folding another group leaves the selection alone")
	l.ToggleFoldProject("B")
	require.Empty(t, l.FoldedProjects())
}

// Compact rows: one line for a past session, two for a running one, no title badge.
func TestCompactRows(t *testing.T) {
	l := newTestList("running", "past")
	l.items[0].Project, l.items[1].Project = "P", "P"
	l.items[1].Archived = true
	l.items[1].Status = session.Stopped
	l.SetCompact(true)
	l.SetSize(32, 20)

	out := l.String()
	require.NotContains(t, out, "Instances")
	require.NotContains(t, out, "1.", "no numbering")
	require.Contains(t, out, "running")
	require.Contains(t, out, "past")
	for _, line := range strings.Split(out, "\n") {
		require.LessOrEqual(t, lipgloss.Width(line), 32, "line too wide: %q", line)
	}
	running := l.renderer.RenderCompact(l.items[0], false)
	past := l.renderer.RenderCompact(l.items[1], true)
	require.Equal(t, 2, len(strings.Split(running, "\n")))
	require.Equal(t, 1, len(strings.Split(past, "\n")))
	require.Contains(t, past, stoppedIcon)

	l.SetFilter("ru")
	require.Contains(t, l.String(), "/ru", "filters still show, as a dim line")
}

func TestHeaderFitsTheWidth(t *testing.T) {
	l := newTestList("x")
	l.items[0].Project = "alex/workbench-tools/desktop-companion"
	l.SetCompact(true)
	l.SetSize(32, 10)
	for _, line := range strings.Split(l.String(), "\n") {
		require.LessOrEqual(t, lipgloss.Width(line), 32, "line too wide: %q", line)
	}
	require.Contains(t, l.String(), "…", "the name is shortened")
	require.Contains(t, l.String(), " 1 ", "the count stays")
}

func TestFilterShowsMatchesInFoldedGroups(t *testing.T) {
	l := newTestList("weekly sec", "other")
	l.items[0].Project, l.items[1].Project = "Orbit", "Orbit"
	l.SetFolded([]string{"Orbit"})

	l.SetFilter("weekly")
	require.Equal(t, []string{"#Orbit", "weekly sec"}, rowLabels(l), "the match is shown although the group is folded")
	l.SetFilter("")
	require.Equal(t, []string{"#Orbit"}, rowLabels(l), "the fold is back once the filter is cleared")
	require.Equal(t, []string{"Orbit"}, l.FoldedProjects())
}

func TestThreeSegmentLabelsLikeASessionBrowser(t *testing.T) {
	labels := groupLabels([]string{
		"/mnt/c/Users/me/Documents/Acme/Projects/Bank/Code/Backend",
		"/mnt/c/Users/me/Documents/Acme/Projects/Atelier",
		"/mnt/c/Users/me",
	}, 3)
	require.Equal(t, "Bank/Code/Backend", labels["/mnt/c/Users/me/Documents/Acme/Projects/Bank/Code/Backend"])
	require.Equal(t, "Acme/Projects/Atelier", labels["/mnt/c/Users/me/Documents/Acme/Projects/Atelier"])
	require.Equal(t, "c/Users/me", labels["/mnt/c/Users/me"])
}

func TestViewModes(t *testing.T) {
	l := newTestList("running", "waiting", "paused", "past")
	l.items[1].Status = session.Waiting
	l.items[2].Status = session.Paused
	l.items[3].Archived, l.items[3].Status = true, session.Stopped

	require.Equal(t, ViewAll, l.Mode())
	require.Len(t, l.visible(), 4)
	l.CycleMode()
	require.Equal(t, ViewActive, l.Mode())
	require.ElementsMatch(t, []string{"running", "waiting"}, titlesOf(l.visible()))
	l.CycleMode()
	require.Equal(t, ViewWaiting, l.Mode())
	require.Equal(t, []string{"waiting"}, titlesOf(l.visible()))
	l.CycleMode()
	require.Equal(t, ViewAll, l.Mode(), "wraps back to all")

	require.Equal(t, 4, l.viewCount(ViewAll))
	require.Equal(t, 2, l.viewCount(ViewActive))
	require.Equal(t, 1, l.viewCount(ViewWaiting))
	require.Equal(t, ViewWaiting, ParseViewMode("waiting"))
	require.Equal(t, ViewAll, ParseViewMode("nonsense"))
}

func TestModeBarIsDrawnAndClickable(t *testing.T) {
	l := newTestList("a", "b")
	l.items[0].Status = session.Waiting
	l.SetCompact(true)
	l.SetSize(32, 20)

	out := l.String()
	first := strings.Split(out, "\n")[0]
	require.Contains(t, first, "All 2")
	require.Contains(t, first, "Active 2")
	require.Contains(t, first, "Waiting 1")
	require.LessOrEqual(t, lipgloss.Width(first), 32)

	plain := stripANSI(first)
	col := strings.Index(plain, "Waiting")
	require.GreaterOrEqual(t, col, 0)
	mode, ok := l.ViewAt(runewidth.StringWidth(plain[:col])+1, 0)
	require.True(t, ok)
	require.Equal(t, ViewWaiting, mode)
	_, ok = l.ViewAt(5, 1)
	require.False(t, ok, "only the first line is the bar")
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

// Hidden past sessions leave every view but Hidden, which the bar shows only when used.
func TestHiddenView(t *testing.T) {
	l := newTestList("kept", "gone")
	l.items[1].Hidden = true
	l.SetCompact(true)
	l.SetSize(40, 20)
	require.Equal(t, []string{"kept"}, titlesOf(l.visible()))
	require.Contains(t, l.String(), "Hid")

	l.SetMode(ViewActive)
	l.CycleMode()
	require.Equal(t, ViewWaiting, l.Mode())
	l.CycleMode()
	require.Equal(t, ViewHidden, l.Mode())
	require.Equal(t, []string{"gone"}, titlesOf(l.visible()))

	l.items[1].Hidden = false
	l.SetMode(ViewWaiting)
	l.CycleMode()
	require.Equal(t, ViewAll, l.Mode(), "no Hidden view when nothing is hidden")
	require.NotContains(t, l.String(), "Hid")
}
