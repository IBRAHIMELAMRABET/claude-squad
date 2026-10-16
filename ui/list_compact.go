package ui

import (
	"claude-squad/session"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// Compact rows for the sidebar layout: no numbering, no vertical padding, one line for a
// past session and two for a running one.

var (
	compactTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#dddddd"})
	compactDimTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "#8a8a8a", Dark: "#9a9a9a"})
	compactMetaStyle = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "#A49FA5", Dark: "#777777"})
	compactSelectedStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("#dde4f0")).
				Foreground(lipgloss.Color("#1a1a1a"))
	compactSelectedMetaStyle = lipgloss.NewStyle().
					Background(lipgloss.Color("#dde4f0")).
					Foreground(lipgloss.Color("#4a4a4a"))
)

// statusIcon returns the two-column status marker of an instance, styled, with the given
// background.
func (r *InstanceRenderer) statusIcon(i *session.Instance, bg lipgloss.TerminalColor) string {
	switch i.Status {
	case session.Running, session.Loading:
		return lipgloss.NewStyle().Background(bg).Render(r.spinner.View() + " ")
	case session.Waiting:
		return waitingStyle.Background(bg).Render(waitingIcon)
	case session.Paused:
		return pausedStyle.Background(bg).Render(pausedIcon)
	case session.Stopped:
		return pausedStyle.Background(bg).Render(stoppedIcon)
	}
	return readyStyle.Background(bg).Render(readyIcon)
}

// fitLeft truncates text to width runes and pads it on the right.
func fitLeft(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if runewidth.StringWidth(text) > width {
		if width <= 1 {
			return runewidth.Truncate(text, width, "")
		}
		text = runewidth.Truncate(text, width-1, "…")
	}
	return text + strings.Repeat(" ", width-runewidth.StringWidth(text))
}

// RenderCompact draws a session as " ● title        2m" and, for running sessions, a second
// line with the branch and repository (or how it runs) and the diff stats.
func (r *InstanceRenderer) RenderCompact(i *session.Instance, selected bool) string {
	width := r.width
	if width < 8 {
		width = 8
	}
	titleStyle, metaStyle := compactTitleStyle, compactMetaStyle
	if i.Archived {
		titleStyle = compactDimTitleStyle
	}
	var bg lipgloss.TerminalColor = lipgloss.NoColor{}
	if selected {
		titleStyle, metaStyle = compactSelectedStyle, compactSelectedMetaStyle
		bg = compactSelectedStyle.GetBackground()
	}

	age := ago(i.LastActivity)
	// " " + icon(2) + title + " " + age
	titleWidth := width - 1 - 2 - 1 - runewidth.StringWidth(age)
	line1 := lipgloss.JoinHorizontal(lipgloss.Left,
		titleStyle.Render(" "),
		r.statusIcon(i, bg),
		titleStyle.Render(fitLeft(i.Title, titleWidth)+" "),
		metaStyle.Render(age),
	)
	if i.Archived {
		return line1
	}

	// Second line: what the session runs on.
	var meta string
	switch {
	case i.Adopted:
		meta = adoptedIcon + " tmux · " + i.RepoLabel()
	case i.InPlace:
		meta = "in place · " + i.RepoLabel()
		if i.Branch != "" {
			meta = branchIcon + " " + i.Branch + " · " + i.RepoLabel()
		}
	default:
		meta = branchIcon + " " + i.Branch
		if repo := i.RepoLabel(); repo != "" && repo != "." {
			meta += " · " + repo
		}
	}
	diff := ""
	if stat := i.GetDiffStats(); stat != nil && stat.Error == nil && !stat.IsEmpty() {
		diff = fmt.Sprintf("+%d -%d", stat.Added, stat.Removed)
	}
	metaWidth := width - 4 - runewidth.StringWidth(diff)
	if diff != "" {
		metaWidth--
	}
	line2 := metaStyle.Render("    " + fitLeft(meta, metaWidth))
	if diff != "" {
		line2 = lipgloss.JoinHorizontal(lipgloss.Left, line2, metaStyle.Render(" "), addedLinesStyle.Background(bg).Render(diff))
	}
	return lipgloss.JoinVertical(lipgloss.Left, line1, line2)
}
