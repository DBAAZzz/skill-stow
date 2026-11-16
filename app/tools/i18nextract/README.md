# i18nextract

标准库实现的 Go 界面文案提取与批量改写工具。它只做机械迁移；key、翻译、业务语义和错误消息由使用者审核。所有命令从 `app/` 运行。

```sh
go run ./tools/i18nextract scan -prefix usage usage_page.go > plan.json
# 审核 plan.json：修改 key，填写 en/description，或把 kind 改成 skip。
go run ./tools/i18nextract apply -plan plan.json -catalog locales
# 需要同时生成类型化函数时：
go run ./tools/i18nextract apply -plan plan.json -catalog locales -generate
```

## Scan

- 按命令行文件顺序、源码出现顺序输出；重复文件只扫描一次，忽略 `_test.go` 和 `*_gen.go`。路径保留为清理后的输入路径，位置使用 Go 的实际字节列号，不受 `//line` 指令影响。
- 解释字符串与 raw string 都参与扫描；CJK 范围为 U+4E00–U+9FFF、U+3000–U+303F、U+FF00–U+FFEF。注释不参与。
- `literal` 可自动替换；`sprintf` 支持 `%d`、`%s`、`%v` 和 `%%`，识别 `fmt` 导入别名。`zh` 已转义 ICU 的字面大括号和单引号。
- 函数体内 `errors.New("中文")` 标为 `errorsNew`；`fmt.Errorf` 标为 `errorf`，占位符规则与 sprintf 相同。无 `%w` 时整条消息提取；只有末尾单个 `%w` 时提取它前面的完整文字（保留冒号、空格等），将最后一个实参单独放到 `wrapped` 字段，不放进 `args` / `placeholders`。`%%w` 是字面 `%w`，不算错误包装。中间 `%w`、多个 `%w`、其他不支持格式仍为 manual。包级错误声明保持 manual。
- `manual` 保留原因：包级声明、本地常量、struct tag、比较/switch case、map key、字符串拼接、不支持的错误包装、`Key` 参数、Go 指令附近、非支持格式动词/变参/参数个数不符等。仅靠 AST 无法证明某段表达式可安全替换时采用保守分类。
- 占位符按参数顺序排列，标识符和选择器名转小驼峰，`len(x)` 用 `count`，其他表达式用 `p0` 等，重名从后缀 `2` 开始避让。相同 `zh` 和有序占位符复用 key。

## Apply

- `file` 路径相对于执行命令的工作目录（不是 plan 所在目录）。请在与 scan 相同的目录执行。
- 仅允许修改 `key`、`en`、`description` 和把 `kind` 改为 `skip`。`manual` 和 `skip` 不写入 catalog，不改源码。源码位置、原文、分类、参数、`wrapped`、占位符、`zh` 必须与重新扫描一致，过期计划会失败；成功后再次执行同一计划也会因源码已变更而失败。
- 将整个 `fmt.Sprintf` 替换成 locale 调用，支持其参数内嵌套的其他待提取文案；按字节偏移从后往前替换，最后 gofmt。保留其他 `fmt` 使用，清除不再使用的 `fmt` 导入；复用已有 locales 导入别名，必要时为新导入选择不冲突的名字。新增 locales 导入插进现有 `import (...)` 的本模块组并由 gofmt 按导入路径排序，没有本模块组就在末尾追加空行分组；单条 import 会扩展成分组，无导入时新建声明。现有分组和导入注释保留。cgo 的 `import "C"` 及前导注释保持独立。
- `errorsNew` 改为 `errors.New(locales.Key())`，保留原 errors 导入别名。无 `%w` 的 `errorf` 改为 `errors.New(locales.Key(args...))`，必要时将 errors 加到标准库组并删除不再使用的 fmt；末尾 `%w` 的形式改为 `fmt.Errorf("%s%w", locales.Key(args...), wrapped)`，保留 fmt 别名及错误链。支持 wrapped 表达式中嵌套的待迁移消息。
- `%v`/`%s` 生成为 `String`，`%d` 生成为 `int`，实参保持原 Go 表达式。扫描不做类型推断：非 string 的 `%v`、非 int 的 `%d`、`[]byte` 的 `%s` 等需要人工修订业务代码或标记 `skip`，随后通过生成与编译发现类型不匹配。
- catalog 保留已有顶层 key 与已有 metadata 的顺序及内容，新消息追加。新源消息紧接着写 `@key`（含 description 和有序 placeholders），description 默认 `file:line`。只在 plan 明确给了 `en` 时合并对应翻译（空字符串也是明确翻译）。缺失 catalog 会创建并添加 `@@locale`。
- 同名消息的文字、占位符类型或顺序冲突，重复位置、非法 key、生成函数名冲突、格式错误等均在写入前拒绝。已有 metadata 的 description/example 等非签名信息保持原样。
- 所有校验、改写、格式化、JSON 编码完成后，先将全部结果暂存到同目录临时文件，再逐文件 rename。校验失败不写任何目标文件；暂存失败不改目标文件。跨文件 rename 并非文件系统事务，最终写入阶段遭遇 I/O 故障时错误会指出可能已有部分文件写入。
- 输出 JSON 为 2 空格缩进，保留中文和 HTML 字符，末尾换行。摘要列出每个源码文件的替换数、新增源 key 数和英文翻译数。
- `-generate` 在 catalog 父目录执行 `go tool mygo generate`。生成失败会明确报告“文件已应用”，不会回滚已审核改写。

## 验证

```sh
go vet ./tools/i18nextract/...
go test ./tools/i18nextract/...
go build ./...
```

`testdata/scan.go` 和 `testdata/errors.go` 是仅供 AST 解析的综合样例（故意含不受支持的格式串和不存在的 UI 调用），不应单独编译；golden JSON 独立记录预期分类。集成测试在临时 module 中改写并编译，包含导入分组、别名冲突和 `errors.Is` 错误链检查，真实业务文件只做只读扫描。
