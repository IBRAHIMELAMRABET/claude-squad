# Claude Squad [![CI](https://github.com/smtg-ai/claude-squad/actions/workflows/build.yml/badge.svg)](https://github.com/smtg-ai/claude-squad/actions/workflows/build.yml) [![GitHub Release](https://img.shields.io/github/v/release/smtg-ai/claude-squad)](https://github.com/smtg-ai/claude-squad/releases/latest)

[Claude Squad](https://smtg-ai.github.io/claude-squad/) is a terminal app that manages multiple [Claude Code](https://github.com/anthropics/claude-code), [Codex](https://github.com/openai/codex), [Gemini](https://github.com/google-gemini/gemini-cli) (and other local agents including [Aider](https://github.com/Aider-AI/aider)) in separate workspaces, allowing you to work on multiple tasks simultaneously.

> **This is a modified fork** of [smtg-ai/claude-squad](https://github.com/smtg-ai/claude-squad).
> It adds a sidebar layout with the real Claude Code session beside a project-grouped list,
> "waiting for you" detection, filters, adoption of Claude sessions started outside `cs`, and
> in-place sessions.
> The changes are listed in [Modifications in this fork](#modifications-in-this-fork), together
> with how to configure them and how to pull upstream updates. The license is unchanged
> (AGPL-3.0, see [LICENSE.md](LICENSE.md)).

![Claude Squad Screenshot](assets/screenshot.png)

### Highlights
- Complete tasks in the background (including yolo / auto-accept mode!)
- Manage instances and tasks in one terminal window
- Review changes before applying them, checkout changes before pushing them
- Each task gets its own isolated git workspace, so no conflicts

<br />

https://github.com/user-attachments/assets/aef18253-e58f-4525-9032-f5a3d66c975a

<br />

### Installation

The fork is installed from source (Go 1.25 or newer, tmux and git):

```bash
git clone https://github.com/IBRAHIMELAMRABET/claude-squad.git
cd claude-squad
go build -o ~/.local/bin/cs .
```

The upstream project is also available through Homebrew (`brew install claude-squad`) and an
install script, but those give you the upstream binary, not this fork.

### Prerequisites

- [tmux](https://github.com/tmux/tmux/wiki/Installing)
- [gh](https://cli.github.com/) (only for `p`, push)

### Usage

```
Usage:
  cs [flags]
  cs [command]

Available Commands:
  completion  Generate the autocompletion script for the specified shell
  debug       Print debug information like config paths
  help        Help about any command
  hook        Record a Claude Code hook event (run by Claude, not by hand)
  reset       Reset all stored instances
  version     Print the version number of claude-squad

Flags:
  -y, --autoyes          [experimental] If enabled, all instances will automatically accept prompts for claude code & aider
  -h, --help             help for claude-squad
  -p, --program string   Program to run in new instances (e.g. 'aider --model ollama_chat/gemma3:1b')
```

Run the application with:

```bash
cs
```
NOTE: The default program is `claude` and we recommend using the latest version.

`cs` normally runs from inside a git repository. With `projects` or `projects_root` configured
(see below) it also starts anywhere else, and new sessions are created in a repository you pick.

#### Layout

By default `cs` fills the terminal with the selected session, attached for real, and opens
the session list over its left edge. You type into Claude Code directly, with mouse,
scrollback and cursor.

```
┌ list ───────────────│ selected session ─────────────────────────┐
│  All 316  Active 2  │ ╭─ Claude Code ─────────────────────────╮ │
│ ▾ Bank/Code/…   │ │ ...                                    │ │
│ ● PERF TEST 2   now │ ╰────────────────────────────────────────╯ │
│ ○ CMI BackOffice 1h │ ❯ _                                        │
└─────────────────────┴────────────────────────────────────────────┘
```

- `Alt-S` shows or hides the list. The list is an overlay: opening or closing it never
  resizes the session, so Claude never has to redraw.
- Moving in the list shows that session behind it; `↵` opens it (the list closes).
- `tab` pages the session's diff, `t` opens a shell in its directory, `?` pages the help;
  leaving any of them returns to the session.
- `q` closes the layout; sessions keep running in tmux and reappear next time.
- `cs --classic` runs upstream's single-window layout (list, preview, diff and terminal tabs).

The list runs in its own tmux session (`claudesquad_list`), shown through a tmux popup; the
connection `cs` keeps to each agent session ignores its size, so only what you see sizes a
session.

#### Past sessions

The list also shows your past Claude Code sessions, read from the transcripts Claude keeps
under `~/.claude/projects` (the same store Switchboard reads), grouped by project like the
running ones and marked `○`. Selecting one shows its title, directory, date and last prompt;
`↵` resumes it in place with `claude --resume`, in the directory it was recorded in, and the
row turns into a running session. Sessions recorded on Windows resume from WSL too: Claude
finds the transcript by id and the reply lands in the same file.

Every session is listed by default (`history_limit` and `history_days` can cap them), titled
by its latest `/rename` or its first prompt. Runs without a terminal (`claude -p`, the SDK:
tests and automation) are left out unless `show_headless_sessions` is set, and folders can be
left out with `history_ignore`. `x` hides one past session; once something is hidden the
view bar shows `Hidden`, where `x` brings a session back. Transcripts are re-read only when
they change.

#### New sessions

`n` starts in the folder of the row under the cursor (a session's, or a folder group's);
`←/→` picks another known folder and `tab` switches between in place and worktree while you
type the name. `ctrl+o` types any folder instead (`C:\work\new`, `~/new`, with `tab`
completion); one that does not exist yet is created after a second `enter`. Any existing folder works; outside a git repository the session runs in
place. A new Claude session gets its own id and your name (`--session-id`, `-n`), so it opens
on a chat and can be resumed by that id later. A session whose program exits, or whose tmux
server went away, stays in the list paused; `↵` continues its conversation.

<br />

<b>Using Claude Squad with other AI assistants:</b>
- For [Codex](https://github.com/openai/codex): Set your API key with `export OPENAI_API_KEY=<your_key>`
- Launch with specific assistants:
   - Codex: `cs -p "codex"`
   - Aider: `cs -p "aider ..."`
   - Gemini: `cs -p "gemini"`
- Make this the default, by modifying the config file (locate with `cs debug`)

<br />

#### Menu
The menu at the bottom of the screen shows available commands:

##### Instance/Session Management
- `n` - Create a new session
- `N` - Create a new session with a prompt
- `D` - Kill (delete) the selected session
- `↑/j`, `↓/k` - Navigate between sessions
- While naming a new session: `←/→` pick the repository, `tab` switch between worktree and in-place

##### Actions
- `↵/o` - Attach to the selected session to reprompt
- `ctrl-q` - Detach from session
- `p` - Commit and push branch to github
- `c` - Checkout. Commits changes and pauses the session
- `r` - Resume a paused session
- `?` - Show help menu

##### List (fork)
- `space` / `←` `→` - Fold/unfold the group under the cursor (the cursor can sit on headers)
- `a` - Show active sessions only (hide paused)
- `/` - Filter sessions by text; `enter` keeps the filter, `esc` clears it
- `w` - Jump to the next session waiting for you
- `z` - Zoom the session pane (sidebar layout) or hide the list (classic layout)
- `t` - Open a shell in the session's directory (sidebar layout)

##### Navigation
- `tab` - Switch between preview tab and diff tab
- `q` - Quit the application
- `shift-↓/↑` - scroll in diff view

### Configuration

Claude Squad stores its configuration in `~/.claude-squad/config.json`. You can find the exact path by running `cs debug`.

#### Profiles

Profiles let you define multiple named program configurations and switch between them when creating a new session. When more than one profile is defined, the session creation overlay shows a profile picker that you can navigate with `←`/`→`.

To configure profiles, add a `profiles` array to your config file and set `default_program` to the name of the profile to select by default:

```json
{
  "default_program": "claude",
  "profiles": [
    { "name": "claude", "program": "claude" },
    { "name": "codex", "program": "codex" },
    { "name": "aider", "program": "aider --model ollama_chat/gemma3:1b" }
  ]
}
```

Each profile has two fields:

| Field     | Description                                              |
|-----------|----------------------------------------------------------|
| `name`    | Display name shown in the profile picker                 |
| `program` | Shell command used to launch the agent for that profile  |

If no profiles are defined, Claude Squad uses `default_program` directly as the launch command (the default is `claude`).

#### Fork settings

All of these are optional; without them the fork behaves like upstream plus grouping.

| Key | Default | Description |
|-----|---------|-------------|
| `projects_root` | unset | Directory whose first-level folders name projects: a repository at `<root>/<Project>/...` belongs to `<Project>`. Sessions matching no project are grouped by their folder, named after it (`~` for a home folder; a parent folder is added only when two names clash). |
| `projects` | `[]` | Explicit projects, checked before the folder rule. Each has a `name`, an optional `color` (hex) for its header, and `paths`: directories (matching everything beneath them) or glob patterns (`dir/**` matches everything under `dir`). |
| `worktree_dir` | `~/.claude-squad/worktrees` | Where worktrees are created. `~` is expanded. |
| `worktree_next_to_repo` | `false` | Put each worktree under `.claude-squad-worktrees/<repo>` beside its repository, so repo and worktree share a filesystem (useful for `/mnt/c` repositories under WSL). |
| `default_mode` | `"worktree"` | `"inplace"` makes new sessions run directly in the repository on its current branch. `tab` while naming a session switches per session. |
| `max_instances` | `10` | Maximum number of sessions. |
| `disable_hooks` | `false` | Do not pass the generated hooks settings file to `claude`; status then comes from the screen only. |
| `claude_args` | `[]` | Extra arguments for claude sessions started by `cs`, for example `["--plugin-dir", "~/mods/my-plugin"]` to load plugins for those sessions only, without touching the shared `~/.claude`. |
| `claude_settings` | unset | JSON object merged into that settings file, for machine-specific Claude settings that must not live in a shared `~/.claude/settings.json` (for example plugin marketplaces under WSL-only paths). The `hooks` key is reserved. |
| `sidebar_width` | `32` | Width of the session list, in columns. |
| `disable_history` | `false` | Do not list past Claude sessions from `~/.claude/projects`. |
| `history_limit` | `0` | Cap on past sessions per project directory; `0` lists them all. |
| `history_days` | `0` | Hide past sessions older than this many days; `0` lists them all. |
| `group_by` | `"project"` | `"folder"` always groups sessions by folder, like a session browser, ignoring `projects` and `projects_root` for grouping (they still feed the repository picker). |
| `group_label_segments` | `3` | How many trailing path segments name a folder group, e.g. `Bank/Code/Backend`. |
| `history_ignore` | `[]` | Directory fragments whose sessions are hidden. |
| `show_headless_sessions` | `false` | Also list runs without a terminal (`claude -p`, the SDK), which are tests and automation. |
| `show_scratchpad_sessions` | `false` | Also list sessions recorded in Claude Code scratchpad folders, which agents and tests start from inside another session. |
| `notifications` | `{"bell": true, "title": true}` | Ring the terminal bell when a session starts waiting, and keep the waiting count in the window title. |
| `disable_adopt` | `false` | Do not list agent sessions running in tmux sessions `cs` did not create. |
| `adopt_programs` | `["claude"]` | Foreground program names that qualify a foreign tmux pane for adoption. |

Example:

```json
{
  "default_program": "claude",
  "projects_root": "/home/me/Projects",
  "projects": [
    { "name": "Infra", "color": "#4C9AFF", "paths": ["~/infra", "/srv/ops/**"] },
    { "name": "Shop",  "color": "#F5A623", "paths": ["/home/me/Projects/Platform/Shop*"] }
  ],
  "worktree_next_to_repo": true,
  "default_mode": "worktree",
  "max_instances": 20,
  "notifications": { "bell": true, "title": true }
}
```

Folds, the active-only filter and the hidden list are remembered in `~/.claude-squad/state.json`.

#### How "waiting for you" is detected

Claude sessions started by `cs` are launched with `--settings ~/.claude-squad/hooks.json`, a
generated file that makes Claude Code run `cs hook` on its `UserPromptSubmit`, `PreToolUse`,
`PostToolUse`, `PermissionRequest`, `Notification`, `Stop` and `SessionEnd` events. The hook
records the event per tmux pane under `~/.claude-squad/status/`, and the list shows `!` with
a "waiting" count for sessions blocked on a permission prompt, a question, or whose turn
finished and has not been opened since. Settings passed with `--settings` are merged with your
own hooks, not replacing them.

Sessions that `cs` did not start (adopted ones) get the same precision if you add the hooks
to your global Claude settings once:

```bash
cs hook --print   # paste the "hooks" entries into ~/.claude/settings.json
```

Without hooks, `cs` falls back to reading Claude's screen: "esc to interrupt" means working,
its permission and selection dialogs mean waiting, "? for shortcuts" means idle. That fallback
depends on Claude Code's wording and may lag behind new Claude versions.

#### Running under WSL with Windows Claude

When Claude Code is installed on Windows, set `default_program` to the Windows binary, for
example `/mnt/c/Users/<you>/.local/bin/claude.exe`. `cs` then runs the real Windows Claude in
its panes through WSL interop: same settings, plugins, hooks, theme and transcripts as in a
PowerShell tab, and sessions recorded on Windows resume natively. Such sessions get only
`--resume` (no hooks file, no `claude_args`, whose paths would mean nothing on Windows), so
their status comes from the screen, and their transcript is matched by folder and start time.

#### Running under WSL

tmux lives inside the WSL distribution. When no WSL terminal stays open, Windows stops the
distribution after its idle timeout and every tmux session dies with it; `cs` then shows those
sessions as paused and `r` resumes them. To keep sessions alive, keep a WSL terminal open or
raise `vmIdleTimeout` in `%USERPROFILE%\.wslconfig`. Systemd-enabled distributions also wipe
`/tmp` at boot, which is where `cs` writes its log.

### Modifications in this fork

Changes made in October 2026 on top of upstream v1.0.20 (AGPL-3.0 §5 notice):

- A sidebar layout, now the default: the list in a tmux pane beside the real session, with
  Alt+arrows to move between them, `z` zoom, and diff/shell/help paged in the session pane.
  Upstream's single-window layout remains as `cs --classic`.
- Sessions are grouped by project (`projects` / `projects_root`), ordered by status then last
  activity, with foldable headers, repository name and a last-activity age on every row.
- A `Waiting` status (permission prompt, question, unread finished turn) detected from Claude
  Code hook events first and from the screen as a fallback; `!` markers, per-group and global
  waiting counts, `w` to jump, bell and window-title notifications.
- Filters: `a` (active only) and `/` (type-to-filter). `z` hides the list pane.
- Adoption of agent sessions running in tmux sessions `cs` did not create (watch and open only).
- Past Claude Code sessions listed from `~/.claude/projects` and resumed with `claude --resume`.
- In-place sessions without a worktree; a repository picker when creating a session; `cs` can
  start outside a git repository once projects are configured.
- Config keys `max_instances`, `worktree_dir`, `worktree_next_to_repo`, `default_mode`,
  `disable_hooks`, `notifications`, `disable_adopt`, `adopt_programs`.
- Fixes carried: prompt detection compared the full program path to `claude`; the folder-trust
  auto-dismiss used an outdated wording. Shift+J/K manual reordering was removed because the
  order is now derived.

Nothing in the fork is specific to one person or company: anything personal lives in
`~/.claude-squad/config.json`.

#### Keeping up with upstream

```bash
git remote add upstream https://github.com/smtg-ai/claude-squad.git   # once
git fetch upstream
git rebase upstream/main      # or: git merge upstream/main
go build -o ~/.local/bin/cs . && go test ./...
```

The fork's commits are small and self-contained (one feature each) to keep rebases manageable.

### FAQs

#### Failed to start new session

If you get an error like `failed to start new session: timed out waiting for tmux session`, update the
underlying program (ex. `claude`) to the latest version.

### How It Works

1. **tmux** to create isolated terminal sessions for each agent
2. **git worktrees** to isolate codebases so each session works on its own branch
3. A simple TUI interface for easy navigation and management

### License

[AGPL-3.0](LICENSE.md)

### Star History

[![Star History Chart](https://api.star-history.com/svg?repos=smtg-ai/claude-squad&type=Date)](https://www.star-history.com/#smtg-ai/claude-squad&Date)
