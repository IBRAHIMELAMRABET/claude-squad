// Package history lists past Claude Code sessions from the transcripts Claude keeps under
// ~/.claude/projects/<encoded cwd>/<session id>.jsonl, so they can be shown next to the
// running ones and resumed with `claude --resume <id>`.
package history

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Entry is one past session.
type Entry struct {
	// ID is the Claude session id (the transcript file name).
	ID string
	// Dir is the project directory name under ~/.claude/projects.
	Dir string
	// CWD is the working directory recorded in the transcript, as written (it may be a
	// Windows path when the session ran on Windows).
	CWD string
	// Title is the latest custom title set with /rename, or the first prompt, shortened.
	Title string
	// CustomTitle is the latest title set with /rename, if any.
	CustomTitle string
	// Headless is true for a run without a terminal (claude -p, the SDK): tests and
	// automation rather than a conversation to come back to.
	Headless bool
	// LastPrompt is the most recent prompt recorded in the transcript, if any.
	LastPrompt string
	// Updated is the transcript's modification time.
	Updated time.Time
	// Path is the transcript file.
	Path string
}

// ProjectsDir returns Claude Code's transcript store.
func ProjectsDir() (string, error) {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "projects"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

// TranscriptExists reports whether Claude recorded a transcript for the session id.
func TranscriptExists(id string) bool {
	dir, err := ProjectsDir()
	if err != nil || id == "" {
		return false
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "*", id+".jsonl"))
	return len(matches) > 0
}

