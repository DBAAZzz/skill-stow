# Agent 图标来源

来自 [lobe-icons](https://github.com/lobehub/lobe-icons)（MIT），按其 `getLobeIconCDN` 的地址规则下载：

```
https://unpkg.com/@lobehub/icons-static-svg@1.102.0/icons/<id>.svg        # mono，currentColor
https://unpkg.com/@lobehub/icons-static-svg@1.102.0/icons/<id>-color.svg  # 彩色
```

| Agent | lobe id | 文件 |
|---|---|---|
| Cursor | `cursor` | `cursor.svg`（没有彩色版） |
| CodeBuddy | `codebuddy` | `codebuddy.svg` `codebuddy-color.svg` |
| WorkBuddy | `workbuddy` | `workbuddy.svg` `workbuddy-color.svg` |
| Codex / Gemini CLI / Antigravity | `codex` `gemini` `antigravity` | `*-color.svg`（彩色，按原色画） |
| 其余 | `githubcopilot` `opencode` `windsurf` `kiro` `qwen` `trae` `goose` `roocode` `cline` `qoder` `openhands` `junie` `kilocode` `amp` `pi` `kimi` | 同名 `.svg` |

`claude.svg` 是 Claude 的星芒图形（lobe-icons 的 `claude`），代替 Claude Code 的像素小人，`fill` 改成了 `currentColor`。

`vscode-color.svg` 是微软官方的 VS Code 图标（[code.visualstudio.com/brand](https://code.visualstudio.com/brand)，2021-06-21 版的 `vscode.svg`），原样未改。界面的 SVG 渲染不支持 filter 和 mix-blend-mode，所以两处投影不画，顶部高光按普通混合画成 25% 白色渐变。设置 · 项目里的「VS Code 系编辑器」用它，白底、比例 0.7。

lobe-icons 没有的（Droid、iFlow、Crush、Augment、Continue、通义灵码）画成字母方块。

`zcode.svg` 是按 ZCode 的 256×256 应用图标描出来的：Z 由三块直边图形组成（上横、斜笔连右上、下横），边缘按像素抗锯齿拟合到 0.1px，斜边斜率约 0.706；和原图逐像素比较只有 8 个边缘像素差异明显。viewBox 保留原图的 256×256，所以 Z 在方块里的大小和原图标一致（`agents.go` 里比例为 1，底色 `#0b0c0f` 取原图上浅下深渐变的中间色）。

界面按 `@lobehub/icons@6.15.0` 各品牌 `Avatar` 的参数画成方块（`agents.go` 的 `brands`，全部参数以那里为准），例如：

| Agent | 底色 | 图标色 | 图标比例 |
|---|---|---|---|
| Claude Code | `#D97757` | `#fff` | 0.7 |
| Cursor | `#000` | `#fff` | 0.6 |
| CodeBuddy | 径向渐变 `#2EA99D → #6C4DFF`（中心 87.5% 102%） | `#fff` | 1 |
| WorkBuddy | 径向渐变 `#94d378 → #01C886`（同上） | `#fff` | 1 |
| Codex | 彩色图标自带白底方块 | — | 1 |
| Gemini CLI | `#fff` | 彩色 | 0.8 |

注意：CodeBuddy 与 WorkBuddy 的 mono 图形完全相同，只靠底色区分。
图标是各公司的商标，仅用于标识对应产品。
