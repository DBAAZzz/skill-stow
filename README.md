# Skill Stow

English · [中文](README.zh-CN.md)

A macOS desktop app for managing the skills of every AI agent on your machine from one window. It shows which agent reads which skill and lets you turn skills on or off per agent, add new skills, update them, and clean up duplicates and leftover links.

- **Library**: your own skills and the skills that ship with agents, listed separately. Your library lives in `~/.agents/skills`.
- **Skill Scopes / Projects**: shows which skills each agent actually reads, globally and in each project.
- **Per-agent enable / disable**: edits the agent's own config file, so the agent sees the same state the app shows (see the table below for supported agents).
- **Add skills**: paste a GitHub link, `owner/repo`, an `npx skills add …` command, or a website URL, or import a local folder.
- **Skill Updates**: skills added from GitHub record their commit and are listed here when upstream has a newer version.
- **Usage**: counts how often each skill is invoked, based on Claude Code and Codex session logs.
- **Tidy Up**: finds duplicate copies, redundant links, and broken links, and fixes them for you.

## Supported agents

Scans the skill folders of 32 agents, including Claude Code, Codex, Cursor, Gemini CLI, GitHub Copilot, OpenCode, Windsurf, Cline, Kiro, Trae, Qwen Code, Qoder, CodeBuddy, and ZCode. See [`app/internal/library/agents.go`](app/internal/library/agents.go) for the full list.

Turning a single skill on or off depends on support in the agent's own config. Five agents are supported today:

| Agent | Config written |
|---|---|
| Claude Code | `~/.claude/settings.json` |
| Codex | `~/.codex/config.toml` (only adds or removes blocks tagged by this app) |
| Gemini CLI | `~/.gemini/settings.json` |
| OpenCode | `~/.config/opencode/opencode.jsonc` or `opencode.json` |
| ZCode | `~/.zcode/cli/config.json` |

Skills in other agents show as "Always On". If the app can't parse a config file, the skill's state shows as "Unknown" and the app doesn't write to that file.

## Running

Requires Go 1.27+ (see [`app/go.mod`](app/go.mod) for the exact version). MyGo is installed with the module as a Go tool.

```sh
cd app
SKILLS_SAMPLE=1 go tool mygo dev  # sample data; never touches your real folders
go tool mygo dev                  # your real data; reloads on code changes
go tool mygo build -platform darwin/arm64  # Apple Silicon; output in app/build/darwin-arm64/
```

To support both Intel and Apple Silicon Macs, run `go tool mygo build -platform darwin/universal`. Output goes to `app/build/darwin-universal/`.

## Files the app changes on your machine

When the app runs on your real data, it writes to your machine:

- Enabling or disabling a skill edits the agent's config file listed above. Only the skill-related entries change, and everything else is left as it was.
- "Add to …" creates a link in the chosen agent's skill folder. "Remove" deletes only links this app created.
- Deleting skills and fixing links in "Tidy Up" changes your skill folders.

The app keeps its own settings, records and an event log (`logs/`, capped at about 1 MB) in `~/Library/Application Support/Skill Stow/`. The log records what the app changed and what failed; it never contains skill contents, config contents or tokens. To report a problem, use Help › Report an Issue…, which shows the diagnostic info before copying it. To keep your machine untouched, run with `SKILLS_SAMPLE=1`.

## Contributing

[AGENTS.md](AGENTS.md) covers development conventions (UI components, colors, UI strings, commit messages) and test commands.

## License

[MIT](LICENSE)
