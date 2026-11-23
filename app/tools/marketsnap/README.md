# marketsnap

从 `skills.sh` 公开 HTML 网页采集技能简介与专题信息，生成供客户端按 `ETag` 拉取的 `market-extras.json`。直接复用 [internal/library](file:///Users/mac/Documents/workspace/skill%20%E7%AE%A1%E7%90%86/.worktrees/cloud-market/app/internal/library/market.go) 的 `MarketExtras` 结构定义。所有命令从 `app/` 执行。

## 用法

```sh
# 小范围本地实跑（只抓前 20 个技能页面验证输出，不覆盖任何线上文件）
go run ./tools/marketsnap -limit 20 -out /tmp/market-extras.json

# 完整构建（读取上一份缓存文件按 30 天轮换增量刷新）
go run ./tools/marketsnap -prev /tmp/prev-market-extras.json -out /tmp/market-extras.json
```

## 采集与合规约束

- 只请求 `/`、`/trending`、`/hot`、`/topic`、`/topic/<id>` 与技能详情公开网页 `/<owner>/<repo>/<skill>`；代码内明确拒绝 `/api/` 与 `/search` 路径以遵守站点 `robots.txt`。
- 带明确工具标识的 `User-Agent`（默认 `skill-stow-marketsnap/1.0 (+https://github.com/DBAAZzz/skill-stow)`，不含个人邮箱）。
- 全局节流（默认 `-interval 100ms`）与有界并发池（默认 `-concurrency 4`）；遇 `429` 或 `5xx` 按 `Retry-After` 或指数退避最多重试 `-max-retries` 次。
- 若提供上一份 `market-extras.json`（`-prev` 或 `-prev-url`），已缓存简介按确定性每日 `1 / -rotate-days`（默认 30 天一轮）重取，其余复用旧值；重取单页若短暂失败则保留旧简介。

## 发布前校验与原子写入

全部页面解析、编码及以下任一校验全部通过后，才会通过同目录临时文件原子重命名写入 `-out`；失败时返回非 0 退出码并保持已有 `-out` 文件原样不动：

- 专题列表或任一专题的技能列表为空；
- 简介总数为 0 或低于 `-min-skills`；
- 全量运行时（`-limit=0` 且存在 `-prev`），简介数量较上一份减少超过 50%；
- 输出 JSON 体积超过 `-max-bytes`（默认 4 MiB），或回读反序列化失败。

## 测试

```sh
go test ./tools/marketsnap/...
```
