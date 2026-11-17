# 应用图标源文件

| 文件 | 用途 |
|---|---|
| `../resources/icon.png` | 完整图标（1024，透明底，官方圆角）。mygo 用它生成 Windows / Linux 图标，也是下面 `.icns` 的大尺寸来源 |
| `icon-small.png` | 16pt（32px）专用的简化图：只留卡套、一张卡片和放大的星星，小尺寸下不糊 |
| `AppIcon.icns` | 打包实际使用的 macOS 图标（`mygo.json` 的 `macos.icon`），32px 来自简化图，64px 起来自完整图 |
| `AppIcon.icon/` | Icon Composer 分层文档（矢量重画：卡套、星星、卡片三组），供 macOS 26 的深色 / 着色 / 透明模式使用，**尚未启用** |

改了 `icon.png` 或 `icon-small.png` 后，在 `app/` 下重新生成 `.icns`：

```sh
go run ./tools/appicon -small appicon/icon-small.png -o appicon/AppIcon.icns resources/icon.png
```

## 启用 AppIcon.icon

编译 `.icon` 需要 Xcode 26（macOS 15.6 及以上）里的 `actool`。装好后：

1. 用 Icon Composer 打开 `AppIcon.icon`，检查分组层次（`icon.json` 里排在前面的组在最上层：卡套 → 星星 → 卡片）、玻璃 / 阴影参数，以及深色、着色模式的效果。
2. 把 `mygo.json` 的 `macos.icon` 改成 `appicon/AppIcon.icon`，`go tool mygo build` 会用 actool 编译。

注意：`macos.icon` 只能填一个。启用 `.icon` 后，系统会自己渲染所有尺寸，`icon-small.png` 的小尺寸专用图就不再生效。
