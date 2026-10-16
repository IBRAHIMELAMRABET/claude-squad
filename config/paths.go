package config

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

var windowsDrivePath = regexp.MustCompile(`^([A-Za-z]):[\\/]`)

// NormalizePath maps a Windows drive path such as `C:\Users\me\repo` to its WSL mount
// `/mnt/c/Users/me/repo` when running on Linux with that mount present. Other paths are
// returned unchanged. Claude Code transcripts written on Windows record Windows paths, and
// this lets them line up with the project configuration seen from WSL.
func NormalizePath(p string) string {
	return normalizePath(p, runtime.GOOS, func(dir string) bool {
		info, err := os.Stat(dir)
		return err == nil && info.IsDir()
	})
}

// normalizePath is NormalizePath with the platform and mount check injected for tests.
func normalizePath(p, goos string, mountExists func(string) bool) string {
	if goos == "windows" {
		return p
	}
	m := windowsDrivePath.FindStringSubmatch(p)
	if m == nil {
		return p
	}
	mount := "/mnt/" + strings.ToLower(m[1])
	if !mountExists(mount) {
		return p
	}
	rest := strings.ReplaceAll(p[len(m[0]):], `\`, "/")
	return filepath.Clean(mount + "/" + rest)
}
