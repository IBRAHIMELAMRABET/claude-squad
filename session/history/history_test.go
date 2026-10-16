package history

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const titled = `{"type":"custom-title","customTitle":"CMIBills CI/CD","sessionId":"a"}
{"type":"mode","mode":"default","sessionId":"a"}
{"type":"user","cwd":"C:\\Users\\me\\Projects\\CMIBills\\Code\\Backend","isMeta":false,"message":{"role":"user","content":"<system-reminder>\nThe user named this session.\n</system-reminder>fix the pipeline"},"timestamp":"2026-08-20T20:25:05.720Z"}
{"type":"last-prompt","lastPrompt":"now the deploy stage","leafUuid":"x","sessionId":"a"}
{"type":"assistant","cwd":"C:\\Users\\me\\Projects\\CMIBills\\Code\\Backend","message":{"role":"assistant","content":[{"type":"text","text":"ok"}]}}
`

const untitled = `{"type":"system","cwd":"/home/me/repo","isMeta":true,"message":{"role":"user","content":"meta"}}
{"type":"user","cwd":"/home/me/repo","isMeta":true,"message":{"role":"user","content":"<command-name>/clear</command-name>"}}
{"type":"user","cwd":"/home/me/repo","message":{"role":"user","content":[{"type":"text","text":"Explain  how the   worktree layer works, in detail please, with the edge cases"},{"type":"image"}]}}
`

func TestParseHead(t *testing.T) {
	h := ParseHead(strings.NewReader(titled))
	require.Equal(t, `C:\Users\me\Projects\CMIBills\Code\Backend`, h.cwd)
	require.Equal(t, "CMIBills CI/CD", h.title(), "the custom title wins")
	require.Equal(t, "fix the pipeline", h.firstPrompt, "system reminders are stripped")
	require.Equal(t, "now the deploy stage", h.lastPrompt)

	h = ParseHead(strings.NewReader(untitled))
	require.Equal(t, "/home/me/repo", h.cwd)
	require.Equal(t, "Explain how the worktree layer works, in detail…", h.title(), "first real prompt, meta and commands skipped, shortened to 48 runes")

	require.Equal(t, "(untitled)", ParseHead(strings.NewReader(`{"type":"user","cwd":"/x"}`)).title())
	require.Equal(t, "", ParseHead(strings.NewReader("not json\n")).cwd)
}

func TestCleanPromptAndShorten(t *testing.T) {
	require.Equal(t, "deploy it", CleanPrompt("<command-message>img</command-message>\n<command-name>/img</command-name>deploy   it"))
	require.Equal(t, "abcd…", Shorten("abcdefgh", 5))
	require.Equal(t, "short", Shorten("short", 5))
}

func writeTranscript(t *testing.T, dir, id, body string, mod time.Time) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0755))
	p := filepath.Join(dir, id+".jsonl")
	require.NoError(t, os.WriteFile(p, []byte(body), 0644))
	require.NoError(t, os.Chtimes(p, mod, mod))
}

func TestScan(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	a := filepath.Join(root, "C--Users-me-Projects-CMIBills-Code-Backend")
	b := filepath.Join(root, "-home-me-repo")
	writeTranscript(t, a, "11111111-1111-1111-1111-111111111111", titled, now.Add(-1*time.Hour))
	writeTranscript(t, a, "22222222-2222-2222-2222-222222222222", titled, now.Add(-2*time.Hour))
	writeTranscript(t, a, "33333333-3333-3333-3333-333333333333", titled, now.Add(-3*time.Hour)) // beyond the per-project limit
	writeTranscript(t, b, "44444444-4444-4444-4444-444444444444", untitled, now.Add(-30*time.Minute))
	writeTranscript(t, b, "11111111-1111-1111-1111-111111111111", titled, now.Add(-10*time.Minute))             // same id, newer copy
	writeTranscript(t, b, "55555555-5555-5555-5555-555555555555", untitled, now.Add(-100*24*time.Hour))         // too old
	require.NoError(t, os.WriteFile(filepath.Join(b, "notes.jsonl"), []byte(untitled), 0644))                   // not a session id
	require.NoError(t, os.WriteFile(filepath.Join(b, "66666666-6666-6666-6666-666666666666.jsonl"), nil, 0644)) // empty
	require.NoError(t, os.MkdirAll(filepath.Join(b, "11111111-1111-1111-1111-111111111111"), 0755))             // a subdir, ignored

	entries, err := Scan(root, 2, 30*24*time.Hour)
	require.NoError(t, err)

	var ids []string
	for _, e := range entries {
		ids = append(ids, e.ID[:8])
	}
	require.Equal(t, []string{"11111111", "44444444", "22222222"}, ids, "newest first, limit per project, one per id")
	require.Equal(t, "-home-me-repo", entries[0].Dir, "the newer copy of the duplicated id wins")
	require.Equal(t, "CMIBills CI/CD", entries[0].Title)
	require.Equal(t, "/home/me/repo", entries[1].CWD)
	require.WithinDuration(t, now.Add(-10*time.Minute), entries[0].Updated, time.Second)

	all, err := Scan(root, 0, 0)
	require.NoError(t, err)
	require.Len(t, all, 5, "no limits: every distinct session")

	_, err = Scan(filepath.Join(root, "missing"), 2, 0)
	require.Error(t, err)
}
