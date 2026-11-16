package app

import (
	"claude-squad/config"
	"claude-squad/session"
	"claude-squad/session/history"
	"time"
)

// liveLink is the Claude process a running session was matched to.
type liveLink struct {
	pid       int
	procStart string
}

// liveProbe is a running Claude session as matching it to its process needs it.
type liveProbe struct {
	inst *session.Instance
	// id is the conversation cs knows the session in.
	id string
	// dir is the directory the session runs in.
	dir string
	// tmux is the session's tmux session.
	tmux string
	// link is the process the session was matched to before, if any.
	link liveLink
}

// Bounds on when a session's Claude process started, from its tmux session's creation:
// claude.exe takes seconds to start through WSL, and the Windows and WSL clocks can drift.
const (
	liveStartedBefore = time.Minute
	liveStartedAfter  = 2 * time.Minute
)

// matchLive finds, in Claude's session registry, the process each running session runs:
// the one it was matched to before; else the newest process in its conversation; else (the
// conversation changed while cs was not following) the process started in its directory
// closest to its tmux session. A process goes to one session only.
func matchLive(probes []liveProbe, live []history.Live, created func(tmux string) (time.Time, error)) map[*session.Instance]history.Live {
	found := make(map[*session.Instance]history.Live)
	claimed := make(map[liveLink]bool)
	link := func(l history.Live) liveLink { return liveLink{l.PID, l.ProcStart} }

	var unlinked []liveProbe
	for _, p := range probes {
		matched := false
		for _, l := range live {
			if p.link.pid != 0 && link(l) == p.link {
				found[p.inst], claimed[link(l)], matched = l, true, true
				break
			}
		}
		if !matched {
			unlinked = append(unlinked, p)
		}
	}

	var byDir []liveProbe
	for _, p := range unlinked {
		var best *history.Live
		for i := range live {
			l := &live[i]
			if l.SessionID == p.id && !claimed[link(*l)] && (best == nil || l.StartedAt > best.StartedAt) {
				best = l
			}
		}
		if best == nil {
			byDir = append(byDir, p)
			continue
		}
		found[p.inst], claimed[link(*best)] = *best, true
	}

	for _, p := range byDir {
		start, err := created(p.tmux)
		if err != nil {
			continue
		}
		var best *history.Live
		var bestGap time.Duration
		for i := range live {
			l := &live[i]
			gap := l.Started().Sub(start)
			if claimed[link(*l)] || gap < -liveStartedBefore || gap > liveStartedAfter ||
				config.NormalizePath(l.CWD) != p.dir {
				continue
			}
			if gap < 0 {
				gap = -gap
			}
			if best == nil || gap < bestGap {
				best, bestGap = l, gap
			}
		}
		if best != nil {
			found[p.inst], claimed[link(*best)] = *best, true
		}
	}
	return found
}

// applyLive follows each running session's Claude process: the conversation it is in now
// (after /clear) and the name it was given (/rename). It reports whether a session moved
// to another conversation, which is worth saving.
func (m *home) applyLive(found map[*session.Instance]history.Live) bool {
	links := make(map[*session.Instance]liveLink, len(found))
	moved := false
	for _, inst := range m.list.GetInstances() {
		l, ok := found[inst]
		if !ok || inst.Archived || inst.Paused() {
			continue
		}
		links[inst] = liveLink{l.PID, l.ProcStart}
		if l.SessionID != claudeID(inst) {
			inst.FollowConversation(l.SessionID)
			moved = true
		}
		if name := l.UserName(); name != "" {
			inst.Label = history.Shorten(name, 48)
		}
	}
	m.liveLinks = links
	return moved
}
