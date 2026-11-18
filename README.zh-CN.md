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

## 安装

需要 macOS 12 或更新版本，Apple Silicon 和 Intel Mac 都能用。

1. 从 [Releases](https://github.com/DBAAZzz/skill-stow/releases/latest) 下载 `skill-stow-<版本>.dmg`，打开后把 Skill Stow 拖进「应用程序」。
2. 第一次打开时，macOS 会提示无法验证开发者。应用还没有用 Apple 开发者证书签名，这个提示是预期的：
   - macOS 15 及以后：先双击打开一次，在提示里点「完成」；然后打开「系统设置 › 隐私与安全性」，滚到「安全性」，点 Skill Stow 旁边的「仍要打开」，输入密码确认。
   - macOS 12 到 14：在「应用程序」里按住 Control 点 Skill Stow，选「打开」，再点「打开」。

   放行一次之后正常双击即可。每装一个新版本要再放行一次。

   也可以在终端里去掉下载标记，效果相同：

   ```sh
   xattr -dr com.apple.quarantine "/Applications/Skill Stow.app"
   ```

不想运行下载的程序，可以按下面「从源码运行」自己构建。

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

## 从源码运行

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

应用自己的设置、记录和事件日志（`logs/`，上限约 1 MB）放在 `~/Library/Application Support/Skill Stow/`。日志只记应用改了什么、哪里失败了，不含技能内容、配置原文和 token。遇到问题时用「帮助 › 反馈问题…」，诊断信息会先给你预览再复制。不想动本机，就用 `SKILLS_SAMPLE=1`。

## 隐私与联网

没有遥测、统计上报，也不需要账号。使用情况是从本机的会话记录里统计的，不会传出去；安全检查在本机离线完成。

只有下面这些情况会联网：

- **从链接添加技能**：从 GitHub（`api.github.com`、`raw.githubusercontent.com`、`codeload.github.com`）、skills.sh（文件快照和安全审计）或你填的网站读取技能。
- **检查或安装技能更新**：向 GitHub 查询最新的 commit。只在你点「检查技能更新」时进行。
- **来源图标**：在从 GitHub 或网站装的技能旁边显示所有者头像或网站图标。每个图标只抓一次，缓存在应用数据目录里。

在技能更新设置里打开「使用本机 gh 登录状态」后，`gh` 的 token 只发给 GitHub，用来提高 API 限额。诊断信息只在你用「帮助 › 反馈问题…」并预览过之后才会复制。

## 参与开发

开发约定（界面组件、颜色、界面文案、提交规范）和测试命令见 [AGENTS.md](AGENTS.md)。

## 许可证

[MIT](LICENSE)
