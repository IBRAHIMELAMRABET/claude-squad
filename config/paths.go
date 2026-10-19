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

// wslSharePath matches a path into a WSL distribution as Windows sees it, such as
// `\\wsl.localhost\Ubuntu\home\me\repo` or `\\wsl$\Ubuntu\home\me\repo`.
var wslSharePath = regexp.MustCompile(`(?i)^[\\/]{2}wsl(?:\.localhost|\$)[\\/]([^\\/]+)(.*)$`)

// normalizePath is NormalizePath with the platform and mount check injected for tests.
func normalizePath(p, goos string, mountExists func(string) bool) string {
	if goos == "windows" {
		return p
	}
	// A path into this distribution recorded by a Windows program (the Windows claude.exe
	// started in a WSL directory) is the Linux path it points to.
	if m := wslSharePath.FindStringSubmatch(p); m != nil {
		if distro := os.Getenv("WSL_DISTRO_NAME"); distro == "" || strings.EqualFold(distro, m[1]) {
			rest := strings.ReplaceAll(m[2], `\`, "/")
			if rest == "" {
				rest = "/"
			}
			return filepath.Clean(rest)
		}
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
