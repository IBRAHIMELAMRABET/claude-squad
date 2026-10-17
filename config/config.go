package config

import (
	"claude-squad/log"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	ConfigFileName = "config.json"
	defaultProgram = "claude"
	// DefaultMaxInstances is the session cap used when max_instances is unset.
	DefaultMaxInstances = 10
	// worktreesNextToRepoDir is the folder created beside a repository when
	// worktree_next_to_repo is enabled.
	worktreesNextToRepoDir = ".claude-squad-worktrees"
)

// GetConfigDir returns the path to the application's configuration directory
func GetConfigDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get config home directory: %w", err)
	}
	return filepath.Join(homeDir, ".claude-squad"), nil
}

// Profile represents a named program configuration
type Profile struct {
	Name    string `json:"name"`
	Program string `json:"program"`
}

// Config represents the application configuration
type Config struct {
	// DefaultProgram is the default program to run in new instances
	DefaultProgram string `json:"default_program"`
	// AutoYes is a flag to automatically accept all prompts.
	AutoYes bool `json:"auto_yes"`
	// DaemonPollInterval is the interval (ms) at which the daemon polls sessions for autoyes mode.
	DaemonPollInterval int `json:"daemon_poll_interval"`
	// BranchPrefix is the prefix used for git branches created by the application.
	BranchPrefix string `json:"branch_prefix"`
	// Profiles is a list of named program profiles.
	Profiles []Profile `json:"profiles,omitempty"`
	// MaxInstances caps the number of sessions. Zero falls back to DefaultMaxInstances.
	MaxInstances int `json:"max_instances"`
	// WorktreeDir is where worktrees are created. Empty means <config dir>/worktrees.
	// A leading "~" is expanded to the home directory.
	WorktreeDir string `json:"worktree_dir,omitempty"`
	// WorktreeNextToRepo places each worktree under ".claude-squad-worktrees/<repo>" beside
	// its repository instead of WorktreeDir, keeping worktrees on the repo's filesystem.
	WorktreeNextToRepo bool `json:"worktree_next_to_repo,omitempty"`
	// ProjectsRoot is a directory whose first-level folders name projects: a repository at
	// <root>/<Project>/... belongs to <Project> unless an explicit project claims it.
	ProjectsRoot string `json:"projects_root,omitempty"`
	// Projects maps repositories to named groups. See Project and ProjectFor.
	Projects []Project `json:"projects,omitempty"`
	// DisableHooks stops cs from passing its hooks settings file to claude sessions it
	// launches. Status then comes from the screen only.
	DisableHooks bool `json:"disable_hooks,omitempty"`
	// Notifications controls how cs signals that a session started waiting for you.
	// Omitted means both signals are on.
	Notifications *Notifications `json:"notifications,omitempty"`
	// DisableAdopt stops cs from listing agent sessions running in tmux sessions it did
	// not create.
	DisableAdopt bool `json:"disable_adopt,omitempty"`
	// AdoptPrograms lists the foreground program names that mark a foreign tmux pane as an
	// agent session worth listing. Default: ["claude"].
	AdoptPrograms []string `json:"adopt_programs,omitempty"`
	// DefaultMode is how new sessions run: ModeWorktree (isolated worktree and branch, the
	// default) or ModeInPlace (directly in the repository on its current branch).
	DefaultMode string `json:"default_mode,omitempty"`
	// ClaudeArgs are extra command-line arguments added to claude sessions started by cs,
	// for example `--plugin-dir <dir>` to load plugins for those sessions only. A leading
	// "~" is expanded.
	ClaudeArgs []string `json:"claude_args,omitempty"`
	// ClaudeSettings is merged into the settings file passed to claude sessions started by
	// cs (`--settings`). Use it for machine-specific Claude settings that must not live in
	// the shared ~/.claude/settings.json, such as marketplaces under WSL-only paths.
	ClaudeSettings json.RawMessage `json:"claude_settings,omitempty"`
	// DisableHistory hides past Claude Code sessions (from ~/.claude/projects) in the list.
	DisableHistory bool `json:"disable_history,omitempty"`
	// HistoryLimit caps how many past sessions per project directory are listed. Zero (the
	// default) lists them all.
	HistoryLimit int `json:"history_limit,omitempty"`
	// HistoryDays hides past sessions older than this many days. Zero (the default) lists
	// them all.
	HistoryDays int `json:"history_days,omitempty"`
	// HistoryIgnore lists path fragments; past sessions whose directory contains one are
	// not listed. Omitted means DefaultHistoryIgnore (temporary and scratch folders).
	HistoryIgnore []string `json:"history_ignore,omitempty"`
	// GroupBy chooses how sessions are grouped: GroupByProject (default: the projects and
	// projects_root rules, folders otherwise) or GroupByFolder (always by folder, like a
	// session browser).
	GroupBy string `json:"group_by,omitempty"`
	// GroupLabelSegments is how many trailing path segments name a folder group. Zero means
	// DefaultGroupLabelSegments.
	GroupLabelSegments int `json:"group_label_segments,omitempty"`
	// SidebarWidth is the width of the session list pane in the sidebar layout. Zero means
	// DefaultSidebarWidth.
	SidebarWidth int `json:"sidebar_width,omitempty"`
	// SidebarDrawer, in the sidebar layout, zooms the session to the full window whenever
	// it takes focus, so the list only shows when you move back to it (Alt+Left). Omitted
	// means on.
	SidebarDrawer *bool `json:"sidebar_drawer,omitempty"`
}

