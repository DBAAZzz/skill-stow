# 官网 skillstow.xyz

静态站，由 `app/tools/sitegen` 生成，部署为 Cloudflare 上只有静态资源的 Worker `skill-stow`（配置见 `wrangler.jsonc`，只挂在自定义域名 skillstow.xyz 上，workers.dev 地址已关闭，本地预览用下面的 `-serve`）。

```sh
cd app
go run ./tools/sitegen                 # 生成到 site/dist/
go run ./tools/sitegen -serve :8788    # 生成后在 http://localhost:8788 预览
go test ./tools/sitegen                # 检查标题、断链、结构化数据、sitemap
cd ../site && npx wrangler deploy      # 手动部署 dist/（需先 npx wrangler login）
```

## 目录

- `templates/base.html`：每页共用的 `<head>`、顶栏和页脚；`partials.html`：下载按钮、Agent 列表、目录表等片段。
- `pages/en/`、`pages/zh/`：各页的正文，每个文件定义 `title`、`description`、`main` 三段。英文在根路径，中文在 `/zh/`。
- `i18n/en.json`、`i18n/zh.json`：模板共用的短文案（`{{t "key"}}`），以及 Agent 名称的本地化（如通义灵码）。
- `static/`：原样复制的文件（样式、图标、OG 图、`_headers`）。首页另有 `home.css`、`home.js`，只在首页加载。
- 截图直接取 `docs/screenshots/<语言>/`，README 截图更新后网站跟着更新。生成时用 `cwebp`（`brew install webp`）给每张截图转一份 WebP，页面用 `<picture>` 优先加载 WebP，体积约为 PNG 的四分之一；本机没装 `cwebp` 时只出 PNG 并提示一句，CI 里装了，部署出去的一定带 WebP。

## 页面从哪来

- 首页、`/agent-skills/`（指南长文）、`/agents/`（目录对照表）各一份模板。
- `/agents/<id>/` 每个 Agent 一页，数据来自 `library.Catalog`：在 `internal/library/agents.go` 加了 Agent，重新生成就多一页。
- 能按 Skill 开关的 Agent 及其配置写法在 `app/tools/sitegen/agents.go` 的 `nativeConfigs`，测试会核对它和 `library.NativeToggleSupported` 一致。
- 首页的 Agent 图标墙和开关演示里的图标读自应用的 `app/agents.go`（`brands` 表）和 `app/assets/agents/`，和应用里画法一致；表里出现生成器不认识的写法时构建会报错。
- 首页的特写卡片是截图的一块：`{{crop "security" x0 y0 x1 y1 "说明"}}`，坐标是 2x 截图上的像素，中英文截图版面略有差别，两份页面各写各的坐标。截图重新生成后，对照一下首屏和「检查」那一屏的扫描高亮是否还对得上。
- `sitemap.xml`（带 hreflang）、`robots.txt`、`llms.txt` 和每页的 JSON-LD 由生成器写出。页面里写成 `<details class="qa">` 的问答会自动进 FAQPage。

## 写页面时

- 中文页的字体是 MiSans，从小米官方网页字体服务引入（`base.html`，按字切片，只下载用到的字），英文页用系统字体。MiSans 许可协议不允许改编或自行分发字体文件，所以不要把字体下载下来裁剪后放进 `static/`；协议要求注明使用了 MiSans，中文页页脚有这一句，别删。

- 首页的动画都由 `home.js` 加 `.in` 触发，页面本身写成动画的终态：没有 JS 或系统开了「减少动态效果」时内容照常显示。
- 问答保持 `<details class="qa"><summary>…</summary>…</details>` 的写法（`class` 只能是 `qa`），否则进不了 FAQPage。

- 中英文两份一起改，标题和描述写用户会搜的词，不只写品牌名。
- 和应用界面一样，不用点号把几段信息拼成一行，并列信息用 `meta-line`（测试会查）。
- 页面上写的功能和行为要和 README、应用一致；Agent 目录、配置文件这类事实从数据生成，不手写。

## 部署

`main` 上 `site/`、截图、`agents.go` 或生成器有改动时，`.github/workflows/site.yml` 会测试、生成并在 `site/` 下执行 `wrangler deploy`。需要仓库 secret `CLOUDFLARE_API_TOKEN`（用「Edit Cloudflare Workers」模板创建）和 `CLOUDFLARE_ACCOUNT_ID`。拉取请求只生成和测试，不部署。

找不到的路径返回 `404.html`（`not_found_handling`），不带结尾斜杠的目录地址自动跳到带斜杠的版本，和 canonical 一致。
