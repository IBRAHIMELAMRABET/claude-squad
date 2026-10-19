package tmux

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "claude", name))
	require.NoError(t, err)
	return string(data)
}

// The fixtures are real Claude Code 2.1 screens captured with tmux capture-pane.
func TestClassifyClaudeScreenFixtures(t *testing.T) {
	cases := map[string]ScreenState{
		"idle.txt":       ScreenIdle,
		"working.txt":    ScreenWorking,
		"permission.txt": ScreenPermission,
		"question.txt":   ScreenQuestion,
		"done.txt":       ScreenIdle,
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, want, ClassifyClaudeScreen(fixture(t, name)))
		})
	}
}

func TestClassifyScreenStripsANSI(t *testing.T) {
	raw := fixture(t, "idle_ansi.txt")
	require.Contains(t, raw, "\x1b[", "the fixture must contain escape sequences")
	require.NotContains(t, StripANSI(raw), "\x1b")
	require.Equal(t, ScreenIdle, classifyScreen("/home/user/.local/bin/claude", raw))
}

func TestClassifyClaudeScreenPriorities(t *testing.T) {
	// A permission prompt drawn over an otherwise idle screen still counts as a prompt.
	require.Equal(t, ScreenPermission, ClassifyClaudeScreen("Do you want to proceed?\n ? for shortcuts"))
	// While working, the footer swaps the shortcuts hint for "esc to interrupt".
	require.Equal(t, ScreenWorking, ClassifyClaudeScreen("Enter to select\n esc to interrupt"))
	require.Equal(t, ScreenUnknown, ClassifyClaudeScreen("plain shell output"))
	require.Equal(t, ScreenUnknown, ClassifyClaudeScreen(""))
}

func TestStripANSI(t *testing.T) {
	require.Equal(t, "bold red", StripANSI("\x1b[1m\x1b[31mbold red\x1b[0m"))
	require.Equal(t, "title", StripANSI("\x1b]0;window\x07title"))
	require.Equal(t, "x", StripANSI("\x1b(Bx\x1b[?25h"))
}

func TestProgramName(t *testing.T) {
	require.Equal(t, "claude", programName("claude"))
	require.Equal(t, "claude", programName("/home/user/.local/bin/claude --model sonnet"))
	require.Equal(t, "aider", programName("aider --model ollama_chat/gemma3:1b"))
	require.Equal(t, "", programName("   "))
}

func TestClassifyScreenOtherPrograms(t *testing.T) {
	require.Equal(t, ScreenPermission, classifyScreen("aider --model x", "(Y)es/(N)o/(D)on't ask again"))
	require.Equal(t, ScreenUnknown, classifyScreen("aider --model x", "thinking..."))
	require.Equal(t, ScreenPermission, classifyScreen("gemini", "Yes, allow once"))
	require.Equal(t, ScreenUnknown, classifyScreen("bash", "? for shortcuts"), "only known agents are classified")
}

func TestScreenStateString(t *testing.T) {
	require.Equal(t, "working", ScreenWorking.String())
	require.Equal(t, "unknown", ScreenUnknown.String())
}

func TestWindowsClaudeIsClassified(t *testing.T) {
	require.True(t, IsClaudeProgram("/mnt/c/Users/me/.local/bin/claude.exe --resume x"))
	require.Equal(t, ScreenIdle, classifyScreen("/mnt/c/Users/me/.local/bin/claude.exe", fixture(t, "idle.txt")))
}
