package app

import (
	"claude-squad/config"
	"claude-squad/session"
	"claude-squad/session/git"
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
	for _, inst := range instances {
		add(inst.ProjectPath())
	}
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
// there are configured projects to pick a repository from, or stored sessions to show.
func CanStartWithoutRepo(cfg *config.Config, hasInstances bool) bool {
	return hasInstances || len(cfg.Projects) > 0 || cfg.ProjectsRoot != ""
}