var sessionFile = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.jsonl$`)

// Scanner lists past sessions, re-reading only the transcripts that changed since its
// previous scan. It is not safe for concurrent use.
type Scanner struct {
	cache map[string]cachedSummary
}

type cachedSummary struct {
	mod     time.Time
	size    int64
	summary summary
}

// NewScanner returns a Scanner with an empty cache.
func NewScanner() *Scanner {
	return &Scanner{cache: make(map[string]cachedSummary)}
}

// Scan lists sessions without keeping a cache between calls.
func Scan(projectsDir string, perProject int, maxAge time.Duration) ([]Entry, error) {
	return NewScanner().Scan(projectsDir, perProject, maxAge)
}

// Scan returns the sessions of every project under projectsDir, newest first: at most
// perProject per project directory when perProject is positive, none older than maxAge
// when maxAge is positive. A session id seen in several directories is kept once, from its
// newest file.
func (s *Scanner) Scan(projectsDir string, perProject int, maxAge time.Duration) ([]Entry, error) {
	dirs, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil, err
	}
	cutoff := time.Time{}
	if maxAge > 0 {
		cutoff = time.Now().Add(-maxAge)
	}

	seen := make(map[string]bool)
	byID := make(map[string]Entry)
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		dir := filepath.Join(projectsDir, d.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		type candidate struct {
			name string
			mod  time.Time
			size int64
		}
		var cands []candidate
		for _, f := range files {
			if f.IsDir() || !sessionFile.MatchString(f.Name()) {
				continue
			}
			info, err := f.Info()
			if err != nil || info.Size() == 0 || info.ModTime().Before(cutoff) {
				continue
			}
			cands = append(cands, candidate{f.Name(), info.ModTime(), info.Size()})
		}
		sort.Slice(cands, func(a, b int) bool { return cands[a].mod.After(cands[b].mod) })
		if perProject > 0 && len(cands) > perProject {
			cands = cands[:perProject]
		}
		for _, c := range cands {
			path := filepath.Join(dir, c.name)
			seen[path] = true
			sum, ok := s.summarize(path, c.mod, c.size)
			if !ok || sum.cwd == "" {
				continue
			}
			e := Entry{
				ID:          strings.TrimSuffix(c.name, ".jsonl"),
				Dir:         d.Name(),
				CWD:         sum.cwd,
				Title:       sum.title(),
				CustomTitle: sum.customTitle,
				Headless:    sum.entrypoint == "sdk-cli",
				LastPrompt:  sum.lastPrompt,
				Updated:     c.mod,
				Path:        path,
			}
			if prev, ok := byID[e.ID]; !ok || e.Updated.After(prev.Updated) {
				byID[e.ID] = e
			}
		}
	}
	for path := range s.cache {
		if !seen[path] {
			delete(s.cache, path)
		}
	}

	out := make([]Entry, 0, len(byID))
	for _, e := range byID {
		out = append(out, e)
	}
	sort.Slice(out, func(a, b int) bool {
		if !out[a].Updated.Equal(out[b].Updated) {
			return out[a].Updated.After(out[b].Updated)
		}
		return out[a].ID < out[b].ID
	})
	return out, nil
}

// summarize returns what a transcript says about its session, from the cache when the file
// did not change.
func (s *Scanner) summarize(path string, mod time.Time, size int64) (summary, bool) {
	if c, ok := s.cache[path]; ok && c.mod.Equal(mod) && c.size == size {
		return c.summary, true
	}
	sum, err := readSummary(path, size)
	if err != nil {
		return summary{}, false
	}
	s.cache[path] = cachedSummary{mod: mod, size: size, summary: sum}
	return sum, true
}

// summary is what a transcript tells about its session.
type summary struct {
	cwd         string
	customTitle string
	firstPrompt string
	lastPrompt  string
	// entrypoint is how the session was started: "cli" in a terminal, "sdk-cli" headless.
	entrypoint string
}

// title picks the display title: the custom title, else the first prompt, else a
// placeholder.
func (h summary) title() string {
	if h.customTitle != "" {
		return Shorten(h.customTitle, 48)
	}
	if h.firstPrompt != "" {
		return Shorten(h.firstPrompt, 48)
	}
	return "(untitled)"
}

// headLines bounds how much of the beginning of a transcript is read: the working
// directory and the first prompt are within the first few entries.
const headLines = 80

// tailBytes is how much of the end of a transcript is read for the latest custom title
// (/rename appends one) and the latest prompt. Transcripts can be very large.
const tailBytes = 256 * 1024

// readSummary parses the beginning and the end of a transcript.
func readSummary(path string, size int64) (summary, error) {
	f, err := os.Open(path)
	if err != nil {
		return summary{}, err
	}
	defer f.Close()
	sum := ParseHead(f)
	start := size - tailBytes
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return sum, nil
	}
	tail, err := io.ReadAll(io.LimitReader(f, tailBytes))
	if err != nil {
		return sum, nil
	}
	parseTail(tail, &sum)
	return sum, nil
}

// transcriptLine is the subset of a transcript entry that matters here.
type transcriptLine struct {
	Type        string          `json:"type"`
	CWD         string          `json:"cwd"`
	CustomTitle string          `json:"customTitle"`
	Entrypoint  string          `json:"entrypoint"`
	LastPrompt  string          `json:"lastPrompt"`
	IsMeta      bool            `json:"isMeta"`
	Message     json.RawMessage `json:"message"`
}

// ParseHead reads up to headLines entries from the beginning of a transcript.
func ParseHead(r io.Reader) summary {
	var h summary
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for i := 0; i < headLines && sc.Scan(); i++ {
		var line transcriptLine
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			continue
		}
		if h.cwd == "" && line.CWD != "" {
			h.cwd = line.CWD
		}
		if h.entrypoint == "" && line.Entrypoint != "" {
			h.entrypoint = line.Entrypoint
		}
		switch line.Type {
		case "custom-title":
			h.customTitle = strings.TrimSpace(line.CustomTitle)
		case "last-prompt":
			if p := CleanPrompt(line.LastPrompt); p != "" {
				h.lastPrompt = p
			}
		case "user":
			if h.firstPrompt != "" || line.IsMeta {
				continue
			}
			if p := CleanPrompt(messageText(line.Message)); p != "" {
				h.firstPrompt = p
			}
		}
	}
	return h
}

// parseTail updates the summary with the latest custom title and prompt found in the end of
// a transcript. The first line may be cut and is skipped when it does not parse.
func parseTail(tail []byte, sum *summary) {
	for _, raw := range bytes.Split(tail, []byte("\n")) {
		if !bytes.Contains(raw, []byte(`"custom-title"`)) && !bytes.Contains(raw, []byte(`"last-prompt"`)) {
			continue
		}
		var line transcriptLine
		if err := json.Unmarshal(raw, &line); err != nil {
			continue
		}
		switch line.Type {
		case "custom-title":
			if t := strings.TrimSpace(line.CustomTitle); t != "" {
				sum.customTitle = t
			}
		case "last-prompt":
			if p := CleanPrompt(line.LastPrompt); p != "" {
				sum.lastPrompt = p
			}
		}
	}
}

// TitleWatcher reports titles set with /rename in transcripts that are being written,
// reading only what was appended since the previous check. It is not safe for concurrent
// use.
type TitleWatcher struct {
	offsets map[string]int64
}

// NewTitleWatcher returns a TitleWatcher that knows no transcript yet.
func NewTitleWatcher() *TitleWatcher {
	return &TitleWatcher{offsets: make(map[string]int64)}
}

// Check returns, by path, the latest custom title appended to each transcript since the
// previous check, for those that got one. A transcript seen for the first time is only
// measured; one no longer passed is forgotten.
func (w *TitleWatcher) Check(paths []string) map[string]string {
	titles := make(map[string]string)
	watched := make(map[string]bool, len(paths))
	for _, path := range paths {
		watched[path] = true
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		size := info.Size()
		offset, known := w.offsets[path]
		if !known || size < offset {
			w.offsets[path] = size
			continue
		}
		if size == offset {
			continue
		}
		title, end, err := appendedTitle(path, offset, size)
		if err != nil {
			continue
		}
		w.offsets[path] = end
		if title != "" {
			titles[path] = title
		}
	}
	for path := range w.offsets {
		if !watched[path] {
			delete(w.offsets, path)
		}
	}
	return titles
}

// appendedTitle returns the last custom title in a transcript between offset and size (at
// most tailBytes of it), and where to continue from: the end of the last complete line,
// so a line still being written is read again next time.
func appendedTitle(path string, offset, size int64) (string, int64, error) {
	if size-offset > tailBytes {
		offset = size - tailBytes
	}
	f, err := os.Open(path)
	if err != nil {
		return "", offset, err
	}
	defer f.Close()
	buf := make([]byte, size-offset)
	n, err := f.ReadAt(buf, offset)
	if err != nil && err != io.EOF {
		return "", offset, err
	}
	complete := buf[:bytes.LastIndexByte(buf[:n], '\n')+1]
	var sum summary
	parseTail(complete, &sum)
	return sum.customTitle, offset + int64(len(complete)), nil
}

// messageText extracts the text of a user message: either a string or a list of content
// blocks with "text" fields.
func messageText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var msg struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil || len(msg.Content) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(msg.Content, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, " ")
}

var (
	// hiddenBlocks are wrappers whose content is not what the user typed.
	hiddenBlocks = regexp.MustCompile(`(?s)<(system-reminder|command-message|local-command-[a-z-]+)>.*?</(system-reminder|command-message|local-command-[a-z-]+)>`)
	anyTag       = regexp.MustCompile(`<[^>]+>`)
	whitespace   = regexp.MustCompile(`\s+`)
)

// CleanPrompt strips the markup Claude Code wraps around prompts: system reminders and
// command echoes go, other tags (pasted content, slash command name and arguments) keep
// their text. Whitespace is collapsed.
func CleanPrompt(s string) string {
	s = hiddenBlocks.ReplaceAllString(s, " ")
	s = anyTag.ReplaceAllString(s, " ")
	return strings.TrimSpace(whitespace.ReplaceAllString(s, " "))
}

// Shorten truncates s to at most n runes, adding an ellipsis.
func Shorten(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
