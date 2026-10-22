package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// OtherProject is the display name used when a session has no path to group by at all.
const OtherProject = "Other"

// FolderGroupPath returns the cleaned path that groups a session no project claims, or ""
// when there is no usable path.
func FolderGroupPath(path string) string {
	p := cleanPath(path)
	if p == "" || p == "." || p == string(filepath.Separator) {
		return ""
	}
	return p
}

// windowsProfile matches a Windows user profile seen from WSL, e.g. /mnt/c/Users/me.
var windowsProfile = regexp.MustCompile(`^/mnt/[a-z]/Users/[^/]+$`)

// FolderGroupName names a folder group like a session browser does: the folder's own name,
// or "~" for a home directory (the user's home, or a Windows user profile seen from WSL).
// With segments > 1 the parent folders are included, to tell apart folders that share a
// name; a home directory is then shown by its path too.
func FolderGroupName(path string, segments int) string {
	p := FolderGroupPath(path)
	if p == "" {
		return ""
	}
	if segments <= 1 {
		if home, err := os.UserHomeDir(); err == nil && p == filepath.Clean(home) {
			return "~"
		}
		if windowsProfile.MatchString(filepath.ToSlash(p)) {
			return "~"
		}
		segments = 1
	}
	parts := strings.Split(strings.Trim(filepath.ToSlash(p), "/"), "/")
	if len(parts) > segments {
		parts = parts[len(parts)-segments:]
	}
	return strings.Join(parts, "/")
}

// Project groups sessions by the work they belong to rather than by repository.
type Project struct {
	// Name is shown as the group header.
	Name string `json:"name"`
	// Color is an optional hex color (e.g. "#4C9AFF") used for the group header.
	Color string `json:"color,omitempty"`
	// Paths lists directories or glob patterns. A directory matches itself and everything
	// beneath it. A pattern containing * ? or [ is matched with filepath.Match against the
	// path and each of its parents, and "dir/**" matches everything beneath dir.
	Paths []string `json:"paths"`
}

// ProjectFor returns the project name for a repository or working directory.
//
// Explicit projects win, in config order. Otherwise, when projects_root is set and the
// path lies beneath it, the first folder under the root names the project (so
// <root>/Orbit/Code/Backend belongs to "Orbit"). Anything else returns "", and the UI
// groups the session by its folder (see PathGroup).
func (c *Config) ProjectFor(path string) string {
	path = cleanPath(path)
	if path == "" {
		return ""
	}
	for _, p := range c.Projects {
		for _, pattern := range p.Paths {
			if pathMatches(pattern, path) {
				return p.Name
			}
		}
	}
	if root := cleanPath(c.ProjectsRoot); root != "" {
		if rel, ok := relativeTo(root, path); ok {
			first := strings.SplitN(rel, string(filepath.Separator), 2)[0]
			if first != "" && first != "." {
				return first
			}
		}
	}
	return ""
}

// ProjectColor returns the configured color for a project name, or "" when none is set.
func (c *Config) ProjectColor(name string) string {
	for _, p := range c.Projects {
		if p.Name == name {
			return p.Color
		}
	}
	return ""
}

// cleanPath expands ~ and normalises the path. Empty stays empty.
func cleanPath(p string) string {
	if p == "" {
		return ""
	}
	p = NormalizePath(p)
	if expanded, err := ExpandHome(p); err == nil {
		p = expanded
	}
	return filepath.Clean(p)
}

// pathMatches reports whether path is covered by pattern (see Project.Paths).
func pathMatches(pattern, path string) bool {
	pattern = cleanPath(pattern)
	if pattern == "" {
		return false
	}
	if base, ok := strings.CutSuffix(pattern, string(filepath.Separator)+"**"); ok {
		_, within := relativeTo(base, path)
		return within
	}
	if strings.ContainsAny(pattern, "*?[") {
		// Match the path itself and each parent so a cwd inside a matching repo still counts.
		for p := path; ; p = filepath.Dir(p) {
			if ok, err := filepath.Match(pattern, p); err == nil && ok {
				return true
			}
			if p == filepath.Dir(p) {
				return false
			}
		}
	}
	_, within := relativeTo(pattern, path)
	return within
}

// relativeTo reports whether path equals base or lies beneath it, returning the
// relative part ("." for base itself).
func relativeTo(base, path string) (string, bool) {
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}
