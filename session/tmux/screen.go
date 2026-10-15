package tmux

import (
	"path/filepath"
	"regexp"
	"strings"
)

// ScreenState is what an agent's terminal screen says it is doing.
type ScreenState int

const (
	// ScreenUnknown means the screen could not be classified (unsupported program or
	// unrecognised layout); callers should fall back to output-change detection.
	ScreenUnknown ScreenState = iota
	// ScreenWorking means the agent is busy producing output or running tools.
	ScreenWorking
	// ScreenPermission means the agent is blocked on a permission prompt.
	ScreenPermission
	// ScreenQuestion means the agent asked the user a question and waits for the answer.
	ScreenQuestion
	// ScreenIdle means the agent sits at its input prompt.
	ScreenIdle
)

func (s ScreenState) String() string {
	switch s {
	case ScreenWorking:
		return "working"
	case ScreenPermission:
		return "permission"
	case ScreenQuestion:
		return "question"
	case ScreenIdle:
		return "idle"
	}
	return "unknown"
}

// ansiRegex matches CSI sequences, OSC sequences, charset selections and keypad modes.
var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[()][A-Za-z0-9]|\x1b[=>]`)

// StripANSI removes terminal escape sequences from captured pane content.
func StripANSI(s string) string {
	return ansiRegex.ReplaceAllString(s, "")
}

// claudePermissionMarkers appear on Claude Code's permission prompts.
var claudePermissionMarkers = []string{
	"No, and tell Claude what to do differently",
	"Do you want to proceed?",
	"Do you want to make this edit",
	"Do you want to create",
	"Yes, and don't ask again",
	"Yes, allow",
	"Tab to amend",
}

// claudeQuestionMarkers appear when Claude asks the user to pick an answer (AskUserQuestion,
// the folder trust screen, plan approval and similar selection dialogs).
var claudeQuestionMarkers = []string{
	"Enter to select",
	"Enter to confirm",
}

// ClassifyClaudeScreen classifies a Claude Code screen (ANSI already stripped). The checks
// are ordered from most to least specific: a busy agent shows "esc to interrupt", a blocked
// one shows its prompt, an idle one shows the shortcuts hint.
func ClassifyClaudeScreen(content string) ScreenState {
	if strings.Contains(content, "esc to interrupt") {
		return ScreenWorking
	}
	for _, m := range claudePermissionMarkers {
		if strings.Contains(content, m) {
			return ScreenPermission
		}
	}
	for _, m := range claudeQuestionMarkers {
		if strings.Contains(content, m) {
			return ScreenQuestion
		}
	}
	if strings.Contains(content, "? for shortcuts") {
		return ScreenIdle
	}
	return ScreenUnknown
}

// programName returns the bare executable name of a program string such as
// "/home/me/.local/bin/claude --model x", i.e. "claude".
func programName(program string) string {
	fields := strings.Fields(program)
	if len(fields) == 0 {
		return ""
	}
	return filepath.Base(fields[0])
}

// classifyScreen classifies pane content for the given program.
func classifyScreen(program, content string) ScreenState {
	plain := StripANSI(content)
	switch name := programName(program); {
	case name == ProgramClaude:
		return ClassifyClaudeScreen(plain)
	case strings.HasPrefix(name, ProgramAider):
		if strings.Contains(plain, "(Y)es/(N)o/(D)on't ask again") {
			return ScreenPermission
		}
	case strings.HasPrefix(name, ProgramGemini):
		if strings.Contains(plain, "Yes, allow once") {
			return ScreenPermission
		}
	}
	return ScreenUnknown
}
