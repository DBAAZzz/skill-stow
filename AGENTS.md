# 项目约定

`CLAUDE.md` 是指向本文件的软链，约定只改 `AGENTS.md`。

应用在 `app/`，Go + MyGo 原生 UI。下面的界面规则都有测试守着，违反了 `go test ./...` 会直接失败，失败信息会指出文件和行号。

## 开发与测试

在 `app/` 下执行：

```sh
SKILLS_SAMPLE=1 go tool mygo dev  # 示例数据，不碰本机目录
go tool mygo dev                  # 本机真实数据（会写 Agent 配置，见 README）
go test ./...                     # 无窗口；扫描用临时目录造的假 home
go tool mygo build                # 打包 .app / .dmg 到 build/

# 各页 2x 截图；加 SNAPSHOT_DARK=1 出深色版，加 SNAPSHOT_REAL=1 用本机真实数据（只读），
# 加 SNAPSHOT_LANG=en 出英文版（按中文找按钮的几张会失败，属预期）
mkdir -p /tmp/shots && SNAPSHOT_DIR=/tmp/shots go test -run Snapshots .
```

README 的截图在 `docs/screenshots/zh/`、`docs/screenshots/en/`，每个功能小节一张，由上面的截图命令加 `-run 'TestSnapshots$|TestUsageSnapshots|TestImportSnapshots|TestUpdate.*Snapshot'` 生成：main=`2-cards-selected`、board=`4-board`、add=`import-pick`、security=`s5-sec-report`、updates=`updates-full`、tidy=`t1-tidy`、compare=`d1-compare`、usage=`u1-usage`（取上面 1600 像素）、search=`k2-palette-test`，都缩到 1760 像素宽。界面有明显变化时重新生成。

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

## 不用点号分隔

界面上不用 `·`、`•`、`‧`、`⋅`、`・` 把几段信息拼成一行（如 `今天 · 3 个文件`、`高风险 1 · 警告 1`）。

- 并排的几段短信息用 `metaLine`（段与段之间只留间距），或各自一个控件；状态用 Tag；附注用括号或写成完整句子。
- 只能交出一个字符串的地方（Tooltip、菜单项、读屏标签、提示）把整句写进 locales；句子里的列举用 `locales.ListSeparator()`（中文是“、”）。
- 列表的项目符号也不用点号，用 `-` 或编号。

由 `separators_test.go` 里的 `TestUITextDoesNotJoinInformationWithDots` 检查。确实不是界面文字时，在同一行加 `//separator:ignore` 并写明原因。

## 设计稿与界面示意

上面的菜单、颜色、文案、点号规则同样适用于设计稿、画板（`.ryugi/artifacts/`）、需求文档和回复里的界面示例。测试只检查代码，设计阶段的偏差要到实现时才暴露，所以画之前就要守住。

- 先读要改的界面的现有代码，按实际字号、尺寸、圆角和 `colors.go` 里的颜色画，不凭截图估。
- 有现成组件就照它画，不另造样式：卡片底栏标记 `flag`、标签 `tag`（添加技能页每项只显示一个）、分组 `insetGroup` 加 `sectionLabel`、并列信息 `metaLine`、整理页图标 `issueIcon`、详情页状态栏 `strip`。
- 新增内容放进已有位置前，先看那里已经显示什么（例如添加技能页已有 skills.sh 的“安全审计”），避免重复或名字相近。
- 交付前和 `SNAPSHOT_DIR` 截出的现有页面并排对照一遍。

## 发版

应用通过 MyGo 的 updater 自更新（`app/app_update.go`）：`app/mygo.json` 的 `updates` 里是签名公钥和发布仓库，已安装的应用只读 GitHub 上最新 Release 里的 `update-darwin-universal.json`，只接受用对应私钥签名的包。私钥 `mygo-update.key` 在 `~/Library/Application Support/mygo/update-keys/` 和仓库 secret `MYGO_UPDATER_PRIVATE_KEY` 里，不进仓库，另存一份到密码管理器；丢了它，已安装的应用就再也收不到更新。

