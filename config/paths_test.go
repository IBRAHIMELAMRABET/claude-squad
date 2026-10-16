package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizePath(t *testing.T) {
	hasMnt := func(dir string) bool { return dir == "/mnt/c" || dir == "/mnt/d" }

	require.Equal(t, "/mnt/c/Users/me/Documents/repo", normalizePath(`C:\Users\me\Documents\repo`, "linux", hasMnt))
	require.Equal(t, "/mnt/d/work", normalizePath(`d:/work/`, "linux", hasMnt), "forward slashes and lower-case drives too")
	require.Equal(t, `E:\nope`, normalizePath(`E:\nope`, "linux", hasMnt), "no mount for that drive: unchanged")
	require.Equal(t, "/home/me/repo", normalizePath("/home/me/repo", "linux", hasMnt))
	require.Equal(t, `C:\Users\me`, normalizePath(`C:\Users\me`, "windows", hasMnt), "nothing to do on Windows")
	require.Equal(t, "", normalizePath("", "linux", hasMnt))
}
