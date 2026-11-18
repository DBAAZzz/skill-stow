# Skill Stow

English · [中文](README.zh-CN.md)

**One Mac app for the skills of every AI agent you use.** See which agent reads which skill, turn skills on or off per agent, add and update them with a security check first, and clean up the copies and broken links that pile up across `~/.claude`, `~/.codex`, `~/.cursor` and the rest.

Free and open source (MIT). macOS 12 or later, Apple Silicon and Intel. No account, no telemetry. [Download the latest version](https://github.com/DBAAZzz/skill-stow/releases/latest), then see [Install](#install) for the first launch.

![Skill Stow: the My Skills library with the inspector open](docs/screenshots/en/main.png)

## What it does

### See every skill in one place

Skill Stow scans the skill folders of 32 agents plus your projects, and shows which agents actually read each skill: from the shared library (`~/.agents/skills`), from an agent's own folder, or from inside a project. Browse as cards or as a board with one column per agent. Skills installed together, with `npx skills` or an agent plugin, are grouped into collections.

Projects are found automatically from Claude Code, Codex and VS Code–based editor history, and you can add any folder by hand.

![Board view: one column per agent](docs/screenshots/en/board.png)

### Turn a skill on or off per agent

Flip the switch for Claude Code, Codex, Gemini CLI, OpenCode or ZCode, and the app edits that agent's own config file, so the agent sees exactly the state the app shows. Add to Another Agent… links a skill into any other agent's folder, and Remove deletes only links the app created.

### Add skills from anywhere, checked before they land

Paste a GitHub link, `owner/repo`, an `npx skills add …` command or a website URL, or drop a folder or `.zip` onto the window. Pick which skills to add and where they go: the shared library, specific agents, or a project.

Every skill gets the local security check before it's added, and for GitHub sources the skills.sh security audit is shown next to it. A skill that is high risk or couldn't be fully checked is only added if you choose Add Anyway.

![Add Skills: pick skills from a folder, with their check results](docs/screenshots/en/add.png)

### Local security check

An offline analyzer reads every file of every skill, including SKILL.md, scripts and references, and flags things like:

- scripts that download and run remote code, open a reverse shell, or decode and run a payload
- reading SSH keys or credential files, or sending secrets to an external address
- instructions telling the agent to ignore earlier instructions, hide what it does, skip confirmations, or send the conversation elsewhere
- invisible or direction-changing characters, and instructions hidden in comments
- real-looking keys inside files, large-scale deletion, changes to login items or shell configuration

Each finding links to the exact line. Mark it Reviewed once you've looked; if the skill's content changes, the finding comes back. The first launch checks everything in the background, and a skill whose files change is marked Outdated and checked again. It's a heuristic check, not a guarantee of safety.

![Security check: a high-risk finding with the exact lines](docs/screenshots/en/security.png)

### Update without surprises

Skills added from GitHub remember the commit they came from. Skill Updates lists the ones with newer upstream commits, and View Differences shows a file-by-file diff before anything changes.

- A skill you edited locally isn't overwritten: it waits for you to compare and confirm.
- A new version that is high risk or couldn't be fully checked isn't applied unless you choose Update Anyway.
- The old version goes to the Trash by default, so you can put it back.
- Update checks only run when you click Check for Skill Updates. Proxy settings and your local `gh` login are supported for restricted networks and higher rate limits.

Skills installed into a project aren't included in updates yet.

![Skill Updates: ready, needs confirmation, needs attention](docs/screenshots/en/updates.png)

### Tidy Up

One list of everything that needs attention, and most fixes take one click:

- identical copies of a skill, merged into one
- copies whose content drifted apart, compared side by side so you can keep the right one
- duplicate links and broken links
- skills whose SKILL.md points to files that don't exist
- install records left behind by skills you deleted
- global links that point into a project, which break if the project moves

<p>
  <img src="docs/screenshots/en/tidy.png" width="49%" alt="Tidy Up: every issue in one list">
  <img src="docs/screenshots/en/compare.png" width="49%" alt="Comparing two copies of a skill side by side">
</p>

### Usage

Counts how often each skill was called, based on Claude Code and Codex session logs on your Mac, so you can see what you actually use and clear out what you don't. Other agents don't keep call records.

![Usage: calls over the last 8 weeks and per skill](docs/screenshots/en/usage.png)

### Feels like a Mac app

- ⌘K from any page searches skill names, descriptions and full text, and jumps to Tidy Up, Skill Updates or Settings. In the Chinese interface, pinyin works too.
- The window follows the disk: install a skill from the terminal and it shows up without a refresh.
- Deleting moves skills to the Trash, with Undo (⌘Z) right in the notification.
- Back and forward history (⌘[ and ⌘]), drag and drop, light and dark appearance, English and Simplified Chinese.

![⌘K search across names, descriptions and full text](docs/screenshots/en/search.png)

## Install

1. Download `skill-stow-<version>.dmg` from [Releases](https://github.com/DBAAZzz/skill-stow/releases/latest), open it and drag Skill Stow into Applications.
2. The first time you open it, macOS says it can't verify the developer. This is expected: the app isn't signed with an Apple Developer ID yet.
   - macOS 15 and later: open the app once and click Done. Then go to System Settings › Privacy & Security, scroll to Security, click Open Anyway next to Skill Stow and confirm with your password.
   - macOS 12 to 14: in Applications, Control-click Skill Stow, choose Open, then click Open.

   After that it opens normally. Each new version needs to be allowed once more.

   Removing the download flag in Terminal does the same:

   ```sh
   xattr -dr com.apple.quarantine "/Applications/Skill Stow.app"
   ```

If you'd rather not run a downloaded binary, build it yourself as described in [Running from source](#running-from-source).

## Supported agents

Skill Stow scans the skill folders of 32 agents, including Claude Code, Codex, Cursor, Gemini CLI, GitHub Copilot, OpenCode, Windsurf, Cline, Kiro, Trae, Qwen Code, Qoder, CodeBuddy and ZCode. See [`app/internal/library/agents.go`](app/internal/library/agents.go) for the full list. If yours isn't detected, add its skill folder in Settings › Agents.

Turning a single skill on or off depends on the agent's own config. Five agents support it today:

| Agent | Config written |
|---|---|
| Claude Code | `~/.claude/settings.json` |
| Codex | `~/.codex/config.toml` (only adds or removes blocks tagged by this app) |
| Gemini CLI | `~/.gemini/settings.json` |
| OpenCode | `~/.config/opencode/opencode.jsonc` or `opencode.json` |
| ZCode | `~/.zcode/cli/config.json` |

Skills in other agents show as Always On. If the app can't parse a config file, the skill's state shows as Unknown and the app doesn't write to that file.

## Files the app changes on your machine

When the app runs on your real data, it writes to your machine:

- Enabling or disabling a skill edits the agent's config file listed above. Only the skill-related entries change, and everything else is left as it was.
- Add to Another Agent… creates a link in the chosen agent's skill folder. Remove deletes only links this app created.
- Adding, updating and deleting skills, and the fixes in Tidy Up, change your skill folders. Deleted skills and merged copies go to the Trash, and so do old versions after an update unless you set them to be deleted immediately.

The app keeps its own settings, records and an event log (`logs/`, capped at about 1 MB) in `~/Library/Application Support/Skill Stow/`. The log records what the app changed and what failed; it never contains skill contents, config contents or tokens. To report a problem, use Help › Report an Issue…, which shows the diagnostic info before copying it.

## Privacy and network

There is no telemetry, analytics or account. Usage stats are read from session logs on your Mac and never leave it, and the security check runs offline.

The app only goes online for these:

- **Adding a skill from a link**: reads the skill from GitHub (`api.github.com`, `raw.githubusercontent.com`, `codeload.github.com`), from skills.sh (file snapshot and security audit), or from the website you entered.
- **Checking for or installing skill updates**: asks GitHub for the latest commit. This only happens when you click Check for Skill Updates.
- **Source icons**: shows the GitHub owner's avatar or the website's icon next to skills you installed from them. Each icon is fetched once and cached in the app data folder.

If you turn on Use Local gh Login in the Skill Updates settings, your `gh` token is sent only to GitHub, to raise the API rate limit. Diagnostic info is copied only when you choose Help › Report an Issue…, after you've seen what it contains.

## Running from source

Requires Go 1.27+ (see [`app/go.mod`](app/go.mod) for the exact version). MyGo is installed with the module as a Go tool.

```sh
cd app
SKILLS_SAMPLE=1 go tool mygo dev  # sample data; never touches your real folders
go tool mygo dev                  # your real data; reloads on code changes
go tool mygo build -platform darwin/universal  # Intel and Apple Silicon; output in app/build/darwin-universal/
```

## Contributing

[AGENTS.md](AGENTS.md) covers development conventions (UI components, colors, UI strings, commit messages) and test commands.

## License

[MIT](LICENSE)
