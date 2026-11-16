package history

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLiveSessions(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)

	live, err := LiveSessions()
	require.NoError(t, err)
	require.Empty(t, live, "no registry (older Claude): nothing, no error")

	sessions := filepath.Join(dir, "sessions")
	require.NoError(t, os.MkdirAll(sessions, 0755))
	write := func(name, body string) {
		require.NoError(t, os.WriteFile(filepath.Join(sessions, name), []byte(body), 0644))
	}
	write("14000.json", `{"pid":14000,"procStart":"77","sessionId":"new-id","cwd":"C:\\Users\\me","startedAt":1794825277629,"name":"Me","nameSource":"user","status":"busy"}`)
	write("15000.json", `{"pid":15000,"sessionId":"other","cwd":"/srv","startedAt":1,"name":"auto name","nameSource":"auto"}`)
	write("14000.0123abcd.key", `not json`)
	write("16000.json", `{"pid":16000,"cwd":"/srv"}`)
	write("17000.json", `{broken`)

	live, err = LiveSessions()
	require.NoError(t, err)
	require.Len(t, live, 2, "key files, entries without a conversation and broken files are skipped")
	byPID := map[int]Live{live[0].PID: live[0], live[1].PID: live[1]}

	me := byPID[14000]
	require.Equal(t, "new-id", me.SessionID)
	require.Equal(t, "77", me.ProcStart)
	require.Equal(t, `C:\Users\me`, me.CWD)
	require.Equal(t, int64(1794825277629), me.Started().UnixMilli())
	require.Equal(t, "Me", me.UserName())
	require.Equal(t, "", byPID[15000].UserName(), "only a name given with /rename counts")
}
