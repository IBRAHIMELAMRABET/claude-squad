package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const samplePayload = `{"session_id":"abc","transcript_path":"/x.jsonl","cwd":"/home/user/repo","permission_mode":"default","hook_event_name":"Stop","stop_hook_active":false}`

func TestRecordAndRead(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "status")
	at := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

	require.NoError(t, Record(dir, strings.NewReader(samplePayload), "%3", at))

	st, ok := Read(dir, "%3")
	require.True(t, ok)
	require.Equal(t, Status{Pane: "%3", Event: "Stop", SessionID: "abc", CWD: "/home/user/repo", At: at}, st)
	require.Equal(t, StateDone, st.State())

	_, ok = Read(dir, "%4")
	require.False(t, ok, "unknown pane")
	_, ok = Read(dir, "")
	require.False(t, ok, "empty pane")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "no temp file left behind")
	require.Equal(t, "pane-3.json", entries[0].Name())

	Remove(dir, "%3")
	_, ok = Read(dir, "%3")
	require.False(t, ok)
}

func TestRecordOutsideTmuxIsANoop(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "status")
	require.NoError(t, Record(dir, strings.NewReader(samplePayload), "", time.Now()))
	_, err := os.Stat(dir)
	require.True(t, os.IsNotExist(err))
}

func TestRecordRejectsInvalidPayload(t *testing.T) {
	require.Error(t, Record(t.TempDir(), strings.NewReader("not json"), "%1", time.Now()))
}

func TestRecordKeepsNotificationType(t *testing.T) {
	dir := t.TempDir()
	p := `{"hook_event_name":"Notification","notification_type":"permission_prompt","message":"Claude needs your permission","cwd":"/r"}`
	require.NoError(t, Record(dir, strings.NewReader(p), "%7", time.Now()))
	st, ok := Read(dir, "%7")
	require.True(t, ok)
	require.Equal(t, "permission_prompt", st.Type)
	require.Equal(t, StatePermission, st.State())
}

func TestStatusState(t *testing.T) {
	cases := []struct {
		event, typ string
		want       State
	}{
		{"UserPromptSubmit", "", StateWorking},
		{"PreToolUse", "", StateWorking},
		{"PostToolUse", "", StateWorking},
		{"PermissionRequest", "", StatePermission},
		{"Notification", "permission_prompt", StatePermission},
		{"Notification", "idle_prompt", StateDone},
		{"Notification", "elicitation_dialog", StateQuestion},
		{"Notification", "auth_success", StateUnknown},
		{"Stop", "", StateDone},
		{"SessionEnd", "", StateEnded},
		{"SomethingNew", "", StateUnknown},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, Status{Event: tc.event, Type: tc.typ}.State(), "%s/%s", tc.event, tc.typ)
	}
}

func TestSettingsCoversEveryEvent(t *testing.T) {
	data, err := Settings("/usr/local/bin/cs hook")
	require.NoError(t, err)

	var parsed struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Type, Command string
			}
		}
	}
	require.NoError(t, json.Unmarshal(data, &parsed))
	require.Len(t, parsed.Hooks, len(Events))
	for _, ev := range Events {
		require.Len(t, parsed.Hooks[ev], 1, ev)
		require.Equal(t, "command", parsed.Hooks[ev][0].Hooks[0].Type)
		require.Equal(t, "/usr/local/bin/cs hook", parsed.Hooks[ev][0].Hooks[0].Command)
	}
}

func TestEnsureSettingsIsIdempotent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path, err := EnsureSettings("/opt/cs")
	require.NoError(t, err)
	first, err := os.Stat(path)
	require.NoError(t, err)

	again, err := EnsureSettings("/opt/cs")
	require.NoError(t, err)
	require.Equal(t, path, again)
	second, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, first.ModTime(), second.ModTime(), "unchanged content is not rewritten")

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(data), `"/opt/cs hook"`)
}

func TestIsClaudeAndLaunchArgs(t *testing.T) {
	require.True(t, IsClaude("claude"))
	require.True(t, IsClaude("/home/user/.local/bin/claude --model sonnet"))
	require.False(t, IsClaude("aider --model x"))
	require.False(t, IsClaude(""))

	require.Equal(t, " --settings /h/.claude-squad/hooks.json", LaunchArgs("claude", "/h/.claude-squad/hooks.json"))
	require.Equal(t, " --settings '/my dir/hooks.json'", LaunchArgs("claude", "/my dir/hooks.json"))
	require.Equal(t, "", LaunchArgs("claude --settings mine.json", "/h/hooks.json"), "user-provided --settings wins")
	require.Equal(t, "", LaunchArgs("codex", "/h/hooks.json"))
}

func TestHookCommandQuotesSpaces(t *testing.T) {
	require.Equal(t, "/usr/bin/cs hook", HookCommand("/usr/bin/cs"))
	require.Equal(t, "'/Program Files/cs' hook", HookCommand("/Program Files/cs"))
	require.Equal(t, `'/it'\''s/cs' hook`, HookCommand("/it's/cs"))
}

func TestPruneDropsDeadPanes(t *testing.T) {
	dir := t.TempDir()
	for _, pane := range []string{"%1", "%2", "%3"} {
		require.NoError(t, Record(dir, strings.NewReader(samplePayload), pane, time.Now()))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("keep"), 0644))

	Prune(dir, map[string]bool{"%2": true})

	_, ok := Read(dir, "%1")
	require.False(t, ok)
	_, ok = Read(dir, "%2")
	require.True(t, ok)
	_, ok = Read(dir, "%3")
	require.False(t, ok)
	_, err := os.Stat(filepath.Join(dir, "notes.txt"))
	require.NoError(t, err, "unrelated files are left alone")

	Prune(filepath.Join(dir, "missing"), nil) // no directory: no panic
}