const (
	// GroupByProject groups by the projects/projects_root rules, then by folder.
	GroupByProject = "project"
	// GroupByFolder always groups by folder.
	GroupByFolder = "folder"
	// DefaultGroupLabelSegments is how many trailing path segments name a folder group.
	DefaultGroupLabelSegments = 3
)

// GroupFor returns the project a path is grouped under, or "" when it is grouped by its
// folder.
func (c *Config) GroupFor(path string) string {
	if c.GroupBy == GroupByFolder {
		return ""
	}
	return c.ProjectFor(path)
}

// GetGroupLabelSegments returns how many trailing path segments name a folder group.
func (c *Config) GetGroupLabelSegments() int {
	if c.GroupLabelSegments <= 0 {
		return DefaultGroupLabelSegments
	}
	return c.GroupLabelSegments
}

// DefaultHistoryIgnore hides sessions that ran in temporary folders.
var DefaultHistoryIgnore = []string{"/AppData/Local/Temp/", "/tmp/"}

// HistoryIgnored reports whether a past session recorded in dir should be hidden.
func (c *Config) HistoryIgnored(dir string) bool {
	patterns := c.HistoryIgnore
	if patterns == nil {
		patterns = DefaultHistoryIgnore
	}
	dir = filepath.ToSlash(dir) + "/" // so a fragment with a trailing slash matches the directory itself
	for _, p := range patterns {
		if p != "" && strings.Contains(dir, p) {
			return true
		}
	}
	return false
}

// GetHistoryLimit returns the per-project cap on past sessions; zero means no cap.
func (c *Config) GetHistoryLimit() int {
	if c.HistoryLimit < 0 {
		return 0
	}
	return c.HistoryLimit
}

// GetHistoryMaxAge returns how old a past session may be to be listed; zero means no limit.
func (c *Config) GetHistoryMaxAge() time.Duration {
	if c.HistoryDays <= 0 {
		return 0
	}
	return time.Duration(c.HistoryDays) * 24 * time.Hour
}

// DefaultSidebarWidth is the width of the session list pane.
const DefaultSidebarWidth = 32

// GetSidebarWidth returns the session list pane width.
func (c *Config) GetSidebarWidth() int {
	if c.SidebarWidth < 20 {
		return DefaultSidebarWidth
	}
	return c.SidebarWidth
}

// DrawerSidebar reports whether the sidebar behaves as a drawer (session zoomed on focus).
func (c *Config) DrawerSidebar() bool {
	return c.SidebarDrawer == nil || *c.SidebarDrawer
}

const (
	// ModeWorktree runs each session in its own git worktree and branch.
	ModeWorktree = "worktree"
	// ModeInPlace runs the session directly in the repository on its current branch.
	ModeInPlace = "inplace"
)

// InPlaceByDefault reports whether new sessions run in place rather than in a worktree.
func (c *Config) InPlaceByDefault() bool {
	return c.DefaultMode == ModeInPlace
}

// GetAdoptPrograms returns the program names that qualify a foreign tmux pane for
// adoption, defaulting to claude.
func (c *Config) GetAdoptPrograms() []string {
	if len(c.AdoptPrograms) == 0 {
		return []string{defaultProgram}
	}
	return c.AdoptPrograms
}

// Notifications are the signals emitted when a session starts waiting for the user.
type Notifications struct {
	// Bell rings the terminal bell.
	Bell bool `json:"bell"`
	// Title puts the number of waiting sessions in the terminal window title.
	Title bool `json:"title"`
}

// GetNotifications returns the notification settings, defaulting to both on.
func (c *Config) GetNotifications() Notifications {
	if c.Notifications == nil {
		return Notifications{Bell: true, Title: true}
	}
	return *c.Notifications
}

// GetMaxInstances returns the session cap, falling back to DefaultMaxInstances.
func (c *Config) GetMaxInstances() int {
	if c.MaxInstances <= 0 {
		return DefaultMaxInstances
	}
	return c.MaxInstances
}

// WorktreeDirFor returns the directory in which worktrees for repoPath are created.
// repoPath may be empty, in which case the global worktree directory is returned.
func (c *Config) WorktreeDirFor(repoPath string) (string, error) {
	if c.WorktreeNextToRepo && repoPath != "" {
		return filepath.Join(filepath.Dir(repoPath), worktreesNextToRepoDir, filepath.Base(repoPath)), nil
	}
	if c.WorktreeDir != "" {
		return ExpandHome(c.WorktreeDir)
	}
	configDir, err := GetConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "worktrees"), nil
}