1. `main` 合入新的 `feat` / `fix` / `perf` / `revert` 提交后，`.github/workflows/release-pr.yml` 会自动维护一个 `build(release): prepare v<版本>` 的发版 PR（分支 `release/prepare`）：按 Conventional Commits 推算下一版本号，更新 `app/mygo.json`，并在 `app/CHANGELOG.md` 顶部生成 `## <版本>` 双语草稿。
2. 准备发版时，在发版 PR 里润色 `app/CHANGELOG.md` 的 `中文` / `English` 更新说明（签名构建要求有这一节，内容就是应用内更新窗口里的「更新说明」；用编号列表，不用 `-`，因为更新窗口会把无序列表画成 `•`）。一旦 PR 分支有人工提交，CI 不会再覆盖它。如果希望 GitHub Release 页面比应用内更新说明更详细，在同一个 PR 里加一个可选的 `docs/releases/v<版本>.md`（只写 `## 中文` 和 `## English` 两节变更正文，不用写安装说明和 SHA-256）。确认 CI 通过后合并该 PR。
3. 发版 PR 合并到 `main` 后，`.github/workflows/release-pr.yml` 检测到 `app/mygo.json` 的新版本尚未打标，会自动打 `v<版本>` 标签并调用 `.github/workflows/release.yml`（也可以直接手工改版本后推 `v<版本>` 标签触发）依次完成：
   - 核对标签、`version` 和发布说明格式（`app/tools/releasenotes`）并跑完测试；
   - 执行 `go tool mygo build -platform darwin/universal -upload`（私钥在仓库 secret `MYGO_UPDATER_PRIVATE_KEY`），建草稿 Release `v<版本>`，上传 `skill-stow-<版本>.dmg`、更新包 `.tar.gz`、`update-darwin-universal.json` 和 `SHA256SUMS`，能做时再加相对前几个版本的增量包 `.delta`（增量包按条件生成：没有可比的旧包、旧包下载失败、签名不符或补丁不比全包小时会跳过，构建照样成功）；
   - 用 `.github/release-notes.tmpl.md` 拼上固定安装/放行说明和 dmg 的 SHA-256，自动更新草稿标题为 `Skill Stow <版本>` 并写入中英文说明（有 `docs/releases/v<版本>.md` 时优先用它，否则用 `app/CHANGELOG.md`）；
   - 自动核对 `update-darwin-universal.json` 里引用的全包和增量包都已上传到该草稿 Release。
4. CI 完成后，去 GitHub 草稿页面预览确认，直接点 **Publish release**（或执行 `gh release edit v<版本> --draft=false`）；发布后已安装的应用才能检查到它，发出去就撤不回，所以不让 CI 自动发布。

发版前想本地预览渲染后的 Release 说明，可在 `app/` 下执行 `go run ./tools/releasenotes render -version <版本> -override ../docs/releases/v<版本>.md -dmg-name skill-stow-<版本>.dmg -sha256 <预览占位>`。CI 不可用时在本机构建：在 `app/` 下执行 `MYGO_UPDATER_PRIVATE_KEY="$(cat ~/Library/Application\ Support/mygo/update-keys/mygo-update.key)" go tool mygo build -platform darwin/universal -upload`，再运行 `app/tools/releasenotes` 更新草稿说明并核对附件。

## 提交规范

提交信息遵循 [Conventional Commits](https://www.conventionalcommits.org/zh-hans/)：`<type>(<scope>): <subject>`。一个提交只做一件事，按功能拆分。

- `type`：`feat` 新功能、`fix` 修复、`docs` 文档、`style` 格式、`refactor` 重构、`perf` 性能、`test` 测试、`build` 构建、`ci` 持续集成、`chore` 杂项、`revert` 回滚。
- `scope` 按功能区写：`import`、`usage`、`browse`、`batch`、`delete`、`update`、`menu`、`library` 等。
- `commit-msg` 钩子里由 commitlint（`commitlint.config.mjs`）校验，钩子由 husky 管理。克隆后在仓库根目录执行一次 `npm install` 就会启用。
