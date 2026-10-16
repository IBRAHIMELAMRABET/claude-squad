package app

import (
	"claude-squad/config"
	"claude-squad/session"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func initTestRepo(t *testing.T, parent, name string) string {
	t.Helper()
	repo := filepath.Join(parent, name)
	require.NoError(t, os.MkdirAll(repo, 0755))
	require.NoError(t, exec.Command("git", "-C", repo, "init", "-q").Run())
	resolved, err := filepath.EvalSymlinks(repo)
	require.NoError(t, err)
	return resolved
}

func TestKnownRepos(t *testing.T) {
	base := t.TempDir()
	alpha := initTestRepo(t, base, "alpha")
	beta := initTestRepo(t, base, "beta")
	gamma := initTestRepo(t, base, "gamma")
	require.NoError(t, os.MkdirAll(filepath.Join(alpha, "sub"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(base, "plain"), 0755))

	cfg := &config.Config{Projects: []config.Project{
		{Name: "B", Paths: []string{beta}},
		{Name: "All", Paths: []string{filepath.Join(base, "*")}}, // alpha, beta, gamma, plain
	}}
	inst, err := session.NewInstance(session.InstanceOptions{Title: "x", Path: gamma, Program: "claude"})
	require.NoError(t, err)

	got := knownRepos(filepath.Join(alpha, "sub"), cfg, []*session.Instance{inst})
	require.Equal(t, []string{alpha, beta, gamma}, got, "cwd's repo first, then config, no duplicates, non-repos skipped")

	got = knownRepos(filepath.Join(base, "plain"), &config.Config{}, nil)
	require.Empty(t, got, "no repository anywhere")
}

func TestExpandPattern(t *testing.T) {
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join(home, "work")}, expandPattern("~/work/**"))
	require.Equal(t, []string{"/srv/app"}, expandPattern("/srv/app"))

	base := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(base, "one"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(base, "two"), 0755))
	require.ElementsMatch(t, []string{filepath.Join(base, "one"), filepath.Join(base, "two")}, expandPattern(filepath.Join(base, "*")))
}

func TestCanStartWithoutRepo(t *testing.T) {
	require.False(t, CanStartWithoutRepo(&config.Config{}, false))
	require.True(t, CanStartWithoutRepo(&config.Config{}, true))
	require.True(t, CanStartWithoutRepo(&config.Config{ProjectsRoot: "/p"}, false))
	require.True(t, CanStartWithoutRepo(&config.Config{Projects: []config.Project{{Name: "x"}}}, false))
}

func TestReposUnderRoot(t *testing.T) {
	root := t.TempDir()
	deep := initTestRepo(t, filepath.Join(root, "Fawri", "Code"), "Backend")
	top := initTestRepo(t, root, "Umnia")
	initTestRepo(t, filepath.Join(root, "Fawri", "Code", "Backend"), "nested-ignored") // inside a repo
	initTestRepo(t, filepath.Join(root, ".hidden"), "skipped")
	initTestRepo(t, filepath.Join(root, "a", "b", "c"), "too-deep")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "Platform", "node_modules", "x"), 0755))

	resolvedRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	got := reposUnder(resolvedRoot, 3)
	require.ElementsMatch(t, []string{deep, top}, got)

	cfg := &config.Config{ProjectsRoot: resolvedRoot}
	require.ElementsMatch(t, []string{deep, top}, knownRepos("", cfg, nil), "projects_root feeds the picker")
}