// ExpandHome replaces a leading "~" with the user's home directory.
func ExpandHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to expand ~ in %q: %w", path, err)
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~")), nil
}

// GetProgram returns the program to run. If Profiles is non-empty and
// DefaultProgram matches a profile name, that profile's Program is returned.
// Otherwise DefaultProgram is returned as-is.
func (c *Config) GetProgram() string {
	for _, p := range c.Profiles {
		if p.Name == c.DefaultProgram {
			return p.Program
		}
	}
	return c.DefaultProgram
}

// GetProfiles returns a unified list of profiles. If Profiles is defined,
// those are returned with the default profile first. Otherwise, a single
// profile is synthesized from DefaultProgram.
func (c *Config) GetProfiles() []Profile {
	if len(c.Profiles) == 0 {
		return []Profile{{Name: c.DefaultProgram, Program: c.DefaultProgram}}
	}
	// Reorder so the default profile comes first.
	profiles := make([]Profile, 0, len(c.Profiles))
	for _, p := range c.Profiles {
		if p.Name == c.DefaultProgram {
			profiles = append(profiles, p)
			break
		}
	}
	for _, p := range c.Profiles {
		if p.Name != c.DefaultProgram {
			profiles = append(profiles, p)
		}
	}
	return profiles
}

// DefaultConfig returns the default configuration
func DefaultConfig() *Config {
	program, err := GetClaudeCommand()
	if err != nil {
		log.ErrorLog.Printf("failed to get claude command: %v", err)
		program = defaultProgram
	}

	return &Config{
		DefaultProgram:     program,
		AutoYes:            false,
		DaemonPollInterval: 1000,
		MaxInstances:       DefaultMaxInstances,
		Notifications:      &Notifications{Bell: true, Title: true},
		BranchPrefix: func() string {
			user, err := user.Current()
			if err != nil || user == nil || user.Username == "" {
				log.ErrorLog.Printf("failed to get current user: %v", err)
				return "session/"
			}
			return fmt.Sprintf("%s/", strings.ToLower(user.Username))
		}(),
	}
}

// GetClaudeCommand attempts to find the "claude" command in the user's shell
// It checks in the following order:
// 1. Shell alias resolution: using "which" command
// 2. PATH lookup
//
// If both fail, it returns an error.
func GetClaudeCommand() (string, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash" // Default to bash if SHELL is not set
	}

	// Force the shell to load the user's profile and then run the command
	// For zsh, source .zshrc; for bash, source .bashrc
	var shellCmd string
	if strings.Contains(shell, "zsh") {
		shellCmd = "source ~/.zshrc &>/dev/null || true; which claude"
	} else if strings.Contains(shell, "bash") {
		shellCmd = "source ~/.bashrc &>/dev/null || true; which claude"
	} else {
		shellCmd = "which claude"
	}

	cmd := exec.Command(shell, "-c", shellCmd)
	output, err := cmd.Output()
	if err == nil && len(output) > 0 {
		path := strings.TrimSpace(string(output))
		if path != "" {
			// Check if the output is an alias definition and extract the actual path
			// Handle formats like "claude: aliased to /path/to/claude" or other shell-specific formats
			aliasRegex := regexp.MustCompile(`(?:aliased to|->|=)\s*([^\s]+)`)
			matches := aliasRegex.FindStringSubmatch(path)
			if len(matches) > 1 {
				path = matches[1]
			}
			return path, nil
		}
	}

	// Otherwise, try to find in PATH directly
	claudePath, err := exec.LookPath("claude")
	if err == nil {
		return claudePath, nil
	}

	return "", fmt.Errorf("claude command not found in aliases or PATH")
}

func LoadConfig() *Config {
	configDir, err := GetConfigDir()
	if err != nil {
		log.ErrorLog.Printf("failed to get config directory: %v", err)
		return DefaultConfig()
	}

	configPath := filepath.Join(configDir, ConfigFileName)
	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Create and save default config if file doesn't exist
			defaultCfg := DefaultConfig()
			if saveErr := saveConfig(defaultCfg); saveErr != nil {
				log.WarningLog.Printf("failed to save default config: %v", saveErr)
			}
			return defaultCfg
		}

		log.WarningLog.Printf("failed to get config file: %v", err)
		return DefaultConfig()
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		log.ErrorLog.Printf("failed to parse config file: %v", err)
		return DefaultConfig()
	}

	return &config
}

// saveConfig saves the configuration to disk
func saveConfig(config *Config) error {
	configDir, err := GetConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get config directory: %w", err)
	}

	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	configPath := filepath.Join(configDir, ConfigFileName)
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	return os.WriteFile(configPath, data, 0644)
}

// SaveConfig exports the saveConfig function for use by other packages
func SaveConfig(config *Config) error {
	return saveConfig(config)
}
