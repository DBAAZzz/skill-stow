# Skill Stow

[English](README.md) · 中文

**一个 Mac 应用，管好你用的所有 AI Agent 的 Skill。** 哪个 Agent 读得到哪个 Skill 一目了然；可以按 Agent 单独开关，添加和更新前先过一遍安全检查；散落在 `~/.claude`、`~/.codex`、`~/.cursor` 等目录里的重复副本和断掉的链接，也能一次清理干净。

免费开源（MIT）。支持 macOS 12 及以上，Apple Silicon 和 Intel 都能用。不用注册账号，没有遥测。[下载最新版](https://github.com/DBAAZzz/skill-stow/releases/latest)，第一次打开的步骤见[安装](#安装)。

![Skill Stow：「我的 Skill」与右侧详情栏](docs/screenshots/zh/main.png)

## 能做什么

### 所有 Skill，一处看全

扫描 32 个 Agent 的 Skill 目录和你的项目，标出每个 Skill 到底被哪些 Agent 读到：来自通用 Skill 库（`~/.agents/skills`）、某个 Agent 自己的目录，还是项目里。可以用卡片浏览，也可以切到看板，一个 Agent 一列。用 `npx skills` 或 Agent 插件一起装的 Skill 会归成一个集合。

项目会从 Claude Code、Codex 和 VS Code 系编辑器的使用记录里自动找出来，也可以手动添加任意文件夹。

![看板：一个 Agent 一列](docs/screenshots/zh/board.png)

### 按 Agent 开关单个 Skill

在 Claude Code、Codex、Gemini CLI、OpenCode 或 ZCode 上拨一下开关，应用会直接改这个 Agent 自己的配置文件，Agent 那边看到的状态和这里完全一致。「添加到其他 Agent…」会把 Skill 链接进其他 Agent 的目录；「移除」只删本应用建的链接。

### 从哪都能加，加之前先检查

粘贴 GitHub 链接、`owner/repo`、`npx skills add …` 命令或网站地址，或者直接把文件夹、`.zip` 拖进窗口。选好要加哪些 Skill、放到哪里：通用 Skill 库、指定的 Agent，或者某个项目。

每个 Skill 加进来之前都会先做本地安全检查；GitHub 来源还会同时列出 skills.sh 的安全审计。高风险或没能检查完的 Skill，要你点「仍然添加」才会加进来。

![添加 Skill：从文件夹里挑 Skill，附检查结果](docs/screenshots/zh/add.png)

### 本地安全检查

离线分析器会读每个 Skill 的所有文件，包括 SKILL.md、脚本和参考资料，标出这类问题：

- 脚本下载远程代码直接运行、反弹 shell、解码后执行
- 读取 SSH 私钥或凭据文件，或把密钥发到外部地址
- 让 Agent 忽略之前的指令、瞒着你做事、跳过确认，或把对话内容发出去
- 看不见的字符、会改变显示顺序的字符，以及藏在注释里的指令
- 文件里像是真的密钥、大范围删除文件、改登录项或 shell 配置

每条结果都能直接跳到对应的那一行。看过之后勾上「已看过」；Skill 内容一变，它会重新出现。首次打开会在后台检查全部 Skill，之后哪个 Skill 的文件变了，就标成「已过期」并重新检查。这是基于规则的检查，不能保证绝对安全。

![安全检查：高风险结果和对应的代码行](docs/screenshots/zh/security.png)

### 更新不踩坑

从 GitHub 添加的 Skill 会记下来源 commit。「更新 Skill」列出上游有新 commit 的 Skill，「查看差异」会在改动之前逐个文件展示差异。

- 你在本地改过的 Skill 不会被直接覆盖，会等你对比确认。
- 新版本有高风险或没能检查完时，要你点「仍然更新」才会更新。
- 旧版本默认移到废纸篓，随时能放回原处。
- 只在你点「检查 Skill 更新」时才联网检查。网络受限时可以设代理，也可以用本机的 `gh` 登录提高 API 限额。

装在项目里的 Skill 暂时不参与更新。

![更新 Skill：可更新、需确认、需处理](docs/screenshots/zh/updates.png)

### 整理

需要处理的问题都列在一处，大多点一下就能解决：

- 内容相同的副本，收成一份
- 内容已经不一样的副本，左右对比后留下想要的那份
- 重复的链接和断掉的链接
- SKILL.md 里引用了不存在的文件
- Skill 删了、安装记录还留着
- 全局链接指向项目里的 Skill，项目一挪就断

<p>
  <img src="docs/screenshots/zh/tidy.png" width="49%" alt="整理：所有问题列在一处">
  <img src="docs/screenshots/zh/compare.png" width="49%" alt="左右对比同一 Skill 的两份副本">
</p>

### 使用情况

根据本机 Claude Code 和 Codex 的会话记录，统计每个 Skill 被调用了多少次：哪些常用、哪些从没用过，一眼就能看出来，方便清理。其他 Agent 没有留下调用记录。

![使用情况：近 8 周和每个 Skill 的调用](docs/screenshots/zh/usage.png)

### 用起来像原生 Mac 应用

- 在任何页面按 ⌘K，都能搜 Skill 的名称、简介和正文，也能直接跳到整理、更新 Skill 或设置。中文界面下打拼音也能搜到。
- 界面跟着磁盘走：在终端里装了 Skill，不用刷新就会出现。
- 删除的 Skill 进废纸篓，提示条上就有「撤销」（⌘Z）。
- 支持前进后退（⌘[ 和 ⌘]）、拖放、浅色和深色外观，界面有简体中文和英文。

![⌘K 搜索名称、简介和正文](docs/screenshots/zh/search.png)

## 安装

1. 从 [Releases](https://github.com/DBAAZzz/skill-stow/releases/latest) 下载 `skill-stow-<版本>.dmg`，打开后把 Skill Stow 拖进「应用程序」。
2. 第一次打开时，macOS 会提示无法验证开发者。应用还没有用 Apple 开发者证书签名，这个提示是预期的：
   - macOS 15 及以后：先双击打开一次，在提示里点「完成」；然后打开「系统设置 › 隐私与安全性」，滚到「安全性」，点 Skill Stow 旁边的「仍要打开」，输入密码确认。
   - macOS 12 到 14：在「应用程序」里按住 Control 点 Skill Stow，选「打开」，再点「打开」。

   放行一次之后正常双击即可。手动下载安装的新版本要再放行一次。

   也可以在终端里去掉下载标记，效果相同：

   ```sh
   xattr -dr com.apple.quarantine "/Applications/Skill Stow.app"
   ```

0.1.1 之后的版本可以自己更新：选「Skill Stow › 检查应用更新…」，或在「设置 › 通用 › 应用更新」里点「检查更新…」。更新窗口会显示更新说明，下载新版本（能只下载变化的部分时只下载那部分），校验签名后重新启动应用。在那里打开「自动检查更新」，就会在每次启动和之后每天检查一次。

不想运行下载的程序，可以按下面「[从源码运行](#从源码运行)」自己构建。

## 支持的 Agent

能扫描 32 个 Agent 的 Skill 目录，包括 Claude Code、Codex、Cursor、Gemini CLI、GitHub Copilot、OpenCode、Windsurf、Cline、Kiro、Trae、Qwen Code、Qoder、CodeBuddy、ZCode 等，完整列表见 [`app/internal/library/agents.go`](app/internal/library/agents.go)。没认出你用的 Agent，可以在「设置 › Agent 管理」里手动添加它的 Skill 目录。

单个 Skill 的启用、停用要靠 Agent 自己的配置支持，目前有 5 个：

| Agent | 写入的配置 |
|---|---|
| Claude Code | `~/.claude/settings.json` |
| Codex | `~/.codex/config.toml`，只增删带本应用标记的块 |
| Gemini CLI | `~/.gemini/settings.json` |
| OpenCode | `~/.config/opencode/opencode.jsonc` 或 `opencode.json` |
| ZCode | `~/.zcode/cli/config.json` |

其他 Agent 里的 Skill 显示为「始终启用」。配置格式认不出来时状态显示为「未知」，不会写入。

## 会改动本机哪些文件

用真实数据运行时，应用会写本机文件：

- 启用、停用会修改上表中的 Agent 配置，只改 Skill 相关的部分，其余内容保持原样。
- 「添加到其他 Agent…」会在 Agent 的 Skill 目录里建链接；「移除」只删本应用建的链接。
- 添加、更新、删除 Skill，以及「整理」里的处理，都会改动 Skill 目录。删掉的 Skill 和收成一份时多出来的副本会进废纸篓；更新后的旧版本也一样，除非你设成了直接删除。

应用自己的设置、记录和事件日志（`logs/`，上限约 1 MB）放在 `~/Library/Application Support/Skill Stow/`。日志只记应用改了什么、哪里失败了，不含 Skill 内容、配置原文和 token。遇到问题时用「帮助 › 反馈问题…」，诊断信息会先给你预览再复制。

## 隐私与联网

没有遥测、统计上报，也不需要账号。使用情况是从本机的会话记录里统计的，不会传出去；安全检查在本机离线完成。

只有下面这些情况会联网：

- **从链接添加 Skill**：从 GitHub（`api.github.com`、`raw.githubusercontent.com`、`codeload.github.com`）、skills.sh（文件快照和安全审计）或你填的网站读取 Skill。
- **检查或安装 Skill 更新**：向 GitHub 查询最新的 commit。只在你点「检查 Skill 更新」时进行。
- **检查或安装应用更新**：读取 GitHub 上 Skill Stow 最新的 Release 并下载新版本。只在你点「检查应用更新…」时进行；在「设置 › 通用」里打开「自动检查更新」后，每次启动和之后每天检查一次（检查失败时一小时后再试）。
- **来源图标**：在从 GitHub 或网站装的 Skill 旁边显示所有者头像或网站图标。每个图标只抓一次，缓存在应用数据目录里。

在「Skill 更新设置」里打开「使用本机 gh 登录状态」后，`gh` 的 token 只发给 GitHub，用来提高 API 限额。诊断信息只在你用「帮助 › 反馈问题…」并预览过之后才会复制。

## 从源码运行

需要 Go 1.27+（版本以 [`app/go.mod`](app/go.mod) 为准），MyGo 作为 Go tool 随模块安装。

```sh
cd app
SKILLS_SAMPLE=1 go tool mygo dev  # 用示例数据启动，不读写本机目录
go tool mygo dev                  # 读本机真实数据，改代码自动重载
go tool mygo build -platform darwin/universal  # 同时支持 Intel 和 Apple Silicon；产物在 app/build/darwin-universal/
```

## 参与开发

开发约定（界面组件、颜色、界面文案、提交规范）和测试命令见 [AGENTS.md](AGENTS.md)。

## 许可证

[MIT](LICENSE)
