// Package history lists past Claude Code sessions from the transcripts Claude keeps under
// ~/.claude/projects/<encoded cwd>/<session id>.jsonl, so they can be shown next to the
// running ones and resumed with `claude --resume <id>`.
package history

import (
	"bufio"
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
	// Title is the custom title set with /rename, or the first prompt, shortened.
	Title string
	// LastPrompt is the most recent prompt found in the head of the transcript, if any.
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

var sessionFile = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.jsonl$`)

// Scan returns the most recent sessions of every project under projectsDir: at most
// perProject per project directory, none older than maxAge when maxAge is positive, newest
// first. A session id seen in several directories is kept once, from its newest file.
func Scan(projectsDir string, perProject int, maxAge time.Duration) ([]Entry, error) {
	dirs, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil, err
	}
	cutoff := time.Time{}
	if maxAge > 0 {
		cutoff = time.Now().Add(-maxAge)
	}

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
			cands = append(cands, candidate{f.Name(), info.ModTime()})
		}
		sort.Slice(cands, func(a, b int) bool { return cands[a].mod.After(cands[b].mod) })
		if perProject > 0 && len(cands) > perProject {
			cands = cands[:perProject]
		}
		for _, c := range cands {
			path := filepath.Join(dir, c.name)
			head, err := readHead(path)
			if err != nil || head.cwd == "" {
				continue
			}
			e := Entry{
				ID:         strings.TrimSuffix(c.name, ".jsonl"),
				Dir:        d.Name(),
				CWD:        head.cwd,
				Title:      head.title(),
				LastPrompt: head.lastPrompt,
				Updated:    c.mod,
				Path:       path,
			}
			if prev, ok := byID[e.ID]; !ok || e.Updated.After(prev.Updated) {
				byID[e.ID] = e
			}
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

// head is what the first lines of a transcript tell about the session.
type head struct {
	cwd         string
	customTitle string
	firstPrompt string
	lastPrompt  string
}

// title picks the display title: the custom title, else the first prompt, else the id-less
// placeholder.
func (h head) title() string {
	if h.customTitle != "" {
		return Shorten(h.customTitle, 48)
	}
	if h.firstPrompt != "" {
		return Shorten(h.firstPrompt, 48)
	}
	return "(untitled)"
}

// headLines bounds how much of a transcript is read: the title, mode and first prompt are
// within the first few entries, and the files can be very large.
const headLines = 80

// readHead parses the beginning of a transcript.
func readHead(path string) (head, error) {
	f, err := os.Open(path)
	if err != nil {
		return head{}, err
	}
	defer f.Close()
	return ParseHead(f), nil
}

// transcriptLine is the subset of a transcript entry that matters here.
type transcriptLine struct {
	Type        string          `json:"type"`
	CWD         string          `json:"cwd"`
	CustomTitle string          `json:"customTitle"`
	LastPrompt  string          `json:"lastPrompt"`
	IsMeta      bool            `json:"isMeta"`
	Message     json.RawMessage `json:"message"`
}

// ParseHead reads up to headLines entries from a transcript.
func ParseHead(r io.Reader) head {
	var h head
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
	systemReminder = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)
	xmlTag         = regexp.MustCompile(`(?s)<[a-zA-Z][^>]*>.*?</[a-zA-Z][^>]*>|<[^>]+>`)
	whitespace     = regexp.MustCompile(`\s+`)
)

// CleanPrompt strips the markup Claude Code wraps around prompts (system reminders, command
// tags) and collapses whitespace.
func CleanPrompt(s string) string {
	s = systemReminder.ReplaceAllString(s, " ")
	s = xmlTag.ReplaceAllString(s, " ")
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
