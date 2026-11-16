# Skill Stow

[English](README.md) · 中文

Skill Stow 是一个 macOS 桌面应用，在一个窗口里管理本机所有 AI Agent 的技能（skill）：看谁读得到哪个技能、按 Agent 开关、添加、更新、清理。

- **技能库**：自己装的技能和 Agent 自带的技能分开列，技能库在 `~/.agents/skills`。
- **技能范围 / 项目**：按全局和项目查看，每个 Agent 实际会读到哪些技能。
- **按 Agent 启用、停用**：直接改 Agent 自己的配置文件，Agent 侧看到的状态和这里一致（支持范围见下表）。
- **添加技能**：可以粘贴 GitHub 链接、`owner/repo`、`npx skills add …` 命令或网站地址，也可以从本地目录导入。
- **技能更新**：从 GitHub 添加的技能会记下 commit，上游有新版本时列出来。
- **使用情况**：从 Claude Code、Codex 的会话记录里统计每个技能的调用。
- **整理**：找出重复副本、多余链接和断掉的链接，并直接处理。

## 支持的 Agent

能扫描 32 个 Agent 的技能目录，包括 Claude Code、Codex、Cursor、Gemini CLI、GitHub Copilot、OpenCode、Windsurf、Cline、Kiro、Trae、Qwen Code、Qoder、CodeBuddy、ZCode 等，完整列表见 [`app/internal/library/agents.go`](app/internal/library/agents.go)。

单个技能的启用、停用要靠 Agent 自己的配置支持，目前接了 5 个：

| Agent | 写入的配置 |
|---|---|
| Claude Code | `~/.claude/settings.json` |
| Codex | `~/.codex/config.toml`，只增删带本应用标记的块 |
| Gemini CLI | `~/.gemini/settings.json` |
| OpenCode | `~/.config/opencode/opencode.jsonc` 或 `opencode.json` |
| ZCode | `~/.zcode/cli/config.json` |

其他 Agent 显示为“始终启用”。配置格式认不出来时状态显示为“未知”，不会写入。

## 运行

需要 Go 1.27+（版本以 [`app/go.mod`](app/go.mod) 为准），MyGo 作为 Go tool 随模块安装。

```sh
cd app
SKILLS_SAMPLE=1 go tool mygo dev  # 用示例数据启动，不读写本机目录
go tool mygo dev                  # 读本机真实数据，改代码自动重载
go tool mygo build -platform darwin/arm64  # Apple Silicon；产物在 app/build/darwin-arm64/
```

需要同时支持 Intel 和 Apple Silicon Mac 时，执行 `go tool mygo build -platform darwin/universal`，产物在 `app/build/darwin-universal/`。

## 会改动本机哪些文件

用真实数据运行时，应用会写本机文件：

- 启用、停用会修改上表中的 Agent 配置，只改技能相关的部分，其余内容保持原样。
- 「添加到 Agent」会在 Agent 的技能目录里建链接；「移除」只删本应用建的链接。
- 「整理」里的删除、修复链接会改动技能目录。

应用自己的设置和记录放在 `~/Library/Application Support/Skill Stow/`。不想动本机，就用 `SKILLS_SAMPLE=1`。

## 参与开发

开发约定（界面组件、颜色、界面文案、提交规范）和测试命令见 [AGENTS.md](AGENTS.md)。

## 许可证

[MIT](LICENSE)
