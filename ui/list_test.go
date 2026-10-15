package ui

import (
	"claude-squad/session"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
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
	l.items[3].Project = "" // Other, always last

	require.Equal(t, []string{"#Alpha", "c", "b", "#Zeta", "a", "#Other", "d"}, rowLabels(l))
}

func TestLastActivityBreaksTies(t *testing.T) {
	l := newTestList("old", "new", "untouched")
	base := time.Now()
	l.items[0].LastActivity = base.Add(-time.Hour)
	l.items[1].LastActivity = base

	require.Equal(t, []string{"new", "old", "untouched"}, titlesOf(l.visible()))
}

func TestHeadersHiddenWhenEverythingIsOther(t *testing.T) {
	l := newTestList("a", "b")
	for _, r := range l.rows() {
		require.False(t, r.header)
	}
	require.Equal(t, []string{"a", "b"}, rowLabels(l))
}

func TestFoldHidesGroupAndMovesSelection(t *testing.T) {
	l := newTestList("a1", "a2", "b1")
	l.items[0].Project, l.items[1].Project, l.items[2].Project = "A", "A", "B"
	l.SelectInstance(l.items[0])

	l.ToggleFold()
	require.Equal(t, []string{"A"}, l.FoldedProjects())
	require.Equal(t, "b1", l.GetSelectedInstance().Title, "selection moves to the next visible session")
	require.Equal(t, []string{"#A", "#B", "b1"}, rowLabels(l))
	require.True(t, l.rows()[0].folded)
	require.Equal(t, 2, l.rows()[0].count)

	l.ToggleFold() // fold B too: nothing left to select, selection stays
	require.Equal(t, "b1", l.GetSelectedInstance().Title)
	require.Empty(t, l.visible())

	l.ToggleFold() // unfolds the selected session's group
	require.Equal(t, []string{"A"}, l.FoldedProjects())
	require.Equal(t, []string{"b1"}, titlesOf(l.visible()))
}

func TestNavigationWrapsAcrossVisibleSessions(t *testing.T) {
	l := newTestList("a1", "b1", "c1")
	l.items[0].Project, l.items[1].Project, l.items[2].Project = "A", "B", "C"
	l.SetFolded([]string{"B"})
	l.SelectInstance(l.items[0])

	l.Down()
	require.Equal(t, "c1", l.GetSelectedInstance().Title, "skips the folded group")
	l.Down()
	require.Equal(t, "a1", l.GetSelectedInstance().Title, "wraps")
	l.Up()
	require.Equal(t, "c1", l.GetSelectedInstance().Title)
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
	l.items[0].Project = "Fawri"
	l.SetSize(60, 30)

	out := l.String()
	require.Contains(t, out, "▾ Fawri")
	require.Contains(t, out, "a1")
}
