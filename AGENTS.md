# 项目约定

应用在 `app/`，Go + MyGo 原生 UI。下面的界面规则都有测试守着，违反了 `go test ./...` 会直接失败，失败信息会指出文件和行号。

## 开发与测试

在 `app/` 下执行：

```sh
SKILLS_SAMPLE=1 go tool mygo dev  # 示例数据，不碰本机目录
go tool mygo dev                  # 本机真实数据（会写 Agent 配置，见 README）
go test ./...                     # 无窗口；扫描用临时目录造的假 home
go tool mygo build                # 打包 .app / .dmg 到 build/

# 各页 2x 截图；加 SNAPSHOT_DARK=1 出深色版，加 SNAPSHOT_REAL=1 用本机真实数据（只读）
mkdir -p /tmp/shots && SNAPSHOT_DIR=/tmp/shots go test -run Snapshots .
```

## 代码分层

- 界面代码（`app/*.go`）只通过 `app/internal/library` 读写磁盘，不直接操作文件。
- Agent 目录表在 `internal/library/agents.go`；各 Agent 原生配置的读写在 `internal/library/native*.go`。

## 菜单与弹出层

下拉框、弹出菜单、右键菜单一律用 `app/menu.go` 里的 `iconActionMenu`、`selectButton`、`triggerMenu`、`contextMenu`，菜单项通过 `frostedMenu` 添加。

- 任何文件都不要调 `.Menu()`、`.ContextMenu()`、`.PopoverMenu()`、`.PopoverMenuButton()`。
- `ui.MenuBase`、`ui.ContextMenuBase`、`ui.PopoverBase` 只能在 `menu.go` 里用。

由 `TestArchitectureEnforcesUnifiedFrostedMenuSystem` 检查。

## 颜色（浅色 / 深色）

- 颜色一律用 `app/colors.go` 里按用途起的名字（`colBg*`、`colText*`、`colBorder*`、`colInk*` 等），先找已有的。
- 确实要新颜色：在 `colors.go` 的 var 里声明名字，再在 `tones` 里加一行，同时写浅色、深色两个值。总数上限 75 个，浅色、深色都相同的两个名字要合并。
- 悬停、按下用 `tintSolid` / `tintInk` 或 `colInkHover.Over(底色)`；投影用 `shadow(a)`；卡片底用 `colBgCard`。
- `ui.Hex` / `ui.RGB` / `ui.RGBA` 只能写在 `colors.go`（Agent 品牌色在 `agents.go`，例外）。
- 不要用色板颜色给包级变量赋初值，那样只会存下启动时的浅色，切到深色不会变。用的时候再取。

由 `colors_test.go` 里的 `TestColorsLiveInColorsFile`、`TestEveryColorHasDarkValue`、`TestPaletteStaysSmall`、`TestNoPaletteColorCapturedAtInit` 检查。

## 界面文案（i18n）

- 界面上的字同时写进 `app/locales/zh-Hans.json` 和 `app/locales/en.json`，在 `app/` 下执行 `go tool mygo generate`，代码里调用生成的函数，比如 `locales.ActionDetectAgain()`。
- Go 代码里不写中文字面量，不拼句子，也不拿界面文字做比较。示例数据 `internal/library/sample.go` 不受这条限制。
- 批量搬运旧文案用 `app/tools/i18nextract`（先 `scan` 出计划，改完再 `apply`），用法见 [它的 README](app/tools/i18nextract/README.md)。

由 `TestUIStringsLiveInTheCatalogs`、`TestEveryMessageHasAnEnglishTranslation` 检查。

## 提交规范

提交信息遵循 [Conventional Commits](https://www.conventionalcommits.org/zh-hans/)：`<type>(<scope>): <subject>`。一个提交只做一件事，按功能拆分。

- `type`：`feat` 新功能、`fix` 修复、`docs` 文档、`style` 格式、`refactor` 重构、`perf` 性能、`test` 测试、`build` 构建、`ci` 持续集成、`chore` 杂项、`revert` 回滚。
- `scope` 按功能区写：`import`、`usage`、`browse`、`batch`、`delete`、`update`、`menu`、`library` 等。
- `commit-msg` 钩子里由 commitlint（`commitlint.config.mjs`）校验，钩子由 husky 管理。克隆后在仓库根目录执行一次 `npm install` 就会启用。
