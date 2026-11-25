# Changelog

每个版本一节，标题是 `## <版本>`。这一节就是应用内更新窗口里的「更新说明」，先写中文、再写英文。用编号列表，不用 `-`：更新窗口会把无序列表画成圆点。

## 0.7.0

中文

1. 打开应用时会回到上次停留的页面，不再每次都从同一页开始
2. 技能市场里的技能可以直接拖到其他应用或复制链接，右键还能在 GitHub 或网页上打开
3. 技能市场的搜索框可以看到最近搜过的 10 次搜索，点一下就能再搜
4. 在技能市场按 Esc 会先关闭右侧的详情，再按一次才清空搜索

English

1. The app reopens on the page you were on last time instead of always starting on the same one
2. Market skills can be dragged into other apps or copied as a link, and the right-click menu opens them on GitHub or the web
3. The Skill Market search box lists your last 10 searches, so you can run one again with a click
4. Pressing Esc in the Skill Market now closes the detail pane first, and clears the search on the next press

## 0.6.0

中文

1. 可以看到 Codex 插件里自带的 Skill 了
2. 可以直接在应用里卸载 Claude Code、WorkBuddy 和 Codex 安装的插件
3. Codex 自带的 Skill 删不掉，但可以在详情里停用
4. 卸载插件时不再被后台扫描打断，完成后列表自动刷新
5. WorkBuddy 预装的插件按内置处理，不再提供卸载

English

1. You can now see the skills that come with Codex plugins in your library.
2. You can now uninstall plugins you installed for Claude Code, WorkBuddy, and Codex, right from the app.
3. Codex's built-in skills can't be deleted, but you can now disable them in their details.
4. Background scans no longer interrupt a plugin uninstall, and the list refreshes once it finishes.
5. WorkBuddy's preinstalled plugins are now treated as built-in and can't be uninstalled.

## 0.5.0

中文

1. feat(import): read GitHub-backed ClawHub skills from their GitHub repository
2. feat(update): show a dot on the settings gear when an app update is found
3. feat(market): show ClawHub author avatars
4. feat(market): add marketsnap to build market-extras.json
5. feat(market): add the cloud market page
6. feat(import): read ClawHub links in the add page
7. feat(market): fetch and cache skills.sh and ClawHub listings
8. feat(import): add ClawHub as an import source
9. fix(i18n): use the word Skill throughout the Chinese UI
10. fix(i18n): rename the market and skill updates pages in Chinese
11. fix(update): drop the hint under the automatic app update switch
12. fix(sidebar): leave more room between the toolbar and the first item
13. fix(market): use an upload icon for importing from the computer
14. fix(market): restyle filter chips and list skills in one column
15. fix(market): make search box span toolbar width and focus snapshots
16. fix(market): fix dark mode fallback icon contrast and search group keys

English

1. feat(import): read GitHub-backed ClawHub skills from their GitHub repository
2. feat(update): show a dot on the settings gear when an app update is found
3. feat(market): show ClawHub author avatars
4. feat(market): add marketsnap to build market-extras.json
5. feat(market): add the cloud market page
6. feat(import): read ClawHub links in the add page
7. feat(market): fetch and cache skills.sh and ClawHub listings
8. feat(import): add ClawHub as an import source
9. fix(i18n): use the word Skill throughout the Chinese UI
10. fix(i18n): rename the market and skill updates pages in Chinese
11. fix(update): drop the hint under the automatic app update switch
12. fix(sidebar): leave more room between the toolbar and the first item
13. fix(market): use an upload icon for importing from the computer
14. fix(market): restyle filter chips and list skills in one column
15. fix(market): make search box span toolbar width and focus snapshots
16. fix(market): fix dark mode fallback icon contrast and search group keys

## 0.4.0

中文

1. feat(browse): follow relative Markdown links in the reader
2. feat(delete): list the paths a deletion touches in the confirm dialog

English

1. feat(browse): follow relative Markdown links in the reader
2. feat(delete): list the paths a deletion touches in the confirm dialog

## 0.3.1

中文

1. fix(library): show hidden skill files and allow jumping to security evidence

English

1. fix(library): show hidden skill files and allow jumping to security evidence

## 0.3.0

中文

1. feat(browse): list uncommitted project skills in the toolbar
2. feat(library): read git state of project skills

English

1. feat(browse): list uncommitted project skills in the toolbar
2. feat(library): read git state of project skills

## 0.2.0

中文

1. 应用可以自己更新了：菜单「检查应用更新…」或「设置 › 通用 › 应用更新」里检查、下载并安装新版本，之后的版本能只下载变化的部分。
2. 自动检查更新默认关闭，打开后每次启动和之后每天检查一次。
3. 英文界面下，技能卡片底部的标记不再被截断。

English

1. Skill Stow now updates itself: check, download and install new versions from Check for App Updates… or Settings › General › App Updates. Later versions download only what changed when they can.
2. Automatic update checks are off by default. When on, the app checks at each launch and once a day after that.
3. In the English interface, the flags at the bottom of skill cards are no longer cut off.
