package main

import (
	"claude-squad/app"
	cmd2 "claude-squad/cmd"
	"claude-squad/config"
	"claude-squad/daemon"
	"claude-squad/hooks"
	"claude-squad/log"
	"claude-squad/session"
	"claude-squad/session/git"
	"claude-squad/session/tmux"
	"claude-squad/sidebar"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	version     = "1.0.20"
	programFlag string
	autoYesFlag bool
	daemonFlag  bool
	classicFlag bool
	sidebarFlag bool
	binName     string
	rootCmd     = &cobra.Command{
		Use:   "claude-squad",
		Short: "Claude Squad - Manage multiple AI agents like Claude Code, Aider, Codex, and Amp.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			log.Initialize(daemonFlag)
			defer log.Close()

			if daemonFlag {
				cfg := config.LoadConfig()
				err := daemon.RunDaemon(cfg)
				log.ErrorLog.Printf("failed to start daemon %v", err)
				return err
			}

			// Check if we're in a git repository
			currentDir, err := filepath.Abs(".")
			if err != nil {
				return fmt.Errorf("failed to get current directory: %w", err)
			}

			cfg := config.LoadConfig()

			// Outside a git repository cs can still show stored sessions and create new ones in
			// configured projects; with nothing configured it would be an empty screen.
			if !git.IsGitRepo(currentDir) {
				hasInstances := string(config.LoadState().GetInstances()) != "[]"
				if !app.CanStartWithoutRepo(cfg, hasInstances) {
					return fmt.Errorf("error: %s must be run from within a git repository "+
						"(or configure projects in config.json to pick a repository)", binName)
				}
			}

			// Program flag overrides config
			program := cfg.GetProgram()
			if programFlag != "" {
				program = programFlag
			}
			// AutoYes flag overrides config
			autoYes := cfg.AutoYes
			if autoYesFlag {
				autoYes = true
			}
			if autoYes {
				defer func() {
					if err := daemon.LaunchDaemon(); err != nil {
						log.ErrorLog.Printf("failed to launch daemon: %v", err)
					}
				}()
			}
			// Kill any daemon that's running.
			if err := daemon.StopDaemon(); err != nil {
				log.ErrorLog.Printf("failed to stop daemon: %v", err)
			}

			if sidebarFlag {
				return app.RunWithOptions(ctx, program, autoYes, app.Options{Sidebar: true})
			}
			if classicFlag || runtime.GOOS == "windows" {
				return app.Run(ctx, program, autoYes)
			}
			// Sidebar layout: an outer tmux session with the list on the left and the selected
			// session on the right. Launch replaces this process with the tmux client.
			execPath, err := os.Executable()
			if err != nil {
				return fmt.Errorf("failed to locate the executable: %w", err)
			}
			return sidebar.Launch(sidebarCommand(execPath, os.Args[1:]))
		},
	}

	resetCmd = &cobra.Command{
		Use:   "reset",
		Short: "Reset all stored instances",
		RunE: func(cmd *cobra.Command, args []string) error {
			log.Initialize(false)
			defer log.Close()

			state := config.LoadState()
			storage, err := session.NewStorage(state)
			if err != nil {
				return fmt.Errorf("failed to initialize storage: %w", err)
			}
			if err := storage.DeleteAllInstances(); err != nil {
				return fmt.Errorf("failed to reset storage: %w", err)
			}
			fmt.Println("Storage has been reset successfully")

			if err := tmux.CleanupSessions(cmd2.MakeExecutor()); err != nil {
				return fmt.Errorf("failed to cleanup tmux sessions: %w", err)
			}
			fmt.Println("Tmux sessions have been cleaned up")

			if err := git.CleanupWorktrees(); err != nil {
				return fmt.Errorf("failed to cleanup worktrees: %w", err)
			}
			fmt.Println("Worktrees have been cleaned up")

			// Kill any daemon that's running.
			if err := daemon.StopDaemon(); err != nil {
				return err
			}
			fmt.Println("daemon has been stopped")

			return nil
		},
	}

	debugCmd = &cobra.Command{
		Use:   "debug",
		Short: "Print debug information like config paths",
		RunE: func(cmd *cobra.Command, args []string) error {
			log.Initialize(false)
			defer log.Close()

			cfg := config.LoadConfig()

			configDir, err := config.GetConfigDir()
			if err != nil {
				return fmt.Errorf("failed to get config directory: %w", err)
			}
			configJson, _ := json.MarshalIndent(cfg, "", "  ")

			fmt.Printf("Config: %s\n%s\n", filepath.Join(configDir, config.ConfigFileName), configJson)

			return nil
		},
	}

	hookPrintFlag bool
	hookCmd       = &cobra.Command{
		Use:   "hook",
		Short: "Record a Claude Code hook event (run by Claude, not by hand)",
		Long: "Reads a Claude Code hook payload on stdin and records the session state for the\n" +
			"tmux pane it runs in, so the session list can show which sessions are waiting.\n" +
			"Sessions started by " + "claude-squad" + " get this hook automatically. For sessions started\n" +
			"elsewhere, add the output of `hook --print` to ~/.claude/settings.json.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if hookPrintFlag {
				execPath, err := os.Executable()
				if err != nil {
					return err
				}
				data, err := hooks.Settings(hooks.HookCommand(execPath))
				if err != nil {
					return err
				}
				fmt.Println(string(data))
				return nil
			}
			dir, err := hooks.StatusDir()
			if err != nil {
				return err
			}
			return hooks.Record(dir, os.Stdin, os.Getenv("TMUX_PANE"), time.Now())
		},
	}

	versionCmd = &cobra.Command{
		Use:   "version",
		Short: "Print the version number",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("%s version %s\n", binName, version)
			fmt.Printf("https://github.com/smtg-ai/claude-squad/releases/tag/v%s\n", version)
		},
	}
)

// sidebarCommand is the shell command the sidebar pane runs: this binary with the same
// flags plus --sidebar.
func sidebarCommand(execPath string, args []string) string {
	parts := []string{shellQuote(execPath)}
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(append(parts, "--sidebar"), " ")
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'''`) + "'"
}

func init() {
	rootCmd.Flags().StringVarP(&programFlag, "program", "p", "",
		"Program to run in new instances (e.g. 'aider --model ollama_chat/gemma3:1b')")
	rootCmd.Flags().BoolVarP(&autoYesFlag, "autoyes", "y", false,
		"[experimental] If enabled, all instances will automatically accept prompts")
	rootCmd.Flags().BoolVar(&daemonFlag, "daemon", false, "Run a program that loads all sessions"+
		" and runs autoyes mode on them.")

	rootCmd.Flags().BoolVar(&classicFlag, "classic", false,
		"Use the classic single-window layout (list + preview tabs) instead of the tmux sidebar layout.")
	rootCmd.Flags().BoolVar(&sidebarFlag, "sidebar", false, "Run as the sidebar pane of the layout (internal).")

	// Hide the flags that are only for internal use
	for _, name := range []string{"daemon", "sidebar"} {
		if err := rootCmd.Flags().MarkHidden(name); err != nil {
			panic(err)
		}
	}

	rootCmd.AddCommand(debugCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(resetCmd)

	hookCmd.Flags().BoolVar(&hookPrintFlag, "print", false,
		"Print the hooks settings JSON to merge into ~/.claude/settings.json")
	rootCmd.AddCommand(hookCmd)
}

func main() {
	// Extract the binary name from how this was invoked
	binName = filepath.Base(os.Args[0])
	rootCmd.Use = binName

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
	}
}
