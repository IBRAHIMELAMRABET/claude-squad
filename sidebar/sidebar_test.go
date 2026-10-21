package sidebar

import (
	"claude-squad/cmd/cmd_test"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScripts(t *testing.T) {
	require.Equal(t, "TMUX= exec tmux attach-session -t '=claudesquad_fix'\n", AttachScript("claudesquad_fix"))
	require.Contains(t, PlaceholderScript(), "No session selected")
	require.Contains(t, PlaceholderScript(), "Alt+S opens the list")

	diff := DiffScript("/wt/fix", "abc123", "claudesquad_fix")
	require.Contains(t, diff, "git -C '/wt/fix' --no-pager diff --color=always 'abc123' | less -R")
	require.True(t, strings.HasSuffix(diff, AttachScript("claudesquad_fix")), "returns to the session")

	workingTree := DiffScript("/repo", "", "")
	require.Contains(t, workingTree, "diff --color=always | less -R")
	require.Contains(t, workingTree, "No session selected", "no session to return to: placeholder")

	shell := ShellScript("/it's here", "s")
	require.Contains(t, shell, `cd '/it'\''s here' && "${SHELL:-sh}"`)

	require.Contains(t, HelpScript("/h/help.txt", "s"), "less -R '/h/help.txt'")
	require.Contains(t, TextScript("/x/card.txt"), "cat '/x/card.txt'")
}

type recorder struct {
	ran    []string
	output string
}

func (r *recorder) exec() cmd_test.MockCmdExec {
	return cmd_test.MockCmdExec{
		OutputFunc: func(c *exec.Cmd) ([]byte, error) { return []byte(r.output), nil },
		RunFunc:    func(c *exec.Cmd) error { r.ran = append(r.ran, strings.Join(c.Args, " ")); return nil },
	}
}

func newTestController(t *testing.T, r *recorder) *Controller {
	t.Setenv("HOME", t.TempDir())
	c, err := New("%9", r.exec(), 36)
	require.NoError(t, err)
	return c
}

func TestNewRequiresTmux(t *testing.T) {
	_, err := New("", cmd_test.MockCmdExec{}, 36)
	require.Error(t, err)
}

// The viewer is always the single pane of the layout session; the list closes by detaching
// the popup's client.
func TestControllerDrivesTheViewerAndTheList(t *testing.T) {
	r := &recorder{}
	c := newTestController(t, r)

	require.NoError(t, c.ShowSession("claudesquad_fix"))
	require.Len(t, r.ran, 1)
	prefix := "tmux respawn-pane -k -t =claudesquad_ui:0.0 sh "
	require.True(t, strings.HasPrefix(r.ran[0], prefix), r.ran[0])
	script := strings.Trim(strings.TrimPrefix(r.ran[0], prefix), "'")
	data, err := os.ReadFile(script)
	require.NoError(t, err)
	require.Equal(t, AttachScript("claudesquad_fix"), string(data))
	require.Equal(t, "attach.sh", filepath.Base(script))

	require.NoError(t, c.CloseList())
	require.Equal(t, "tmux detach-client -s =claudesquad_list", r.ran[1])

	require.NoError(t, c.ShowText("hello\nworld"))
	card, err := os.ReadFile(filepath.Join(c.dir, "card.txt"))
	require.NoError(t, err)
	require.Equal(t, "hello\nworld", string(card))
}

func TestQuitOrder(t *testing.T) {
	r := &recorder{}
	c := newTestController(t, r)
	require.NoError(t, c.Quit())
	require.Equal(t, "tmux set-hook -u -t =claudesquad_ui pane-died", r.ran[0], "the hook goes first")
	require.Contains(t, r.ran[1], "tmux respawn-pane -k -t =claudesquad_ui:0.0 sh ", "then the nested client is replaced")
	require.Equal(t, "tmux kill-session -t =claudesquad_ui", r.ran[2])
	require.Equal(t, "tmux kill-session -t =claudesquad_list", r.ran[3], "the list, which runs this process, goes last")
}

func TestViewerSize(t *testing.T) {
	r := &recorder{output: "228 62\n"}
	c := newTestController(t, r)
	w, h := c.ViewerSize()
	require.Equal(t, 228, w)
	require.Equal(t, 62, h)

	r.output = "garbage"
	w, h = c.ViewerSize()
	require.Zero(t, w)
	require.Zero(t, h)
}

func TestOpenListAndKeyBindings(t *testing.T) {
	require.Equal(t, []string{"display-popup", "-E", "-B", "-x", "0", "-y", "0", "-w", "32", "-h", "100%",
		"TMUX= exec tmux attach-session -t =claudesquad_list"}, OpenList(32))
	require.Equal(t, "32", OpenList(0)[8], "default width")

	b := KeyBindings(32)
	require.Len(t, b, 3)
	require.Equal(t, "M-s", b[0][2])
	require.Equal(t, "#{==:#{session_name},claudesquad_ui}", b[0][5], "opens from the viewer")
	require.Equal(t, `display-popup -E -B -x 0 -y 0 -w 32 -h 100% "TMUX= exec tmux attach-session -t =claudesquad_list"`, b[0][6])
	require.Equal(t, "if-shell -F '#{==:#{session_name},claudesquad_list}' 'detach-client' 'send-keys M-s'", b[0][7],
		"closes from the list, and reaches the program anywhere else")
	require.Equal(t, "M-Left", b[1][2])
	require.Equal(t, "M-Right", b[2][2])
	require.Equal(t, "detach-client", b[2][6])
}

func TestTrueColorTerminal(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	require.True(t, TrueColorTerminal(env(map[string]string{"COLORTERM": "truecolor"})))
	require.True(t, TrueColorTerminal(env(map[string]string{"COLORTERM": "24bit"})))
	require.True(t, TrueColorTerminal(env(map[string]string{"WT_SESSION": "abc"})), "Windows Terminal")
	require.True(t, TrueColorTerminal(env(map[string]string{"TERM_PROGRAM": "vscode"})))
	require.False(t, TrueColorTerminal(env(map[string]string{"TERM": "xterm-256color"})))
}

func TestFitSessions(t *testing.T) {
	r := &recorder{output: "228 62"}
	c := newTestController(t, r)
	c.FitSessions([]string{"claudesquad_a"})
	require.Equal(t, []string{
		"tmux resize-window -t =claudesquad_a -x 228 -y 62",
		"tmux set-option -w -t =claudesquad_a window-size latest",
	}, r.ran)

	r.ran, r.output = nil, ""
	c.FitSessions([]string{"claudesquad_a"})
	require.Empty(t, r.ran, "nothing when the viewer size is unknown")
}
