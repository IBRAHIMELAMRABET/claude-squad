package app

import (
	"claude-squad/config"
	"claude-squad/session"
	"claude-squad/session/git"
	"os"
	"path/filepath"
	"strings"
)

// knownRepos returns the repositories a new session can be created in, without
// duplicates: the one containing cwd first, then the directories named in the project
// config (globs expanded), then the repositories of existing sessions. Paths that are not
// inside a git repository are skipped.
func knownRepos(cwd string, cfg *config.Config, instances []*session.Instance) []string {
	var out []string
	seen := make(map[string]bool)
	add := func(path string) {
		if path == "" {
			return
		}
		root, err := git.FindGitRepoRoot(path)
		if err != nil || seen[root] {
			return
		}
		seen[root] = true
		out = append(out, root)
	}

	add(cwd)
	for _, p := range cfg.Projects {
		for _, pattern := range p.Paths {
			for _, match := range expandPattern(pattern) {
				add(match)
			}
		}
	}
	if root, err := config.ExpandHome(cfg.ProjectsRoot); err == nil && root != "" {
		for _, repo := range reposUnder(root, 3) {
			add(repo)
		}
	}
	for _, inst := range instances {
		add(inst.ProjectPath())
	}
	return out
}

// selectedFolder is the folder of the row under the cursor: the folder a session runs in
// (its repository for a worktree session), or a folder group's folder. It is "" when
// there is none or it no longer exists.
func (m *home) selectedFolder() string {
	dir := ""
	if key, ok := m.list.SelectedHeader(); ok {
		if filepath.IsAbs(key) {
			dir = key
		}
	} else if inst := m.list.GetSelectedInstance(); inst != nil {
		dir = config.NormalizePath(inst.Path)
	}
	if dir == "" || !dirExists(dir) {
		return ""
	}
	return dir
}

// withFirst puts first ahead of rest, dropping its duplicate. An empty first is left out.
func withFirst(first string, rest []string) []string {
	if first == "" {
		return rest
	}
	out := []string{first}
	for _, p := range rest {
		if p != first {
			out = append(out, p)
		}
	}
	return out
}

// isRepo reports whether path is inside a git repository. Overridable in tests.
var isRepo = func(path string) bool {
	_, err := git.FindGitRepoRoot(path)
	return err == nil
}

// reposUnder lists git repositories (directories holding a .git entry) up to maxDepth levels
// below root, in sorted order. Hidden directories and node_modules are skipped, and the walk
// does not descend into a repository.
func reposUnder(root string, maxDepth int) []string {
	var out []string
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			out = append(out, dir)
			return
		}
		if depth == maxDepth {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() || strings.HasPrefix(name, ".") || name == "node_modules" {
				continue
			}
			walk(filepath.Join(dir, name), depth+1)
		}
	}
	walk(root, 0)
	return out
}

// expandPattern turns a project path entry into concrete directories: "~" is expanded, a
// trailing "/**" is dropped, and glob patterns are matched on disk.
func expandPattern(pattern string) []string {
	if expanded, err := config.ExpandHome(pattern); err == nil {
		pattern = expanded
	}
	pattern = strings.TrimSuffix(pattern, string(filepath.Separator)+"**")
	if !strings.ContainsAny(pattern, "*?[") {
		return []string{pattern}
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	return matches
}

// CanStartWithoutRepo reports whether cs may start outside a git repository: it can when
// there are configured projects to pick a repository from, stored sessions to show, or past
// sessions to list and start new ones next to.
func CanStartWithoutRepo(cfg *config.Config, hasInstances bool) bool {
	return hasInstances || len(cfg.Projects) > 0 || cfg.ProjectsRoot != "" || !cfg.DisableHistory
}
