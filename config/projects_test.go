package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectFor(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	cfg := &Config{
		ProjectsRoot: "/mnt/c/Projects",
		Projects: []Project{
			{Name: "Fawri", Color: "#4C9AFF", Paths: []string{"~/Fawri_Infra", "/srv/fawri/**"}},
			{Name: "Sentinel", Paths: []string{"/mnt/c/Projects/Platform/Sentinel*"}},
			{Name: "Shadow", Paths: []string{"/mnt/c/Projects/Fawri"}},
		},
	}

	cases := []struct {
		name, path, want string
	}{
		{"explicit directory", filepath.Join(home, "Fawri_Infra"), "Fawri"},
		{"explicit directory, cwd inside it", filepath.Join(home, "Fawri_Infra", "ansible"), "Fawri"},
		{"explicit directory does not match a sibling prefix", filepath.Join(home, "Fawri_Infra2"), ""},
		{"double-star pattern", "/srv/fawri/api", "Fawri"},
		{"double-star pattern matches the base itself", "/srv/fawri", "Fawri"},
		{"glob pattern", "/mnt/c/Projects/Platform/SentinelApi", "Sentinel"},
		{"glob pattern, cwd inside the repo", "/mnt/c/Projects/Platform/SentinelApi/src", "Sentinel"},
		{"glob pattern does not cross into unrelated folders", "/mnt/c/Projects/Platform/Other", "Platform"},
		{"explicit mapping wins over the folder rule", "/mnt/c/Projects/Fawri/Code/Backend", "Shadow"},
		{"folder rule: first segment under the root", "/mnt/c/Projects/CMIBills/Code/Backend", "CMIBills"},
		{"folder rule: repo directly under the root", "/mnt/c/Projects/Umnia", "Umnia"},
		{"folder rule: the root itself has no project", "/mnt/c/Projects", ""},
		{"outside everything", "/home/someone/scratch", ""},
		{"empty path", "", ""},
		{"unclean path", "/mnt/c/Projects//CMIBills/./Code/../Code", "CMIBills"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, cfg.ProjectFor(tc.path))
		})
	}
}

func TestProjectForWithoutConfig(t *testing.T) {
	assert.Equal(t, "", (&Config{}).ProjectFor("/any/where"))
}

func TestProjectColor(t *testing.T) {
	cfg := &Config{Projects: []Project{{Name: "Fawri", Color: "#4C9AFF"}, {Name: "Plain"}}}
	assert.Equal(t, "#4C9AFF", cfg.ProjectColor("Fawri"))
	assert.Equal(t, "", cfg.ProjectColor("Plain"))
	assert.Equal(t, "", cfg.ProjectColor("Unknown"))
}

func TestProjectsRoundTripThroughConfigFile(t *testing.T) {
	cfg := &Config{
		ProjectsRoot: "/mnt/c/Projects",
		Projects:     []Project{{Name: "Fawri", Color: "#4C9AFF", Paths: []string{"~/Fawri_Infra"}}},
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	require.NoError(t, err)
	var loaded Config
	require.NoError(t, json.Unmarshal(data, &loaded))
	assert.Equal(t, cfg.Projects, loaded.Projects)
	assert.Contains(t, string(data), `"projects_root": "/mnt/c/Projects"`)
	assert.Contains(t, string(data), `"paths": [`)
}

func TestFolderGroupName(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	assert.Equal(t, "repo", FolderGroupName("/home/me/work/repo", 1), "the folder's own name")
	assert.Equal(t, "work/repo", FolderGroupName("/home/me/work/repo", 2), "parents added to disambiguate")
	assert.Equal(t, "repo", FolderGroupName("/repo/", 1))
	assert.Equal(t, "~", FolderGroupName(home, 1), "the home directory")
	assert.Equal(t, "~", FolderGroupName("/mnt/c/Users/me", 1), "a Windows profile seen from WSL")
	assert.Equal(t, "Users/me", FolderGroupName("/mnt/c/Users/me", 2), "shown by path when disambiguating")
	assert.Equal(t, "", FolderGroupName("", 1))
	assert.Equal(t, "", FolderGroupName("/", 1))
	assert.Equal(t, "/home/me/work/repo", FolderGroupPath("/home/me/work/repo/"))
	assert.Equal(t, "", FolderGroupPath(""))
}
